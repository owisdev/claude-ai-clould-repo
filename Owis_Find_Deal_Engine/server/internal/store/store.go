// Package store keeps the service's own data in PostgreSQL: runtime
// settings with their change history, and the admin list.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLock serializes migrations when several instances start at once
// (pg_advisory_lock key).
const migrationLock = 7_340_001

// DB is a connection pool. Safe for concurrent use.
type DB struct {
	pool *pgxpool.Pool
}

// Open connects to url (postgres://...) and checks the connection.
func Open(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		// The error may quote the URL, password included.
		return nil, errors.New("DATABASE_URL: invalid connection string")
	}
	cfg.MaxConns = 8
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: %w", err)
	}
	return &DB{pool: pool}, nil
}

// Close closes the pool.
func (db *DB) Close() { db.pool.Close() }

// Migrate applies the embedded migrations that have not run yet, in file
// name order, each in its own transaction. It returns how many it applied.
func (db *DB) Migrate(ctx context.Context) (int, error) {
	conn, err := db.pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLock); err != nil {
		return 0, err
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLock)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    integer PRIMARY KEY,
		name       text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return 0, err
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return 0, err
	}
	sort.Strings(files)
	applied := 0
	for _, f := range files {
		name := strings.TrimPrefix(f, "migrations/")
		num, _, _ := strings.Cut(name, "_")
		version, err := strconv.Atoi(num)
		if err != nil {
			return applied, fmt.Errorf("migration %s: name must start with a number", name)
		}
		var done bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version).Scan(&done); err != nil {
			return applied, err
		}
		if done {
			continue
		}
		sql, err := migrations.ReadFile(f)
		if err != nil {
			return applied, err
		}
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version, name) VALUES ($1, $2)", version, name)
			return err
		})
		if err != nil {
			return applied, fmt.Errorf("migration %s: %w", name, err)
		}
		applied++
	}
	return applied, nil
}
