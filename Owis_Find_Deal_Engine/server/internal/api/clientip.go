package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIPHeader carries the caller's own address when the request comes
// through Cloudflare (here: the Cloudflare Tunnel's cloudflared).
const ClientIPHeader = "CF-Connecting-IP"

type clientIPKey struct{}

// WithClientIP finds each request's caller address once: the connection's
// address, or ClientIPHeader when the connection comes from a trusted
// proxy (config TrustedProxies). Anyone can send the header, so it is
// ignored from everywhere else. Wrap the whole server with it; remoteIP
// reads the result.
func WithClientIP(trusted []netip.Prefix) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := connIP(r)
			if a, err := netip.ParseAddr(ip); err == nil && isTrusted(trusted, a.Unmap()) {
				if h, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get(ClientIPHeader))); err == nil {
					ip = h.Unmap().String()
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey{}, ip)))
		})
	}
}

func isTrusted(trusted []netip.Prefix, a netip.Addr) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// remoteIP is the caller's address: the one WithClientIP found, else the
// connection's address. Used by the admin per-IP limit, API key IP
// allow-lists, the audit log and the request log.
func remoteIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey{}).(string); ok {
		return ip
	}
	return connIP(r)
}

// connIP is the address of the connection's other end, without the port.
func connIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
