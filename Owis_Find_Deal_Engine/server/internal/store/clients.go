package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Errors of the API clients.
var (
	ErrClientNotFound = errors.New("no API client with this id")
	ErrClientRevoked  = errors.New("the API client is revoked")
	ErrKeyNotFound    = errors.New("no such API key")
)

// Client is an application or AI agent allowed to call the public API
// with its own keys.
type Client struct {
	ID         int64
	Name       string
	Owner      string
	Scopes     []string
	DailyQuota int64 // live searches per UTC day
	RateRPS    float64
	RateBurst  int
	Countries  []string // allowed countries; empty = all
	IPAllow    []string // CIDRs; empty = any address
	ExpiresAt  *time.Time
	CreatedAt  time.Time
	CreatedBy  string
	UpdatedAt  time.Time
	UpdatedBy  string
	RevokedAt  *time.Time
	RevokedBy  string

	Keys []APIKey // filled by Clients and Client, newest first
}

// APIKey is one key of a client. The key itself is never stored.
type APIKey struct {
	ID         int64
	ClientID   int64
	Prefix     string
	CreatedAt  time.Time
	CreatedBy  string
	ExpiresAt  *time.Time // end of the grace period after a rotation
	RevokedAt  *time.Time
	LastUsedAt *time.Time
}

// NewKey is a key to store: its prefix and SHA-256 hash.
type NewKey struct {
	Prefix string
	Hash   []byte
}

const clientCols = `c.id, c.name, c.owner, c.scopes, c.daily_quota, c.rate_rps, c.rate_burst, c.countries, c.ip_allow,
	c.expires_at, c.created_at, c.created_by, c.updated_at, c.updated_by, c.revoked_at, c.revoked_by`

const keyCols = "k.id, k.client_id, k.prefix, k.created_at, k.created_by, k.expires_at, k.revoked_at, k.last_used_at"

func scanClient(row pgx.Row, extra ...any) (Client, error) {
	var c Client
	err := row.Scan(append([]any{&c.ID, &c.Name, &c.Owner, &c.Scopes, &c.DailyQuota, &c.RateRPS, &c.RateBurst,
		&c.Countries, &c.IPAllow, &c.ExpiresAt, &c.CreatedAt, &c.CreatedBy, &c.UpdatedAt, &c.UpdatedBy,
		&c.RevokedAt, &c.RevokedBy}, extra...)...)
	return c, err
}

func keyDest(k *APIKey) []any {
	return []any{&k.ID, &k.ClientID, &k.Prefix, &k.CreatedAt, &k.CreatedBy, &k.ExpiresAt, &k.RevokedAt, &k.LastUsedAt}
}

// CreateClient stores a new client with its first key.
func (db *DB) CreateClient(ctx context.Context, c Client, key NewKey, by string) (Client, error) {
	var out Client
	err := pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		var err error
		out, err = scanClient(tx.QueryRow(ctx, `INSERT INTO api_clients AS c
			(name, owner, scopes, daily_quota, rate_rps, rate_burst, countries, ip_allow, expires_at, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
			RETURNING `+clientCols,
			c.Name, c.Owner, nonNil(c.Scopes), c.DailyQuota, c.RateRPS, c.RateBurst, nonNil(c.Countries), nonNil(c.IPAllow),
			c.ExpiresAt, by))
		if err != nil {
			return err
		}
		k, err := insertKey(ctx, tx, out.ID, key, by)
		out.Keys = []APIKey{k}
		return err
	})
	return out, err
}

func insertKey(ctx context.Context, tx pgx.Tx, clientID int64, key NewKey, by string) (APIKey, error) {
	var k APIKey
	err := tx.QueryRow(ctx, `INSERT INTO api_keys AS k (client_id, prefix, hash, created_by)
		VALUES ($1, $2, $3, $4) RETURNING `+keyCols, clientID, key.Prefix, key.Hash, by).Scan(keyDest(&k)...)
	return k, err
}

// Clients returns every client with its keys, newest first.
func (db *DB) Clients(ctx context.Context) ([]Client, error) {
	rows, err := db.pool.Query(ctx, "SELECT "+clientCols+" FROM api_clients c ORDER BY c.id DESC")
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Client, error) { return scanClient(r) })
	if err != nil {
		return nil, err
	}
	keys, err := db.keys(ctx, 0)
	if err != nil {
		return nil, err
	}
	byClient := map[int64][]APIKey{}
	for _, k := range keys {
		byClient[k.ClientID] = append(byClient[k.ClientID], k)
	}
	for i := range list {
		list[i].Keys = byClient[list[i].ID]
	}
	return list, nil
}

// Client returns one client with its keys.
func (db *DB) Client(ctx context.Context, id int64) (Client, error) {
	c, err := scanClient(db.pool.QueryRow(ctx, "SELECT "+clientCols+" FROM api_clients c WHERE c.id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, ErrClientNotFound
	}
	if err != nil {
		return Client{}, err
	}
	c.Keys, err = db.keys(ctx, id)
	return c, err
}

// keys returns the keys of one client (0 = all), newest first.
func (db *DB) keys(ctx context.Context, clientID int64) ([]APIKey, error) {
	rows, err := db.pool.Query(ctx, "SELECT "+keyCols+" FROM api_keys k WHERE $1 = 0 OR k.client_id = $1 ORDER BY k.id DESC", clientID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (APIKey, error) {
		var k APIKey
		err := r.Scan(keyDest(&k)...)
		return k, err
	})
}

// UpdateClient changes the settings of a client that is not revoked
// (everything but its id, keys and history fields).
func (db *DB) UpdateClient(ctx context.Context, c Client, by string) (Client, error) {
	out, err := scanClient(db.pool.QueryRow(ctx, `UPDATE api_clients AS c SET
		name = $2, owner = $3, scopes = $4, daily_quota = $5, rate_rps = $6, rate_burst = $7, countries = $8,
		ip_allow = $9, expires_at = $10, updated_at = now(), updated_by = $11
		WHERE c.id = $1 AND c.revoked_at IS NULL RETURNING `+clientCols,
		c.ID, c.Name, c.Owner, nonNil(c.Scopes), c.DailyQuota, c.RateRPS, c.RateBurst, nonNil(c.Countries),
		nonNil(c.IPAllow), c.ExpiresAt, by))
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, db.clientState(ctx, c.ID)
	}
	if err != nil {
		return Client{}, err
	}
	out.Keys, err = db.keys(ctx, c.ID)
	return out, err
}

// clientState explains why a change found no client to change.
func (db *DB) clientState(ctx context.Context, id int64) error {
	var revoked bool
	err := db.pool.QueryRow(ctx, "SELECT revoked_at IS NOT NULL FROM api_clients WHERE id = $1", id).Scan(&revoked)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrClientNotFound
	case err != nil:
		return err
	case revoked:
		return ErrClientRevoked
	}
	return errors.New("store: client changed concurrently")
}

// RotateKey adds a new key to the client. Its other usable keys stay
// valid until graceUntil (or their own earlier end), then stop working.
func (db *DB) RotateKey(ctx context.Context, clientID int64, key NewKey, graceUntil time.Time, by string) (APIKey, error) {
	var k APIKey
	err := pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		var revoked bool
		err := tx.QueryRow(ctx, "SELECT revoked_at IS NOT NULL FROM api_clients WHERE id = $1 FOR UPDATE", clientID).Scan(&revoked)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrClientNotFound
		case err != nil:
			return err
		case revoked:
			return ErrClientRevoked
		}
		if _, err := tx.Exec(ctx, `UPDATE api_keys SET expires_at = LEAST(coalesce(expires_at, $2), $2)
			WHERE client_id = $1 AND revoked_at IS NULL`, clientID, graceUntil); err != nil {
			return err
		}
		k, err = insertKey(ctx, tx, clientID, key, by)
		return err
	})
	return k, err
}

// RevokeClient stops the client and all its keys at once. Revoking is
// final: create a new client to give access again.
func (db *DB) RevokeClient(ctx context.Context, id int64, by string) error {
	tag, err := db.pool.Exec(ctx, `UPDATE api_clients SET revoked_at = now(), revoked_by = $2, updated_at = now(), updated_by = $2
		WHERE id = $1 AND revoked_at IS NULL`, id, by)
	if err == nil && tag.RowsAffected() == 0 {
		err = db.clientState(ctx, id)
	}
	return err
}

// RevokeKey stops one key of a client at once (e.g. the old key before
// its grace period ends).
func (db *DB) RevokeKey(ctx context.Context, clientID, keyID int64) error {
	tag, err := db.pool.Exec(ctx, "UPDATE api_keys SET revoked_at = coalesce(revoked_at, now()) WHERE id = $1 AND client_id = $2",
		keyID, clientID)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrKeyNotFound
	}
	return err
}

// ClientByKeyHash returns the key with this SHA-256 hash and its client,
// whatever their state (the caller checks revocation and expiry).
func (db *DB) ClientByKeyHash(ctx context.Context, hash []byte) (Client, APIKey, error) {
	var k APIKey
	c, err := scanClient(db.pool.QueryRow(ctx, "SELECT "+clientCols+", "+keyCols+`
		FROM api_keys k JOIN api_clients c ON c.id = k.client_id WHERE k.hash = $1`, hash), keyDest(&k)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, APIKey{}, ErrKeyNotFound
	}
	return c, k, err
}

// TouchKeys records when keys were last used (key id → time); older
// times never overwrite newer ones.
func (db *DB) TouchKeys(ctx context.Context, used map[int64]time.Time) error {
	if len(used) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(used))
	ats := make([]time.Time, 0, len(used))
	for id, at := range used {
		ids = append(ids, id)
		ats = append(ats, at)
	}
	_, err := db.pool.Exec(ctx, `UPDATE api_keys k SET last_used_at = u.at
		FROM unnest($1::bigint[], $2::timestamptz[]) AS u(id, at)
		WHERE k.id = u.id AND (k.last_used_at IS NULL OR k.last_used_at < u.at)`, ids, ats)
	return err
}

// nonNil stores an empty list as '{}' rather than NULL.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
