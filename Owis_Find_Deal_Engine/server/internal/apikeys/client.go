package apikeys

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"owis_find_deal_engine/internal/store"
)

// Scopes a client can be given.
const (
	ScopeSearch    = "search"    // POST /api/v1/search
	ScopeCountries = "countries" // the list of countries (public on /api/v1/countries; the MCP tool of step 13)
)

// Scopes are all scopes, in display order.
var Scopes = []string{ScopeSearch, ScopeCountries}

// Defaults of a new client.
const (
	DefaultDailyQuota = 100
	DefaultRateRPS    = 1.0
	DefaultRateBurst  = 5
)

// Limits of the client settings.
const (
	MaxDailyQuota = 1_000_000
	MaxRateRPS    = 100
	MaxRateBurst  = 1000
	maxIPRules    = 50
	maxNameLen    = 100
	maxOwnerLen   = 200
)

// ErrInvalidClient wraps every validation error of Normalize.
var ErrInvalidClient = errors.New("invalid API client")

// Normalize checks the settings of a client and puts them in canonical
// form (sorted, lowercase countries, IP rules as CIDRs). countries are the
// codes a client may be limited to; now decides whether an expiry is in
// the past (only checked when checkExpiry, so an unchanged past expiry
// can stay when other settings change).
func Normalize(c *store.Client, countries []string, now time.Time, checkExpiry bool) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidClient, fmt.Sprintf(format, a...))
	}
	c.Name = strings.TrimSpace(c.Name)
	c.Owner = strings.TrimSpace(c.Owner)
	switch {
	case c.Name == "":
		return bad("name is required")
	case utf8.RuneCountInString(c.Name) > maxNameLen:
		return bad("name must not be longer than %d characters", maxNameLen)
	case utf8.RuneCountInString(c.Owner) > maxOwnerLen:
		return bad("owner must not be longer than %d characters", maxOwnerLen)
	case c.DailyQuota < 1 || c.DailyQuota > MaxDailyQuota:
		return bad("daily_quota must be from 1 to %d", MaxDailyQuota)
	case !(c.RateRPS >= 0.01 && c.RateRPS <= MaxRateRPS):
		return bad("rate_limit_rps must be from 0.01 to %d", MaxRateRPS)
	case c.RateBurst < 1 || c.RateBurst > MaxRateBurst:
		return bad("rate_limit_burst must be from 1 to %d", MaxRateBurst)
	case checkExpiry && c.ExpiresAt != nil && !c.ExpiresAt.After(now):
		return bad("expires_at must be in the future")
	}

	scopes := []string{}
	for _, s := range c.Scopes {
		s = strings.ToLower(strings.TrimSpace(s))
		if !slices.Contains(Scopes, s) {
			return bad("scope %q is unknown (known: %s)", s, strings.Join(Scopes, ", "))
		}
		if !slices.Contains(scopes, s) {
			scopes = append(scopes, s)
		}
	}
	if len(scopes) == 0 {
		return bad("at least one scope is required (%s)", strings.Join(Scopes, ", "))
	}
	slices.Sort(scopes)
	c.Scopes = scopes

	allowed := []string{}
	for _, code := range c.Countries {
		code = strings.ToLower(strings.TrimSpace(code))
		if !slices.Contains(countries, code) {
			return bad("country %q is unknown (known: %s)", code, strings.Join(countries, ", "))
		}
		if !slices.Contains(allowed, code) {
			allowed = append(allowed, code)
		}
	}
	slices.Sort(allowed)
	c.Countries = allowed

	if len(c.IPAllow) > maxIPRules {
		return bad("at most %d IP rules", maxIPRules)
	}
	rules := []string{}
	for _, r := range c.IPAllow {
		p, err := parseRule(strings.TrimSpace(r))
		if err != nil {
			return bad("ip_allow %q must be an IP address or a CIDR range (203.0.113.7, 203.0.113.0/24, 2001:db8::/32)", r)
		}
		if s := p.String(); !slices.Contains(rules, s) {
			rules = append(rules, s)
		}
	}
	c.IPAllow = rules
	return nil
}

func parseRule(r string) (netip.Prefix, error) {
	if strings.Contains(r, "/") {
		p, err := netip.ParsePrefix(r)
		return p.Masked(), err
	}
	a, err := netip.ParseAddr(r)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// AllowedIP reports whether the client may call from ip (an empty list
// allows any address).
func AllowedIP(c store.Client, ip string) bool {
	if len(c.IPAllow) == 0 {
		return true
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, r := range c.IPAllow {
		if p, err := netip.ParsePrefix(r); err == nil && p.Contains(a) {
			return true
		}
	}
	return false
}

// AllowedCountry reports whether the client may search in country (an
// empty list allows every country).
func AllowedCountry(c store.Client, country string) bool {
	return len(c.Countries) == 0 || slices.Contains(c.Countries, strings.ToLower(strings.TrimSpace(country)))
}

// HasScope reports whether the client has scope.
func HasScope(c store.Client, scope string) bool { return slices.Contains(c.Scopes, scope) }
