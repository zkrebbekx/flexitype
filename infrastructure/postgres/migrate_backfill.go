package postgres

import (
	"context"
	"fmt"

	"github.com/zkrebbekx/flexitype/pkg/db"
)

// backfillBatch is how many source rows one backfill statement processes.
// Small enough that each batch is a short transaction of its own, so a
// backfill over a table with 10^8 rows never holds a lock long enough to stall
// writes, and can be interrupted and resumed at any point.
const backfillBatch = 5000

// backfill is a data-moving migration step.
//
// Schema DDL belongs in a .sql migration; moving data does not. A backfill
// held inside the schema transaction keeps that transaction's locks for the
// whole scan, which is how migration 000019 came to hold SHARE ROW EXCLUSIVE
// on the value table through a whole-table aggregate and block every write in
// the fleet. Backfills therefore run after the schema is in place, in bounded
// batches, each committing on its own.
//
// A backfill must be idempotent and resumable: it is retried from the start of
// whatever it has not finished, on every boot, until it reports completion.
// Correctness must never depend on there being no concurrent writers — the
// schema change that precedes it is responsible for keeping new writes right
// while the backfill catches up.
type backfill struct {
	// name identifies the step in flexitype_schema_backfill. It never changes
	// once released; a new name means a new step.
	name string

	// requiresVersion is the migration version that must be applied before
	// this step is meaningful. A step whose version is not applied is skipped
	// rather than run against a schema that cannot serve it.
	requiresVersion int

	// step processes at most limit source rows and reports whether any work
	// remained. It runs outside any transaction the runner owns, so it must
	// open its own if it needs one.
	step func(ctx context.Context, q db.QueryExecer, limit int) (processed int, err error)
}

// backfills are the registered steps, run in order. Order matters only when
// one step depends on another's output; today they are independent.
var backfills = []backfill{entitySummaryBackfill}

// runBackfills executes every registered step that has not completed. A step
// runs to exhaustion — it is not spread across boots — but each of its batches
// is a separate short statement, so an interrupted run resumes from where it
// stopped rather than starting over.
func runBackfills(ctx context.Context, q db.QueryExecer) error {
	for _, b := range backfills {
		done, err := backfillCompleted(ctx, q, b.name)
		if err != nil {
			return err
		}
		if done {
			continue
		}
		applied, err := versionApplied(ctx, q, b.requiresVersion)
		if err != nil {
			return err
		}
		if !applied {
			continue
		}
		for {
			processed, err := b.step(ctx, q, backfillBatch)
			if err != nil {
				return fmt.Errorf("backfill %s: %w", b.name, err)
			}
			if processed == 0 {
				break
			}
			if err := ctx.Err(); err != nil {
				// Shutdown mid-backfill is not a failure: the completed
				// batches are durable and the next boot resumes.
				return err
			}
		}
		if _, err := q.ExecContext(ctx,
			`INSERT INTO flexitype_schema_backfill (name) VALUES ($1) ON CONFLICT (name) DO NOTHING`,
			b.name); err != nil {
			return fmt.Errorf("record backfill %s: %w", b.name, err)
		}
	}
	return nil
}

func backfillCompleted(ctx context.Context, q db.QueryExecer, name string) (bool, error) {
	var done bool
	if err := q.GetContext(ctx, &done,
		`SELECT EXISTS (SELECT 1 FROM flexitype_schema_backfill WHERE name = $1)`, name); err != nil {
		return false, fmt.Errorf("read backfill state %s: %w", name, err)
	}
	return done, nil
}

func versionApplied(ctx context.Context, q db.QueryExecer, version int) (bool, error) {
	var applied bool
	if err := q.GetContext(ctx, &applied,
		`SELECT EXISTS (SELECT 1 FROM flexitype_schema_migrations WHERE version = $1)`, version); err != nil {
		return false, fmt.Errorf("read migration state %06d: %w", version, err)
	}
	return applied, nil
}

// entitySummaryBackfill populates flexitype_entity_summary for entities that
// existed before migration 000019 created the projection.
//
// The trigger 000019 installs keeps every write after that point correct, so
// this only has to catch up history, and it may run while the fleet is
// serving. It walks live value rows in primary-key order, aggregates the batch
// per (tenant, type, entity), and upserts — taking the larger count and the
// later timestamp is wrong here, so it recomputes each touched key from the
// table rather than summing partial batches, which also makes a re-run
// harmless.
var entitySummaryBackfill = backfill{
	name:            "000019_entity_summary",
	requiresVersion: 19,
	step: func(ctx context.Context, q db.QueryExecer, limit int) (int, error) {
		// Claim a batch of entity keys that have no summary row yet, then
		// recompute exactly those keys. DISTINCT over an indexed scan keeps the
		// claim bounded; the recompute is per key, so the statement never
		// aggregates the whole table.
		var processed int
		err := q.GetContext(ctx, &processed, `
			WITH missing AS (
			    SELECT DISTINCT v.tenant_id, v.type_definition_id, v.entity_id
			      FROM flexitype_attribute_value v
			     WHERE v.archived_at IS NULL
			       AND NOT EXISTS (
			           SELECT 1 FROM flexitype_entity_summary s
			            WHERE s.tenant_id = v.tenant_id
			              AND s.type_definition_id = v.type_definition_id
			              AND s.entity_id = v.entity_id)
			     LIMIT $1
			), computed AS (
			    SELECT v.tenant_id, v.type_definition_id, v.entity_id,
			           count(*) AS value_count, max(v.updated_at) AS last_updated_at
			      FROM flexitype_attribute_value v
			      JOIN missing m
			        ON m.tenant_id = v.tenant_id
			       AND m.type_definition_id = v.type_definition_id
			       AND m.entity_id = v.entity_id
			     WHERE v.archived_at IS NULL
			     GROUP BY v.tenant_id, v.type_definition_id, v.entity_id
			), inserted AS (
			    INSERT INTO flexitype_entity_summary
			        (tenant_id, type_definition_id, entity_id, value_count, last_updated_at)
			    SELECT tenant_id, type_definition_id, entity_id, value_count, last_updated_at
			      FROM computed
			    ON CONFLICT (tenant_id, type_definition_id, entity_id) DO NOTHING
			    RETURNING 1
			)
			SELECT count(*) FROM inserted`, limit)
		if err != nil {
			return 0, err
		}
		return processed, nil
	},
}
