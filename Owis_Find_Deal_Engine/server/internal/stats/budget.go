package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"owis_find_deal_engine/internal/store"
)

// BudgetStore is what the budget check needs (store.DB).
type BudgetStore interface {
	UsageCost(ctx context.Context, from, to time.Time) (float64, error)
	HasAudit(ctx context.Context, action, target string) (bool, error)
	AddAudit(ctx context.Context, e store.AuditEntry) error
}

// ActionBudgetAlert is the audit action of a budget alert.
const ActionBudgetAlert = "budget.alert"

// CheckBudget compares this month's estimated cost with the monthly
// budget (limit 0 = none). When the alert level or the whole budget is
// reached for the first time this month, it logs an error and writes an
// audit entry (once per level and month, also with several instances
// mostly; a rare duplicate is harmless).
func CheckBudget(ctx context.Context, st BudgetStore, now time.Time, limit float64, alertPercent int, log *slog.Logger) (Budget, error) {
	month := MonthStart(now)
	spent, err := st.UsageCost(ctx, month, now.UTC())
	if err != nil {
		return Budget{}, err
	}
	b := NewBudget(month, spent, limit, alertPercent)
	if b.Status != BudgetWarning && b.Status != BudgetExceeded {
		return b, nil
	}
	levels := []int{alertPercent}
	if b.Status == BudgetExceeded && alertPercent < 100 {
		levels = append(levels, 100)
	}
	for _, level := range levels {
		target := fmt.Sprintf("%s:%d%%", b.Month, level)
		done, err := st.HasAudit(ctx, ActionBudgetAlert, target)
		if err != nil || done {
			if err != nil {
				return b, err
			}
			continue
		}
		detail, _ := json.Marshal(map[string]any{"level_percent": level, "spent_usd": b.SpentUSD, "limit_usd": b.LimitUSD})
		if err := st.AddAudit(ctx, store.AuditEntry{Actor: "system", Action: ActionBudgetAlert, Target: target, Detail: detail}); err != nil {
			return b, err
		}
		msg := "monthly budget alert: estimated cost reached the alert level"
		if level >= 100 {
			msg = "monthly budget used up: estimated cost reached the monthly budget"
		}
		log.Error(msg,
			"month", b.Month, "level_percent", level, "spent_usd", b.SpentUSD, "limit_usd", b.LimitUSD)
	}
	return b, nil
}
