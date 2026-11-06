package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// settingsLock serializes settings writes (pg_advisory_xact_lock key), so
// every change gets the right old value in the history.
const settingsLock = 7_340_002

// Setting is the current value of a runtime setting. Value is encrypted
// when Secret is true.
type Setting struct {
	Key       string
	Value     string
	Secret    bool
	UpdatedAt time.Time
	UpdatedBy string
}

// Update sets (Value non-nil) or removes (Value nil) one setting.
type Update struct {
	Key    string
	Value  *string
	Secret bool
}

// Change is one row of the settings history. Old and New are nil when the
// setting was not set before / was removed, and always nil for secrets.
type Change struct {
	ID        int64
	Key       string
	Old, New  *string
	Secret    bool
	ChangedBy string
	ChangedAt time.Time
}

// Settings returns every stored setting and the settings version: the id
// of the latest history row (0 = never changed). Both come from the same
// snapshot of the database.
func (db *DB) Settings(ctx context.Context) ([]Setting, int64, error) {
	var (
		out     []Setting
		version int64
	)
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, "SELECT key, value, secret, updated_at, updated_by FROM settings ORDER BY key")
			if err != nil {
				return err
			}
			out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Setting, error) {
				var s Setting
				err := r.Scan(&s.Key, &s.Value, &s.Secret, &s.UpdatedAt, &s.UpdatedBy)
				return s, err
			})
			if err != nil {
				return err
			}
			return tx.QueryRow(ctx, "SELECT coalesce(max(id), 0) FROM settings_history").Scan(&version)
		})
	return out, version, err
}

// SettingsVersion returns the id of the latest history row (0 = never
// changed). It grows with every change, so a poller can compare it.
func (db *DB) SettingsVersion(ctx context.Context) (int64, error) {
	var v int64
	err := db.pool.QueryRow(ctx, "SELECT coalesce(max(id), 0) FROM settings_history").Scan(&v)
	return v, err
}

// SetSettings applies updates in one transaction and records each in the
// history as done by "by". Removing a setting that is not set is a no-op.
// The caller skips updates that would not change anything. It returns the
// new settings version.
func (db *DB) SetSettings(ctx context.Context, by string, updates []Update) (int64, error) {
	if by == "" {
		return 0, errors.New("store: SetSettings needs who made the change")
	}
	var version int64
	err := pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", settingsLock); err != nil {
			return err
		}
		for _, u := range updates {
			var (
				old       *string
				oldSecret bool
			)
			err := tx.QueryRow(ctx, "SELECT value, secret FROM settings WHERE key = $1", u.Key).Scan(&old, &oldSecret)
			if errors.Is(err, pgx.ErrNoRows) {
				old = nil
			} else if err != nil {
				return err
			}
			if old == nil && u.Value == nil {
				continue
			}
			if u.Value == nil {
				_, err = tx.Exec(ctx, "DELETE FROM settings WHERE key = $1", u.Key)
			} else {
				_, err = tx.Exec(ctx, `INSERT INTO settings (key, value, secret, updated_at, updated_by)
					VALUES ($1, $2, $3, now(), $4)
					ON CONFLICT (key) DO UPDATE SET value = excluded.value, secret = excluded.secret,
						updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
					u.Key, *u.Value, u.Secret, by)
			}
			if err != nil {
				return err
			}
			secret := u.Secret || oldSecret
			histOld, histNew := old, u.Value
			if secret {
				histOld, histNew = nil, nil
			}
			if _, err := tx.Exec(ctx, `INSERT INTO settings_history (key, old_value, new_value, secret, changed_by)
				VALUES ($1, $2, $3, $4, $5)`, u.Key, histOld, histNew, secret, by); err != nil {
				return err
			}
		}
		return tx.QueryRow(ctx, "SELECT coalesce(max(id), 0) FROM settings_history").Scan(&version)
	})
	return version, err
}

// SettingsHistory returns the latest changes, newest first: of one key, or
// of all keys when key is empty.
func (db *DB) SettingsHistory(ctx context.Context, key string, limit int) ([]Change, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := db.pool.Query(ctx, `SELECT id, key, old_value, new_value, secret, changed_by, changed_at
		FROM settings_history WHERE $1 = '' OR key = $1 ORDER BY id DESC LIMIT $2`, key, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Change, error) {
		var c Change
		err := r.Scan(&c.ID, &c.Key, &c.Old, &c.New, &c.Secret, &c.ChangedBy, &c.ChangedAt)
		return c, err
	})
}
