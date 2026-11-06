package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Audit outcomes.
const (
	OutcomeOK     = "ok"
	OutcomeDenied = "denied"
	OutcomeFailed = "failed"
)

// AuditEntry is one admin action. Detail never holds secret values.
type AuditEntry struct {
	ID        int64           `json:"id"`
	At        time.Time       `json:"at"`
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Target    string          `json:"target,omitempty"`
	Detail    json.RawMessage `json:"detail,omitempty"`
	Outcome   string          `json:"outcome"`
	RequestID string          `json:"request_id,omitempty"`
	RemoteIP  string          `json:"remote_ip,omitempty"`
}

// AuditFilter selects audit entries, newest first.
type AuditFilter struct {
	Actor    string // exact; empty = all
	Action   string // prefix, e.g. "settings."; empty = all
	BeforeID int64  // entries older than this id (paging); 0 = newest
	Limit    int    // 1-500, default 100
}

// AddAudit writes one audit entry.
func (db *DB) AddAudit(ctx context.Context, e AuditEntry) error {
	if e.Actor == "" || e.Action == "" {
		return errors.New("store: audit entry needs actor and action")
	}
	if e.Outcome == "" {
		e.Outcome = OutcomeOK
	}
	detail := e.Detail
	if len(detail) == 0 {
		detail = json.RawMessage("{}")
	}
	_, err := db.pool.Exec(ctx, `INSERT INTO audit_log (actor, action, target, detail, outcome, request_id, remote_ip)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.Actor, e.Action, e.Target, detail, e.Outcome, e.RequestID, e.RemoteIP)
	return err
}

// AuditLog returns audit entries, newest first.
func (db *DB) AuditLog(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := db.pool.Query(ctx, `SELECT id, at, actor, action, target, detail, outcome, request_id, remote_ip
		FROM audit_log
		WHERE ($1 = '' OR actor = $1) AND starts_with(action, $2) AND ($3 = 0 OR id < $3)
		ORDER BY id DESC LIMIT $4`, f.Actor, f.Action, f.BeforeID, f.Limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AuditEntry, error) {
		var (
			e      AuditEntry
			detail []byte
		)
		err := r.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.Target, &detail, &e.Outcome, &e.RequestID, &e.RemoteIP)
		e.Detail = detail
		return e, err
	})
}
