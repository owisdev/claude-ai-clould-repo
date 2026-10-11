package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Admin is one entry of the admin list.
type Admin struct {
	ID          int64
	Email       string
	FirebaseUID *string // linked at the admin's first login (step 8)
	CreatedAt   time.Time
	CreatedBy   string
	DisabledAt  *time.Time
}

// EnsureFirstAdmin adds email as an admin if the admin list is empty. It
// reports whether it added one. Later changes go through the admin panel,
// so removing every admin does not bring this one back.
func (db *DB) EnsureFirstAdmin(ctx context.Context, email string) (bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var added bool
	err := pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		// Two instances starting together must not both add one.
		if _, err := tx.Exec(ctx, "LOCK TABLE admins IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM admins)").Scan(&exists); err != nil || exists {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO admins (email, created_by) VALUES ($1, 'bootstrap:ADMIN_EMAIL')", email); err != nil {
			return err
		}
		added = true
		return nil
	})
	return added, err
}

// Admins returns the admin list, oldest first.
func (db *DB) Admins(ctx context.Context) ([]Admin, error) {
	rows, err := db.pool.Query(ctx, `SELECT id, email, firebase_uid, created_at, created_by, disabled_at
		FROM admins ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Admin, error) {
		var a Admin
		err := r.Scan(&a.ID, &a.Email, &a.FirebaseUID, &a.CreatedAt, &a.CreatedBy, &a.DisabledAt)
		return a, err
	})
}
