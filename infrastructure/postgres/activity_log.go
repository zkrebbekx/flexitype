package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// activityLog persists audit entries. Writes run on the caller-provided
// transaction (the unit of work's pre-commit handler); reads run on the
// pool.
type activityLog struct {
	pool db.QueryExecer
}

// NewActivityLog builds the activity-log adapter.
func NewActivityLog(pool db.QueryExecer) activity.Log {
	return &activityLog{pool: pool}
}

func (l *activityLog) Write(ctx context.Context, tx db.QueryExecer, entries []activity.Entry) error {
	if len(entries) == 0 {
		return nil
	}

	const cols = 9
	args := make([]any, 0, len(entries)*cols)
	rows := make([]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, "(?, ?, ?, ?, ?, ?, ?, ?, ?)")
		args = append(args,
			e.ID.String(), e.TenantID.String(), e.Actor, e.Entity, e.EntityID,
			string(e.Action), jsonbParam(e.Before), jsonbParam(e.After), e.OccurredAt,
		)
	}

	query := bind(`INSERT INTO flexitype_activity_log
	   (id, tenant_id, actor, entity, entity_id, action, before_state, after_state, occurred_at)
	 VALUES ` + strings.Join(rows, ", "))
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("write activity log: %w", err)
	}
	return nil
}

// activityKeyset orders newest-first with the id as a unique tiebreaker.
var activityKeyset = []db.KeysetColumn{{Expr: "occurred_at", Desc: true, Cast: "::timestamptz"}, {Expr: "id", Desc: true}}

func (l *activityLog) List(ctx context.Context, filter activity.Filter, page db.Page) ([]activity.Entry, int, error) {
	where := []string{"tenant_id = ?"}
	args := []any{filter.TenantID.String()}
	if filter.Entity != "" {
		where = append(where, "entity = ?")
		args = append(args, filter.Entity)
	}
	if filter.EntityID != "" {
		where = append(where, "entity_id = ?")
		args = append(args, filter.EntityID)
	}
	if filter.Actor != "" {
		where = append(where, "actor = ?")
		args = append(args, filter.Actor)
	}
	filterClause := strings.Join(where, " AND ")
	filterArgs := append([]any(nil), args...)

	pageWhere, pageArgs := keysetWhere(where, args, activityKeyset, page.Cursor)
	pageArgs = append(pageArgs, page.FetchLimit())

	// NULL jsonb cannot scan into json.RawMessage; coalesce to empty text.
	query := bind(`SELECT id, tenant_id, actor, entity, entity_id, action,
	        COALESCE(before_state::text, '') AS before_state,
	        COALESCE(after_state::text, '')  AS after_state,
	        occurred_at
	 FROM flexitype_activity_log
	 WHERE ` + strings.Join(pageWhere, " AND ") + `
	 ORDER BY occurred_at DESC, id DESC
	 LIMIT ?`)

	var rows []activityRow
	if err := l.pool.SelectContext(ctx, &rows, query, pageArgs...); err != nil {
		return nil, 0, fmt.Errorf("list activity log: %w", err)
	}

	entries := make([]activity.Entry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, row.entry())
	}
	total := 0
	if page.WantTotal {
		if err := l.pool.GetContext(ctx, &total, bind(
			`SELECT count(*) FROM flexitype_activity_log WHERE `+filterClause), filterArgs...); err != nil {
			return nil, 0, fmt.Errorf("count activity log: %w", err)
		}
	}
	return entries, total, nil
}

// activityRow scans jsonb descriptors as text; database/sql cannot scan
// into json.RawMessage directly.
type activityRow struct {
	ID         ulid.ID               `db:"id"`
	TenantID   valueobjects.TenantID `db:"tenant_id"`
	Actor      string                `db:"actor"`
	Entity     string                `db:"entity"`
	EntityID   string                `db:"entity_id"`
	Action     string                `db:"action"`
	Before     string                `db:"before_state"`
	After      string                `db:"after_state"`
	OccurredAt time.Time             `db:"occurred_at"`
	TotalCount int                   `db:"total_count"`
}

func (r activityRow) entry() activity.Entry {
	e := activity.Entry{
		ID:         r.ID,
		TenantID:   r.TenantID,
		Actor:      r.Actor,
		Entity:     r.Entity,
		EntityID:   r.EntityID,
		Action:     activity.Action(r.Action),
		OccurredAt: r.OccurredAt,
	}
	if r.Before != "" {
		e.Before = json.RawMessage(r.Before)
	}
	if r.After != "" {
		e.After = json.RawMessage(r.After)
	}
	return e
}
