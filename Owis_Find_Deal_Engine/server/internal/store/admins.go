package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Errors of the admin list.
var (
	ErrNotAdmin = errors.New("not an admin")
	// ErrAdminLinked: the email is an admin, but linked to another login
	// (the account was deleted and created again): unlink it first.
	ErrAdminLinked   = errors.New("admin email is linked to another login")
	ErrAdminExists   = errors.New("admin already on the list")
	ErrLastAdmin     = errors.New("the last active admin cannot be disabled")
	ErrAdminNotFound = errors.New("no admin with this email")
)

// Admin is one entry of the admin list.
type Admin struct {
	ID          int64
	Email       string
	FirebaseUID *string // linked at the admin's first login
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

// AdminForLogin returns the admin that a signed-in user is. The user's
// login id (uid) is linked to the admin entry with the same email at the
// first login, and only when the email is verified; after that only the
// uid counts, so changing the email of the login does not change who is
// an admin. Disabled admins are returned too (the caller refuses them).
func (db *DB) AdminForLogin(ctx context.Context, uid, email string, emailVerified bool) (Admin, error) {
	var a Admin
	scan := func(row pgx.Row) error {
		return row.Scan(&a.ID, &a.Email, &a.FirebaseUID, &a.CreatedAt, &a.CreatedBy, &a.DisabledAt)
	}
	const cols = "id, email, firebase_uid, created_at, created_by, disabled_at"
	err := scan(db.pool.QueryRow(ctx, "SELECT "+cols+" FROM admins WHERE firebase_uid = $1", uid))
	if err == nil || !errors.Is(err, pgx.ErrNoRows) {
		return a, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if !emailVerified || email == "" {
		return Admin{}, ErrNotAdmin
	}
	err = scan(db.pool.QueryRow(ctx, "SELECT "+cols+" FROM admins WHERE email = $1", email))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Admin{}, ErrNotAdmin
	case err != nil:
		return Admin{}, err
	case a.FirebaseUID != nil:
		return Admin{}, ErrAdminLinked
	}
	tag, err := db.pool.Exec(ctx, "UPDATE admins SET firebase_uid = $1 WHERE id = $2 AND firebase_uid IS NULL", uid, a.ID)
	if err != nil {
		return Admin{}, err
	}
	if tag.RowsAffected() == 0 {
		return Admin{}, ErrAdminLinked // linked by a concurrent login
	}
	a.FirebaseUID = &uid
	return a, nil
}

// AddAdmin puts email on the admin list.
func (db *DB) AddAdmin(ctx context.Context, email, by string) (Admin, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var a Admin
	err := db.pool.QueryRow(ctx, `INSERT INTO admins (email, created_by) VALUES ($1, $2)
		RETURNING id, email, firebase_uid, created_at, created_by, disabled_at`, email, by).
		Scan(&a.ID, &a.Email, &a.FirebaseUID, &a.CreatedAt, &a.CreatedBy, &a.DisabledAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return Admin{}, ErrAdminExists
	}
	return a, err
}

// SetAdminDisabled disables or enables the admin with email. The last
// active admin cannot be disabled (nobody could sign in to the panel).
func (db *DB) SetAdminDisabled(ctx context.Context, email string, disabled bool) error {
	email = strings.ToLower(strings.TrimSpace(email))
	return pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "LOCK TABLE admins IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return err
		}
		if disabled {
			var others bool
			if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM admins WHERE disabled_at IS NULL AND email <> $1)", email).
				Scan(&others); err != nil {
				return err
			}
			if !others {
				return ErrLastAdmin
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE admins SET disabled_at = CASE WHEN $2 THEN coalesce(disabled_at, now()) END
			WHERE email = $1`, email, disabled)
		if err == nil && tag.RowsAffected() == 0 {
			err = ErrAdminNotFound
		}
		return err
	})
}

// UnlinkAdmin forgets the login linked to the admin with email; the next
// sign-in with a verified email links it again.
func (db *DB) UnlinkAdmin(ctx context.Context, email string) error {
	tag, err := db.pool.Exec(ctx, "UPDATE admins SET firebase_uid = NULL WHERE email = $1",
		strings.ToLower(strings.TrimSpace(email)))
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrAdminNotFound
	}
	return err
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
