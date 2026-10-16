package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// UsageRow is one usage counter of one UTC day (see migration 0003).
type UsageRow struct {
	Day     time.Time
	Kind    string
	K1      string
	K2      string
	K3      string
	Count   int64
	CostUSD float64
	TotalMS int64
}

// QueryRow counts the searches of one normalized query on one UTC day.
type QueryRow struct {
	Day      time.Time
	Country  string
	Query    string
	Searches int64
	Live     int64
}

// AddUsage adds counters (incrementing existing rows) in one transaction,
// so several instances can add their counts to the same day.
func (db *DB) AddUsage(ctx context.Context, usage []UsageRow, queries []QueryRow) error {
	if len(usage) == 0 && len(queries) == 0 {
		return nil
	}
	return pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		b := &pgx.Batch{}
		for _, u := range usage {
			b.Queue(`INSERT INTO usage_daily (day, kind, k1, k2, k3, count, cost_usd, total_ms)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
				ON CONFLICT (day, kind, k1, k2, k3) DO UPDATE SET
					count = usage_daily.count + excluded.count,
					cost_usd = usage_daily.cost_usd + excluded.cost_usd,
					total_ms = usage_daily.total_ms + excluded.total_ms`,
				u.Day, u.Kind, u.K1, u.K2, u.K3, u.Count, u.CostUSD, u.TotalMS)
		}
		for _, q := range queries {
			b.Queue(`INSERT INTO query_daily (day, country, query, searches, live) VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (day, country, query) DO UPDATE SET
					searches = query_daily.searches + excluded.searches,
					live = query_daily.live + excluded.live`,
				q.Day, q.Country, q.Query, q.Searches, q.Live)
		}
		return tx.SendBatch(ctx, b).Close()
	})
}

// UsageRows returns the counters of the UTC days from..to (inclusive).
func (db *DB) UsageRows(ctx context.Context, from, to time.Time) ([]UsageRow, error) {
	rows, err := db.pool.Query(ctx, `SELECT day, kind, k1, k2, k3, count, cost_usd, total_ms
		FROM usage_daily WHERE day BETWEEN $1 AND $2 ORDER BY day, kind, k1, k2, k3`, from, to)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (UsageRow, error) {
		var u UsageRow
		err := r.Scan(&u.Day, &u.Kind, &u.K1, &u.K2, &u.K3, &u.Count, &u.CostUSD, &u.TotalMS)
		return u, err
	})
}

// TopQueries returns the most searched queries of the UTC days from..to,
// most searched first (Day is zero: summed over the days).
func (db *DB) TopQueries(ctx context.Context, from, to time.Time, limit int) ([]QueryRow, error) {
	rows, err := db.pool.Query(ctx, `SELECT country, query, sum(searches)::bigint, sum(live)::bigint
		FROM query_daily WHERE day BETWEEN $1 AND $2
		GROUP BY country, query ORDER BY 3 DESC, 4 DESC, 2 LIMIT $3`, from, to, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (QueryRow, error) {
		var q QueryRow
		err := r.Scan(&q.Country, &q.Query, &q.Searches, &q.Live)
		return q, err
	})
}

// UsageCost returns the estimated cost of the UTC days from..to.
func (db *DB) UsageCost(ctx context.Context, from, to time.Time) (float64, error) {
	var cost float64
	err := db.pool.QueryRow(ctx, `SELECT coalesce(sum(cost_usd), 0) FROM usage_daily
		WHERE kind = 'source' AND day BETWEEN $1 AND $2`, from, to).Scan(&cost)
	return cost, err
}

// PruneUsage deletes counters before usageBefore and query counts before
// queriesBefore (UTC days).
func (db *DB) PruneUsage(ctx context.Context, usageBefore, queriesBefore time.Time) error {
	if _, err := db.pool.Exec(ctx, "DELETE FROM usage_daily WHERE day < $1", usageBefore); err != nil {
		return err
	}
	_, err := db.pool.Exec(ctx, "DELETE FROM query_daily WHERE day < $1", queriesBefore)
	return err
}

// HasAudit reports whether an audit entry with action and target exists
// (e.g. a budget alert already sent this month).
func (db *DB) HasAudit(ctx context.Context, action, target string) (bool, error) {
	var ok bool
	err := db.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM audit_log WHERE action = $1 AND target = $2)", action, target).Scan(&ok)
	return ok, err
}
