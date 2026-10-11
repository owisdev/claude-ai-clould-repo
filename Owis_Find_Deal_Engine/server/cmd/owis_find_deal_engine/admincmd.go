package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"owis_find_deal_engine/internal/config"
	"owis_find_deal_engine/internal/settings"
	"owis_find_deal_engine/internal/store"
)

const adminUsage = `Usage (needs DATABASE_URL and SETTINGS_ENCRYPTION_KEY, e.g. inside the
container: docker compose exec server /owis_find_deal_engine settings list):

  settings list                     runtime settings and where each comes from
  settings get KEY                  one setting (secrets are never shown)
  settings set [-by NAME] KEY VALUE [KEY VALUE ...]
                                    change settings; VALUE "-" reads one line
                                    from stdin (for secrets: no shell history)
  settings unset [-by NAME] KEY ... back to the default
  settings history [KEY]            latest changes, newest first
  admins list                       the admin list

Changes are validated as a whole before they are stored, and the running
server applies them within SETTINGS_POLL_INTERVAL (default 10s).
`

// adminCommand runs the settings / admins command line and returns the
// process exit code.
func adminCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprint(stderr, adminUsage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if os.Getenv("DATABASE_URL") == "" {
		fmt.Fprintln(stderr, "DATABASE_URL is not set")
		return 1
	}
	db, mgr, err := connectSettings(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer db.Close()

	cmd := adminCmd{db: db, mgr: mgr, stdin: bufio.NewReader(stdin), out: stdout}
	switch args[0] + " " + args[1] {
	case "settings list":
		err = cmd.list(ctx)
	case "settings get":
		if len(args) != 3 {
			err = errUsage
			break
		}
		err = cmd.get(ctx, args[2])
	case "settings set", "settings unset":
		err = cmd.change(ctx, args[1] == "unset", args[2:])
	case "settings history":
		key := ""
		if len(args) > 2 {
			key = args[2]
		}
		err = cmd.history(ctx, key)
	case "admins list":
		err = cmd.admins(ctx)
	default:
		err = errUsage
	}
	switch {
	case errors.Is(err, errUsage):
		fmt.Fprint(stderr, adminUsage)
		return 2
	case err != nil:
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

var errUsage = errors.New("usage")

type adminCmd struct {
	db    *store.DB
	mgr   *settings.Manager
	stdin *bufio.Reader
	out   io.Writer
}

// shown hides secrets: they are write-only.
func shown(key, value string) string {
	if config.Secret(key) {
		return "(set, hidden)"
	}
	return value
}

func (c adminCmd) list(ctx context.Context) error {
	rows, version, err := c.db.Settings(ctx)
	if err != nil {
		return err
	}
	stored := map[string]store.Setting{}
	for _, s := range rows {
		stored[s.Key] = s
	}
	keys := config.RuntimeKeys()
	for k := range stored {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "KEY\tVALUE\tUPDATED\n")
	for _, k := range keys {
		s, ok := stored[k]
		if !ok {
			fmt.Fprintf(tw, "%s\t(default)\t\n", k)
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s by %s\n", k, oneLine(shown(k, s.Value)), s.UpdatedAt.UTC().Format(time.DateTime), s.UpdatedBy)
	}
	fmt.Fprintf(tw, "\nsettings version %d\n", version)
	return tw.Flush()
}

func (c adminCmd) get(ctx context.Context, key string) error {
	values, _, err := c.mgr.Values(ctx)
	if err != nil {
		return err
	}
	v, ok := values[key]
	if !ok {
		v = "(default)"
	}
	fmt.Fprintln(c.out, shown(key, v))
	return nil
}

func (c adminCmd) change(ctx context.Context, unset bool, args []string) error {
	fs := flag.NewFlagSet("settings", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	by := fs.String("by", "cli", "who makes the change (history)")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	args = fs.Args()
	changes := map[string]*string{}
	switch {
	case unset && len(args) > 0:
		for _, k := range args {
			changes[k] = nil
		}
	case !unset && len(args) > 0 && len(args)%2 == 0:
		for i := 0; i < len(args); i += 2 {
			v := args[i+1]
			if v == "-" {
				line, err := c.stdin.ReadString('\n')
				if err != nil && !(errors.Is(err, io.EOF) && line != "") {
					return fmt.Errorf("%s: no value on stdin", args[i])
				}
				v = strings.TrimRight(line, "\r\n")
			}
			changes[args[i]] = &v
		}
	default:
		return errUsage
	}
	author := "cli"
	if *by != "cli" {
		author = "cli:" + *by
	}
	version, changed, err := c.mgr.Set(ctx, author, changes)
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		fmt.Fprintln(c.out, "nothing changed (same values)")
		return nil
	}
	fmt.Fprintf(c.out, "changed %s (settings version %d)\n", strings.Join(changed, ", "), version)
	unchanged := slices.DeleteFunc(slices.Sorted(maps.Keys(changes)), func(k string) bool { return slices.Contains(changed, k) })
	if len(unchanged) > 0 {
		fmt.Fprintf(c.out, "already set: %s\n", strings.Join(unchanged, ", "))
	}
	return nil
}

func (c adminCmd) history(ctx context.Context, key string) error {
	changes, err := c.db.SettingsHistory(ctx, key, 50)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "ID\tWHEN (UTC)\tBY\tKEY\tCHANGE\n")
	for _, ch := range changes {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", ch.ID, ch.ChangedAt.UTC().Format(time.DateTime), ch.ChangedBy, ch.Key, describe(ch))
	}
	return tw.Flush()
}

func describe(ch store.Change) string {
	if ch.Secret {
		return "secret changed"
	}
	val := func(v *string) string {
		if v == nil {
			return "(default)"
		}
		return oneLine(*v)
	}
	return val(ch.Old) + " -> " + val(ch.New)
}

func (c adminCmd) admins(ctx context.Context) error {
	list, err := c.db.Admins(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "ID\tEMAIL\tSTATUS\tADDED (UTC)\tBY\n")
	for _, a := range list {
		status := "active"
		if a.DisabledAt != nil {
			status = "disabled"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", a.ID, a.Email, status, a.CreatedAt.UTC().Format(time.DateTime), a.CreatedBy)
	}
	return tw.Flush()
}

// oneLine keeps table rows on one line (JSON inputs may contain newlines).
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
