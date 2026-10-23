package webhook

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testSecrets(t *testing.T) *Secrets {
	t.Helper()
	s, err := NewSecrets(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSecretDerivation(t *testing.T) {
	s := testSecrets(t)
	salt := NewSalt()
	a, b := s.Secret(salt), s.Secret(salt)
	if a != b || !strings.HasPrefix(a, SecretPrefix) || len(a) != len(SecretPrefix)+40 {
		t.Fatalf("secret %q / %q", a, b)
	}
	if s.Secret(NewSalt()) == a {
		t.Fatal("a new salt must give a new secret")
	}
	other, _ := NewSecrets(append(make([]byte, 31), 1))
	if other.Secret(salt) == a {
		t.Fatal("another server key must give another secret")
	}
	if p := ShownPrefix(a); p != a[:14] {
		t.Fatalf("prefix %q", p)
	}
	if _, err := NewSecrets([]byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestSignVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"id":"srch_x"}`)
	h := Sign("whsec_abc", now, body)
	if !strings.HasPrefix(h, "t=1800000000,v1=") {
		t.Fatalf("header %q", h)
	}
	if err := Verify("whsec_abc", h, body, now.Add(time.Minute), 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"other secret": Verify("whsec_abd", h, body, now, 5*time.Minute),
		"other body":   Verify("whsec_abc", h, []byte(`{"id":"srch_y"}`), now, 5*time.Minute),
		"garbage":      Verify("whsec_abc", "nonsense", body, now, 5*time.Minute),
	} {
		if !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := Verify("whsec_abc", h, body, now.Add(10*time.Minute), 5*time.Minute); !errors.Is(err, ErrTooOld) {
		t.Errorf("old: %v", err)
	}
}

func TestCheckURL(t *testing.T) {
	for _, u := range []string{"https://example.com/hook", "https://hooks.example.com:8443/a?b=c", "https://8.8.8.8/x"} {
		if err := CheckURL(u, false); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"", "example.com/hook", "http://example.com/hook", "ftp://example.com", "https://user:pw@example.com/",
		"https://127.0.0.1/", "https://10.0.0.5/", "https://[::1]/", "https://169.254.169.254/latest", "https://localhost/",
		"https://api.localhost/", "https://192.168.1.2/", "https://100.64.0.1/", "https://example.com/#x",
		"https://example.com/" + strings.Repeat("a", MaxURLLen)} {
		if err := CheckURL(u, false); !errors.Is(err, ErrInvalidURL) {
			t.Errorf("%q accepted: %v", u, err)
		}
	}
	if err := CheckURL("http://127.0.0.1:9000/hook", true); err != nil {
		t.Errorf("allowPrivate: %v", err)
	}
}

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{"8.8.8.8": true, "2606:4700::1111": true, "127.0.0.1": false, "10.1.2.3": false,
		"172.16.0.1": false, "192.168.0.1": false, "169.254.169.254": false, "::1": false, "fd00::1": false, "fe80::1": false,
		"::ffff:127.0.0.1": false, "0.0.0.0": false, "100.100.1.1": false, "224.0.0.1": false} {
		if got := Public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
}

func TestDeliverSignedAndRetried(t *testing.T) {
	var calls atomic.Int32
	var gotBody []byte
	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		gotBody, _ = io.ReadAll(r.Body)
		gotHeader = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := NewSender(Options{AllowPrivate: true, Backoff: []time.Duration{time.Millisecond, time.Millisecond}})
	body := []byte(`{"id":"srch_1","status":"done"}`)
	res := s.Deliver(context.Background(), Message{URL: srv.URL + "/hook", Secret: "whsec_k", Event: EventCompleted, Delivery: "srch_1", Body: body})
	if res.Status != StatusDelivered || res.Attempts != 2 || res.HTTPStatus != 204 {
		t.Fatalf("result %+v", res)
	}
	if string(gotBody) != string(body) || gotHeader.Get(HeaderEvent) != EventCompleted || gotHeader.Get(HeaderDelivery) != "srch_1" {
		t.Fatalf("body %s headers %v", gotBody, gotHeader)
	}
	if err := Verify("whsec_k", gotHeader.Get(HeaderSignature), gotBody, time.Now(), time.Minute); err != nil {
		t.Fatal(err)
	}
}

func TestDeliverStopsOnClientError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()
	s := NewSender(Options{AllowPrivate: true, Backoff: []time.Duration{time.Millisecond}})
	res := s.Deliver(context.Background(), Message{URL: srv.URL, Secret: "whsec_k", Event: EventFailed, Delivery: "d", Body: []byte("{}")})
	if res.Status != StatusFailed || res.Attempts != 1 || res.HTTPStatus != 410 || calls.Load() != 1 {
		t.Fatalf("result %+v calls %d", res, calls.Load())
	}
}

func TestDeliverNoRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("redirect followed") }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	s := NewSender(Options{AllowPrivate: true, Backoff: []time.Duration{}})
	if res := s.Deliver(context.Background(), Message{URL: srv.URL, Secret: "k", Body: []byte("{}")}); res.Status != StatusFailed || res.HTTPStatus != 307 {
		t.Fatalf("result %+v", res)
	}
}

func TestDeliverRefusesInternalAddress(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1) }))
	defer srv.Close()
	// The test server listens on 127.0.0.1: refused at dial time, also
	// when reached through a host name ("localhost" resolves to it).
	s := NewSender(Options{Backoff: []time.Duration{}})
	for _, u := range []string{srv.URL, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)} {
		res := s.Deliver(context.Background(), Message{URL: u, Secret: "k", Body: []byte("{}")})
		if res.Status != StatusFailed || !strings.Contains(res.Error, "not public") {
			t.Errorf("%s: %+v", u, res)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("internal address reached")
	}
}
