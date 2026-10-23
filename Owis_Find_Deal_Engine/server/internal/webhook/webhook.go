// Package webhook signs and sends the webhooks of the asynchronous search:
// when a search job is done, the API client gets a POST with the job at
// the URL it gave, signed with its own secret (HMAC-SHA256), so it can
// check that the call comes from this service and was not changed.
//
// Signature header (the same scheme as Stripe's):
//
//	X-OFD-Signature: t=<unix seconds>,v1=<hex HMAC-SHA256(secret, "<t>.<body>")>
//
// The receiver recomputes the HMAC over the timestamp, a dot and the raw
// body, compares it in constant time, and refuses old timestamps (e.g.
// more than 5 minutes) so a recorded call cannot be replayed.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Headers of a webhook call.
const (
	HeaderSignature = "X-OFD-Signature"
	HeaderEvent     = "X-OFD-Event"
	HeaderDelivery  = "X-OFD-Delivery" // the job id: the same for every attempt
)

// Events.
const (
	EventCompleted = "search.completed"
	EventFailed    = "search.failed"
)

// Secret format: SecretPrefix + 40 lowercase base32 characters.
const (
	SecretPrefix = "whsec_"
	saltLen      = 32
	shownChars   = 8
)

// MaxURLLen bounds a webhook URL.
const MaxURLLen = 2048

var encoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// Secrets derives the clients' signing secrets from the server's key
// (SETTINGS_ENCRYPTION_KEY): secret = HMAC(key', salt), with a random
// salt per client stored in the database. The secret itself is never
// stored.
type Secrets struct {
	key []byte
}

// NewSecrets returns Secrets for the 32-byte server key.
func NewSecrets(serverKey []byte) (*Secrets, error) {
	if len(serverKey) != 32 {
		return nil, errors.New("webhook: the server key must be 32 bytes")
	}
	m := hmac.New(sha256.New, serverKey)
	m.Write([]byte("owis_find_deal_engine webhook secret v1"))
	return &Secrets{key: m.Sum(nil)}, nil
}

// NewSalt makes the salt of a new secret.
func NewSalt() []byte {
	b := make([]byte, saltLen)
	_, _ = rand.Read(b) // never fails (crypto/rand)
	return b
}

// Secret is the signing secret of salt.
func (s *Secrets) Secret(salt []byte) string {
	m := hmac.New(sha256.New, s.key)
	m.Write(salt)
	return SecretPrefix + encoding.EncodeToString(m.Sum(nil)[:25])
}

// ShownPrefix is the part of a secret that may be shown again.
func ShownPrefix(secret string) string {
	return secret[:min(len(secret), len(SecretPrefix)+shownChars)]
}

// Sign returns the signature header value for body sent at t.
func Sign(secret string, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac(secret, ts, body))
}

func mac(secret, ts string, body []byte) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return m.Sum(nil)
}

// Errors of Verify.
var (
	ErrBadSignature = errors.New("webhook: signature does not match")
	ErrTooOld       = errors.New("webhook: timestamp outside the tolerance")
)

// Verify checks a signature header as a receiver does: the HMAC must match
// and the timestamp must be within tolerance of now.
func Verify(secret, header string, body []byte, now time.Time, tolerance time.Duration) error {
	var ts string
	var sigs [][]byte
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			if b, err := hex.DecodeString(v); err == nil {
				sigs = append(sigs, b)
			}
		}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(sigs) == 0 {
		return ErrBadSignature
	}
	if d := now.Sub(time.Unix(sec, 0)); d > tolerance || d < -tolerance {
		return ErrTooOld
	}
	want := mac(secret, ts, body)
	for _, s := range sigs {
		if hmac.Equal(s, want) {
			return nil
		}
	}
	return ErrBadSignature
}

// ErrInvalidURL wraps every error of CheckURL.
var ErrInvalidURL = errors.New("invalid webhook_url")

// CheckURL checks a webhook URL given by a client: https (http only when
// allowPrivate, for development), a host, no user name or password, and
// no internal address when the host is an IP address. Host names are
// checked again when the call is made (see Sender).
func CheckURL(raw string, allowPrivate bool) error {
	bad := func(msg string) error { return fmt.Errorf("%w: %s", ErrInvalidURL, msg) }
	if len(raw) > MaxURLLen {
		return bad(fmt.Sprintf("must not be longer than %d characters", MaxURLLen))
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil || !u.IsAbs() || u.Host == "" || u.Opaque != "":
		return bad("must be an absolute URL such as https://example.com/hooks/search")
	case u.User != nil:
		return bad("must not contain a user name or password")
	case u.Fragment != "":
		return bad("must not contain a fragment (#...)")
	case u.Scheme == "http" && !allowPrivate:
		return bad("must use https")
	case u.Scheme != "https" && u.Scheme != "http":
		return bad("must use https")
	}
	if !allowPrivate {
		host := u.Hostname()
		if a, err := netip.ParseAddr(host); err == nil && !Public(a) {
			return bad("must not point to an internal address")
		}
		if h := strings.ToLower(strings.TrimSuffix(host, ".")); h == "localhost" || strings.HasSuffix(h, ".localhost") {
			return bad("must not point to an internal address")
		}
	}
	return nil
}

// Public reports whether a is a public internet address (not loopback,
// private, link-local, shared CGNAT, multicast, documentation, ...).
func Public(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // shared address space (CGNAT)
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64 can reach IPv4 inside
	netip.MustParsePrefix("2001:db8::/32"),
}

// Options of a Sender.
type Options struct {
	// AllowPrivate lets webhooks reach internal addresses and plain http
	// (development only: otherwise a client could make the server call
	// Redis, Postgres or the cloud metadata service).
	AllowPrivate bool
	// Timeout of one attempt (default 10 s).
	Timeout time.Duration
	// Backoff is the wait before each retry (default 5 s, 30 s: three
	// attempts in all).
	Backoff []time.Duration
}

// Sender delivers webhooks. Safe for concurrent use.
type Sender struct {
	client  *http.Client
	backoff []time.Duration
	sleep   func(ctx context.Context, d time.Duration) error
	now     func() time.Time
}

// NewSender returns a Sender.
func NewSender(o Options) *Sender {
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.Backoff == nil {
		o.Backoff = []time.Duration{5 * time.Second, 30 * time.Second}
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if !o.AllowPrivate {
		// Checked on the address actually dialled, after DNS, so a host
		// name that resolves to an internal address is refused too.
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !Public(ap.Addr()) {
				return fmt.Errorf("webhook: address %s is not public", address)
			}
			return nil
		}
	}
	transport := &http.Transport{
		Proxy:                 nil, // straight to the receiver
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: o.Timeout,
		MaxIdleConns:          20,
		IdleConnTimeout:       30 * time.Second,
	}
	return &Sender{
		client: &http.Client{
			Transport: transport,
			Timeout:   o.Timeout,
			// A redirect could lead anywhere; the receiver must answer itself.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		backoff: o.Backoff,
		sleep:   sleepCtx,
		now:     time.Now,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Delivery statuses.
const (
	StatusPending   = "pending"
	StatusDelivered = "delivered"
	StatusFailed    = "failed"
)

// Result is how a delivery went.
type Result struct {
	Status     string `json:"status"` // delivered, failed
	Attempts   int    `json:"attempts"`
	HTTPStatus int    `json:"http_status,omitempty"` // of the last answer
	Error      string `json:"error,omitempty"`
}

// Message is one webhook to send.
type Message struct {
	URL      string
	Secret   string
	Event    string
	Delivery string // id, the same for every attempt
	Body     []byte
}

// Deliver sends m, retrying on network errors, 408, 429 and 5xx answers.
// Any 2xx answer counts as delivered. Each attempt is signed afresh.
func (s *Sender) Deliver(ctx context.Context, m Message) Result {
	var res Result
	for attempt := 0; ; attempt++ {
		res.Attempts = attempt + 1
		status, err := s.attempt(ctx, m)
		res.HTTPStatus = status
		switch {
		case err == nil && status >= 200 && status < 300:
			res.Status, res.Error = StatusDelivered, ""
			return res
		case err != nil:
			res.Error = err.Error()
		default:
			res.Error = fmt.Sprintf("receiver answered %d", status)
		}
		retry := err != nil || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
		if !retry || attempt >= len(s.backoff) || s.sleep(ctx, s.backoff[attempt]) != nil {
			res.Status = StatusFailed
			return res
		}
	}
}

func (s *Sender) attempt(ctx context.Context, m Message) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.URL, bytes.NewReader(m.Body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "owis-find-deal-engine-webhook/1")
	req.Header.Set(HeaderEvent, m.Event)
	req.Header.Set(HeaderDelivery, m.Delivery)
	req.Header.Set(HeaderSignature, Sign(m.Secret, s.now(), m.Body))
	resp, err := s.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // without the URL, which the client knows
		}
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()
	return resp.StatusCode, nil
}
