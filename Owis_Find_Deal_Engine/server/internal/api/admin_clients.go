package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"owis_find_deal_engine/internal/apikeys"
	"owis_find_deal_engine/internal/store"
)

// ClientStore keeps the API clients and their keys (store.DB).
type ClientStore interface {
	CreateClient(ctx context.Context, c store.Client, key store.NewKey, by string) (store.Client, error)
	Clients(ctx context.Context) ([]store.Client, error)
	Client(ctx context.Context, id int64) (store.Client, error)
	UpdateClient(ctx context.Context, c store.Client, by string) (store.Client, error)
	RotateKey(ctx context.Context, clientID int64, key store.NewKey, graceUntil time.Time, by string) (store.APIKey, error)
	RevokeClient(ctx context.Context, id int64, by string) error
	RevokeKey(ctx context.Context, clientID, keyID int64) error
}

// Grace period of the old keys when a client's key is rotated.
const (
	DefaultRotateGrace = 24 * time.Hour
	MaxRotateGrace     = 30 * 24 * time.Hour
)

// clientView is an API client as the admin API shows it. Keys are never
// shown, only their prefix.
type clientView struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Owner      string     `json:"owner"`
	Status     string     `json:"status"` // active, expired, revoked
	Scopes     []string   `json:"scopes"`
	DailyQuota int64      `json:"daily_quota"`
	RateRPS    float64    `json:"rate_limit_rps"`
	RateBurst  int        `json:"rate_limit_burst"`
	Countries  []string   `json:"countries"` // empty = all
	IPAllow    []string   `json:"ip_allow"`  // empty = any address
	ExpiresAt  *time.Time `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
	CreatedBy  string     `json:"created_by"`
	UpdatedAt  time.Time  `json:"updated_at"`
	UpdatedBy  string     `json:"updated_by"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	RevokedBy  string     `json:"revoked_by,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at"`
	Keys       []keyView  `json:"keys"`
}

type keyView struct {
	ID         int64      `json:"id"`
	Prefix     string     `json:"prefix"`
	Status     string     `json:"status"` // active, grace (rotated, still valid), expired, revoked
	CreatedAt  time.Time  `json:"created_at"`
	CreatedBy  string     `json:"created_by"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

func viewClient(c store.Client, now time.Time) clientView {
	v := clientView{ID: c.ID, Name: c.Name, Owner: c.Owner, Status: "active", Scopes: c.Scopes, DailyQuota: c.DailyQuota,
		RateRPS: c.RateRPS, RateBurst: c.RateBurst, Countries: c.Countries, IPAllow: c.IPAllow, ExpiresAt: utc(c.ExpiresAt),
		CreatedAt: c.CreatedAt.UTC(), CreatedBy: c.CreatedBy, UpdatedAt: c.UpdatedAt.UTC(), UpdatedBy: c.UpdatedBy,
		RevokedAt: utc(c.RevokedAt), RevokedBy: c.RevokedBy, Keys: []keyView{}}
	switch {
	case c.RevokedAt != nil:
		v.Status = "revoked"
	case c.ExpiresAt != nil && !now.Before(*c.ExpiresAt):
		v.Status = "expired"
	}
	for _, k := range c.Keys {
		kv := keyView{ID: k.ID, Prefix: k.Prefix, Status: "active", CreatedAt: k.CreatedAt.UTC(), CreatedBy: k.CreatedBy,
			ExpiresAt: utc(k.ExpiresAt), RevokedAt: utc(k.RevokedAt), LastUsedAt: utc(k.LastUsedAt)}
		switch {
		case k.RevokedAt != nil:
			kv.Status = "revoked"
		case k.ExpiresAt != nil && !now.Before(*k.ExpiresAt):
			kv.Status = "expired"
		case k.ExpiresAt != nil:
			kv.Status = "grace"
		}
		if k.LastUsedAt != nil && (v.LastUsedAt == nil || k.LastUsedAt.After(*v.LastUsedAt)) {
			v.LastUsedAt = kv.LastUsedAt
		}
		v.Keys = append(v.Keys, kv)
	}
	return v
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// clientInput are the settings of a client; fields left out keep their
// value (PATCH) or default (POST). "expires_at": null removes the expiry.
type clientInput struct {
	Name       *string         `json:"name"`
	Owner      *string         `json:"owner"`
	Scopes     *[]string       `json:"scopes"`
	DailyQuota *int64          `json:"daily_quota"`
	RateRPS    *float64        `json:"rate_limit_rps"`
	RateBurst  *int            `json:"rate_limit_burst"`
	Countries  *[]string       `json:"countries"`
	IPAllow    *[]string       `json:"ip_allow"`
	ExpiresAt  json.RawMessage `json:"expires_at"`
}

// apply writes the given fields into c and returns their JSON names.
func (in clientInput) apply(c *store.Client) ([]string, error) {
	var fields []string
	set := func(name string, given bool, do func()) {
		if given {
			do()
			fields = append(fields, name)
		}
	}
	set("name", in.Name != nil, func() { c.Name = *in.Name })
	set("owner", in.Owner != nil, func() { c.Owner = *in.Owner })
	set("scopes", in.Scopes != nil, func() { c.Scopes = *in.Scopes })
	set("daily_quota", in.DailyQuota != nil, func() { c.DailyQuota = *in.DailyQuota })
	set("rate_limit_rps", in.RateRPS != nil, func() { c.RateRPS = *in.RateRPS })
	set("rate_limit_burst", in.RateBurst != nil, func() { c.RateBurst = *in.RateBurst })
	set("countries", in.Countries != nil, func() { c.Countries = *in.Countries })
	set("ip_allow", in.IPAllow != nil, func() { c.IPAllow = *in.IPAllow })
	if len(in.ExpiresAt) > 0 {
		if bytes.Equal(in.ExpiresAt, []byte("null")) {
			c.ExpiresAt = nil
		} else {
			var t time.Time
			if err := json.Unmarshal(in.ExpiresAt, &t); err != nil {
				return nil, fmt.Errorf("%w: expires_at must be a time like 2027-01-31T00:00:00Z, or null", apikeys.ErrInvalidClient)
			}
			c.ExpiresAt = &t
		}
		fields = append(fields, "expires_at")
	}
	return fields, nil
}

// clientDetail is the audit detail of a client's settings (no key).
func clientDetail(c store.Client, fields []string) json.RawMessage {
	return jsonDetail(map[string]any{"fields": fields, "name": c.Name, "owner": c.Owner, "scopes": c.Scopes,
		"daily_quota": c.DailyQuota, "rate_limit_rps": c.RateRPS, "rate_limit_burst": c.RateBurst,
		"countries": c.Countries, "ip_allow": c.IPAllow, "expires_at": c.ExpiresAt})
}

func clientTarget(c store.Client) string { return apikeys.ClientUserID(c.ID) + " " + c.Name }

func (h *adminHandlers) listClients(w http.ResponseWriter, r *http.Request) {
	list, err := h.d.Clients.Clients(r.Context())
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	now := time.Now()
	out := make([]clientView, len(list))
	for i, c := range list {
		out[i] = viewClient(c, now)
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": out, "scopes": apikeys.Scopes})
}

func (h *adminHandlers) getClient(w http.ResponseWriter, r *http.Request) {
	c, ok := h.loadClient(w, r)
	if ok {
		writeJSON(w, http.StatusOK, viewClient(c, time.Now()))
	}
}

// createClient makes a client and its first key. The key is in this
// answer only.
func (h *adminHandlers) createClient(w http.ResponseWriter, r *http.Request) {
	var in clientInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	c := store.Client{Scopes: slices.Clone(apikeys.Scopes), DailyQuota: apikeys.DefaultDailyQuota,
		RateRPS: apikeys.DefaultRateRPS, RateBurst: apikeys.DefaultRateBurst}
	fields, err := in.apply(&c)
	if err == nil {
		err = apikeys.Normalize(&c, h.d.Runtime().Countries(), time.Now(), true)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	key, stored := apikeys.Generate()
	c, err = h.d.Clients.CreateClient(r.Context(), c, stored, actor(r))
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	h.clientsChanged()
	h.audit(r, store.AuditEntry{Action: "clients.create", Target: clientTarget(c), Detail: clientDetail(c, fields)})
	writeJSON(w, http.StatusCreated, map[string]any{"client": viewClient(c, time.Now()), "key": key,
		"note": "Copy the key now: it is shown only once. Send it as \"Authorization: Bearer <key>\"."})
}

// changeClient changes the settings of a client (not its keys).
func (h *adminHandlers) changeClient(w http.ResponseWriter, r *http.Request) {
	var in clientInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	c, ok := h.loadClient(w, r)
	if !ok {
		return
	}
	oldExpiry := c.ExpiresAt
	fields, err := in.apply(&c)
	if err == nil && len(fields) == 0 {
		err = fmt.Errorf("%w: name at least one setting to change", apikeys.ErrInvalidClient)
	}
	if err == nil {
		// A past expiry that stays as it is does not block other changes.
		expiryChanged := slices.Contains(fields, "expires_at") && !sameTime(oldExpiry, c.ExpiresAt)
		err = apikeys.Normalize(&c, h.d.Runtime().Countries(), time.Now(), expiryChanged)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	c, err = h.d.Clients.UpdateClient(r.Context(), c, actor(r))
	if h.clientError(w, r, err) {
		return
	}
	h.clientsChanged()
	h.audit(r, store.AuditEntry{Action: "clients.change", Target: clientTarget(c), Detail: clientDetail(c, fields)})
	writeJSON(w, http.StatusOK, viewClient(c, time.Now()))
}

func sameTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}

// rotateClientKey gives the client a new key (in this answer only); the
// old keys keep working for the grace period: {"grace": "24h"}.
func (h *adminHandlers) rotateClientKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Grace string `json:"grace"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
			return
		}
	}
	grace := DefaultRotateGrace
	if body.Grace != "" {
		d, err := time.ParseDuration(body.Grace)
		if err != nil || d < 0 || d > MaxRotateGrace {
			writeError(w, http.StatusBadRequest, CodeInvalidRequest,
				"grace must be a duration from 0s (old keys stop at once) to "+MaxRotateGrace.String()+", e.g. 24h")
			return
		}
		grace = d
	}
	c, ok := h.loadClient(w, r)
	if !ok {
		return
	}
	key, stored := apikeys.Generate()
	until := time.Now().Add(grace)
	k, err := h.d.Clients.RotateKey(r.Context(), c.ID, stored, until, actor(r))
	if h.clientError(w, r, err) {
		return
	}
	h.clientsChanged()
	h.audit(r, store.AuditEntry{Action: "clients.rotate", Target: clientTarget(c),
		Detail: jsonDetail(map[string]any{"new_key_prefix": k.Prefix, "grace": grace.String(), "old_keys_until": until.UTC()})})
	if c, err = h.d.Clients.Client(r.Context(), c.ID); err != nil {
		h.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"client": viewClient(c, time.Now()), "key": key,
		"old_keys_until": until.UTC(),
		"note":           "Copy the key now: it is shown only once. The old keys stop working at old_keys_until."})
}

// revokeClient stops a client and all its keys at once (final).
func (h *adminHandlers) revokeClient(w http.ResponseWriter, r *http.Request) {
	c, ok := h.loadClient(w, r)
	if !ok {
		return
	}
	if h.clientError(w, r, h.d.Clients.RevokeClient(r.Context(), c.ID, actor(r))) {
		return
	}
	h.clientsChanged()
	h.audit(r, store.AuditEntry{Action: "clients.revoke", Target: clientTarget(c)})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// revokeClientKey stops one key at once, e.g. an old key before its grace
// period ends.
func (h *adminHandlers) revokeClientKey(w http.ResponseWriter, r *http.Request) {
	c, ok := h.loadClient(w, r)
	if !ok {
		return
	}
	keyID, err := strconv.ParseInt(r.PathValue("key"), 10, 64)
	i := slices.IndexFunc(c.Keys, func(k store.APIKey) bool { return k.ID == keyID })
	if err != nil || i < 0 {
		writeError(w, http.StatusNotFound, CodeNotFound, "this client has no key with this id")
		return
	}
	if h.clientError(w, r, h.d.Clients.RevokeKey(r.Context(), c.ID, keyID)) {
		return
	}
	h.clientsChanged()
	h.audit(r, store.AuditEntry{Action: "clients.key_revoke", Target: clientTarget(c),
		Detail: jsonDetail(map[string]any{"key": c.Keys[i].Prefix, "key_id": keyID})})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// loadClient reads the client of the {id} path value, or answers why not.
func (h *adminHandlers) loadClient(w http.ResponseWriter, r *http.Request) (store.Client, bool) {
	id, err := strconv.ParseInt(strings.TrimPrefix(r.PathValue("id"), "client:"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, CodeNotFound, "no API client with this id")
		return store.Client{}, false
	}
	c, err := h.d.Clients.Client(r.Context(), id)
	return c, !h.clientError(w, r, err)
}

// clientError answers a client store error and reports whether it did.
func (h *adminHandlers) clientError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrClientNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, store.ErrKeyNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, store.ErrClientRevoked):
		writeError(w, http.StatusConflict, CodeConflict, err.Error()+"; create a new client instead")
	default:
		h.storeError(w, r, err)
	}
	return true
}

// clientsChanged makes this server see client changes at once (others
// within apikeys.CacheTTL).
func (h *adminHandlers) clientsChanged() {
	if h.d.ClientsChanged != nil {
		h.d.ClientsChanged()
	}
}
