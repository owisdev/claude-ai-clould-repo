package api

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestWithClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.203.20.10/32")}
	cases := []struct {
		name, remote, header, want string
	}{
		{"tunnel with header", "10.203.20.10:41000", "203.0.113.7", "203.0.113.7"},
		{"tunnel, IPv6 caller", "10.203.20.10:41000", "2001:db8::1", "2001:db8::1"},
		{"tunnel, spaces", "10.203.20.10:41000", " 203.0.113.7 ", "203.0.113.7"},
		{"tunnel without header", "10.203.20.10:41000", "", "10.203.20.10"},
		{"tunnel, bad header", "10.203.20.10:41000", "203.0.113.7, 198.51.100.1", "10.203.20.10"},
		{"fake header from outside", "192.0.2.50:5555", "203.0.113.7", "192.0.2.50"},
		{"neighbour in the subnet", "10.203.20.11:5555", "203.0.113.7", "10.203.20.11"},
		{"IPv4-mapped tunnel", "[::ffff:10.203.20.10]:41000", "203.0.113.7", "203.0.113.7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			h := WithClientIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = remoteIP(r) }))
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = c.remote
			if c.header != "" {
				req.Header.Set(ClientIPHeader, c.header)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != c.want {
				t.Errorf("remoteIP = %q, want %q", got, c.want)
			}
		})
	}
}

// Without WithClientIP (or with no trusted proxies) the header never counts.
func TestRemoteIPWithoutTrust(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.50:5555"
	req.Header.Set(ClientIPHeader, "203.0.113.7")
	if got := remoteIP(req); got != "192.0.2.50" {
		t.Errorf("no middleware: %q", got)
	}
	var got string
	WithClientIP(nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = remoteIP(r) })).
		ServeHTTP(httptest.NewRecorder(), req)
	if got != "192.0.2.50" {
		t.Errorf("no trusted proxies: %q", got)
	}
}
