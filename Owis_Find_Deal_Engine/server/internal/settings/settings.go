// Package settings keeps the runtime settings in the database: it decrypts
// secrets, builds the service configuration from database values plus the
// bootstrap environment, validates every change before it is stored, and
// tells the server when the settings changed so it can apply them without
// a restart.
package settings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"owis_find_deal_engine/internal/config"
	"owis_find_deal_engine/internal/store"
)

// ImportedBy is the history author of settings copied from the environment
// on the first start with a database.
const ImportedBy = "import:env"

// Store is the settings part of store.DB.
type Store interface {
	Settings(ctx context.Context) ([]store.Setting, int64, error)
	SettingsVersion(ctx context.Context) (int64, error)
	SetSettings(ctx context.Context, by string, updates []store.Update) (int64, error)
}

// Manager reads and changes the runtime settings. Safe for concurrent use.
type Manager struct {
	store  Store
	cipher *Cipher
	env    config.Lookup // bootstrap settings (os.LookupEnv)
}

// NewManager returns a Manager. env supplies the bootstrap settings.
func NewManager(st Store, c *Cipher, env config.Lookup) *Manager {
	return &Manager{store: st, cipher: c, env: env}
}

// Values returns the stored runtime settings (secrets decrypted) and the
// settings version.
func (m *Manager) Values(ctx context.Context) (map[string]string, int64, error) {
	rows, version, err := m.store.Settings(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("load settings: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, s := range rows {
		v := s.Value
		if s.Secret {
			if v, err = m.cipher.Decrypt(s.Key, s.Value); err != nil {
				return nil, 0, fmt.Errorf("setting %s: %w", s.Key, err)
			}
		}
		out[s.Key] = v
	}
	return out, version, nil
}

// Config returns the service configuration: runtime settings from the
// database, bootstrap settings from the environment.
func (m *Manager) Config(ctx context.Context) (config.Config, int64, error) {
	values, version, err := m.Values(ctx)
	if err != nil {
		return config.Config{}, 0, err
	}
	cfg, err := config.LoadFrom(config.Layered(values, m.env))
	return cfg, version, err
}

// Set changes runtime settings: a non-nil value sets one, nil removes it
// (back to its default). The whole change is validated as the
// configuration it leads to and refused if that is invalid, so the
// running server never sees a broken setup. Secrets are encrypted.
// Changes that keep the current value are skipped. It returns the new
// version and the keys that changed.
func (m *Manager) Set(ctx context.Context, by string, changes map[string]*string) (int64, []string, error) {
	values, version, err := m.Values(ctx)
	if err != nil {
		return 0, nil, err
	}
	next := maps.Clone(values)
	var errs []error
	for _, k := range slices.Sorted(maps.Keys(changes)) {
		if !config.Runtime(k) {
			errs = append(errs, fmt.Errorf("%s is not a runtime setting (unknown, or set in the environment only)", k))
			continue
		}
		if v := changes[k]; v != nil {
			next[k] = *v
		} else {
			delete(next, k)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return 0, nil, err
	}
	if _, err := config.LoadFrom(config.Layered(next, m.env)); err != nil {
		return 0, nil, fmt.Errorf("invalid settings, nothing changed: %w", err)
	}

	var (
		updates []store.Update
		changed []string
	)
	for _, k := range slices.Sorted(maps.Keys(changes)) {
		old, had := values[k]
		v := changes[k]
		if (v == nil && !had) || (v != nil && had && *v == old) {
			continue
		}
		u := store.Update{Key: k, Secret: config.Secret(k)}
		if v != nil {
			stored := *v
			if u.Secret {
				if stored, err = m.cipher.Encrypt(k, *v); err != nil {
					return 0, nil, err
				}
			}
			u.Value = &stored
		}
		updates = append(updates, u)
		changed = append(changed, k)
	}
	if len(updates) == 0 {
		return version, nil, nil
	}
	version, err = m.store.SetSettings(ctx, by, updates)
	if err != nil {
		return 0, nil, fmt.Errorf("save settings: %w", err)
	}
	return version, changed, nil
}

// ImportEnv copies the runtime settings found in environ (os.Environ) into
// the database once: only while the settings were never changed, so it
// moves an existing .env setup into the database on the first start. It
// returns the imported keys.
func (m *Manager) ImportEnv(ctx context.Context, environ []string) ([]string, error) {
	version, err := m.store.SettingsVersion(ctx)
	if err != nil || version != 0 {
		return nil, err
	}
	changes := map[string]*string{}
	for k, v := range config.RuntimeFromEnv(environ) {
		changes[k] = &v
	}
	if len(changes) == 0 {
		return nil, nil
	}
	_, keys, err := m.Set(ctx, ImportedBy, changes)
	if err != nil {
		return nil, fmt.Errorf("import settings from the environment: %w", err)
	}
	return keys, nil
}

// IgnoredEnv returns the runtime settings in environ whose value differs
// from the database: with a database they have no effect any more.
func IgnoredEnv(values map[string]string, environ []string) []string {
	var out []string
	for k, v := range config.RuntimeFromEnv(environ) {
		if db, ok := values[k]; !ok || db != v {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// Watch checks the settings version every interval until ctx ends. When
// it changed, the new configuration is built and passed to apply. A
// configuration that cannot be built or applied is logged once and the
// running one stays.
func (m *Manager) Watch(ctx context.Context, interval time.Duration, version int64,
	apply func(config.Config, int64) error, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		v, err := m.store.SettingsVersion(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("settings: cannot check for changes", "err", err)
			}
			continue
		}
		if v == version {
			continue
		}
		cfg, loaded, err := m.Config(ctx)
		if err == nil {
			v = loaded
			err = apply(cfg, v)
		}
		if err != nil && ctx.Err() == nil {
			log.Error("settings changed but cannot be applied; the current settings stay", "version", v, "err", err)
		}
		version = v
	}
}
