package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/internal/testdb"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestQueryPlansIntegration pins each hot query shape to the index that serves
// it.
//
// The regression it guards against is not a planner preference — it is a
// predicate that no index CAN serve, which is what each of these was. A
// wrapped column (`strpos(...)`), the entity folded into an aggregate FILTER
// instead of the WHERE clause, `= ANY` where an equality would let the index
// supply the ordering: each looks correct, returns correct results, and is
// distinguishable from the indexed form only by its plan. That is why these
// survived review and were found by EXPLAIN.
//
// The assertions run with sequential scans discouraged, because the property
// being pinned is whether an index CAN serve the predicate, not which plan the
// optimiser prefers at a given table size. With seqscan off an unservable
// predicate still produces a sequential scan — Postgres has nothing else to
// offer — so each assertion fails exactly when its predicate stops being
// indexable. Each case also EXPLAINs the shape it replaced, so the test
// records the difference rather than asserting an unexplained constant.
func TestQueryPlansIntegration(t *testing.T) {
	// Plan pinning needs a session-level `SET enable_seqscan = off` held
	// across statements, which a transaction-mode pooler does not preserve —
	// it hands the next statement a different backend. The pooled CI job
	// exists to prove the product's SQL is transaction-pooling safe, and this
	// test's mechanism is not; running it there would assert the pooler's
	// behaviour rather than the query shapes.
	if os.Getenv("FLEXITYPE_TEST_SHARED_SCHEMA") != "" {
		t.Skip("plan pinning needs session state a transaction pooler does not keep")
	}
	pool := testdb.Open(t, "postgres_plans")
	// One connection, so the session-level enable_seqscan below applies to
	// every statement rather than to whichever connection the pool hands out.
	pool.SetMaxOpenConns(1)
	ctx := context.Background()
	if err := Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	explain := func(query string, args ...any) string {
		var lines []string
		if err := pool.Select(&lines, "EXPLAIN "+query, args...); err != nil {
			t.Fatalf("explain %q: %v", query, err)
		}
		return strings.Join(lines, "\n")
	}

	Convey("Given a seeded schema with sequential scans discouraged", t, func() {
		testdb.TruncateAll(t, pool)
		f := seedPlanFixture(t, pool)
		pool.MustExec(`SET enable_seqscan = off`)
		defer pool.MustExec(`SET enable_seqscan = on`)

		Convey("The FQL candidate enumeration reaches the entity-summary ordering index", func() {
			// A single root type must compile to an equality. With `= ANY` the
			// index cannot supply the ordering, so the tenant's whole entity
			// population is scanned and sorted before the LIMIT applies.
			plan := explain(`SELECT tenant_id, type_definition_id, entity_id, value_count, last_updated_at
			   FROM flexitype_entity_summary
			  WHERE tenant_id = $1 AND type_definition_id = $2
			  ORDER BY last_updated_at DESC, entity_id
			  LIMIT 20`, "default", f.typeID)

			So(plan, ShouldContainSubstring, "idx_flexitype_entity_summary_order")
			So(plan, ShouldNotContainSubstring, "Sort Method")

			Convey("And the `= ANY` form it replaced could not", func() {
				old := explain(`SELECT tenant_id, type_definition_id, entity_id, value_count, last_updated_at
				   FROM flexitype_entity_summary
				  WHERE tenant_id = $1 AND type_definition_id = ANY($2)
				  ORDER BY last_updated_at DESC, entity_id
				  LIMIT 20`, "default", pq.Array([]string{f.typeID}))
				So(old, ShouldContainSubstring, "Sort")
			})
		})

		Convey("Substring matching is served by a trigram index", func() {
			if !f.trigram {
				t.Skip("pg_trgm unavailable; contains/icontains stay unindexed by design")
			}
			plan := explain(`SELECT 1 FROM flexitype_attribute_value v
			  WHERE v.value_text ILIKE $1 ESCAPE '\'`, "%needle%")

			Convey("Then the pattern itself is the index condition", func() {
				So(plan, ShouldContainSubstring, "idx_flexitype_attribute_value_trgm")
				So(plan, ShouldContainSubstring, "Index Cond")
				So(plan, ShouldContainSubstring, "~~*")
			})

			Convey("And the strpos form it replaced could only ever be a filter", func() {
				old := explain(`SELECT 1 FROM flexitype_attribute_value v
				  WHERE strpos(lower(v.value_text), $1) > 0`, "needle")
				// It may still touch the trigram index for that index's own
				// `value_text IS NOT NULL` predicate, but the substring test
				// itself is never an index condition — it is applied per row.
				So(old, ShouldContainSubstring, "Filter: (strpos")
				So(old, ShouldNotContainSubstring, "~~*")
			})
		})

		Convey("The FQL value scope reaches the entity+attribute index", func() {
			// Without (tenant_id, entity_id, attribute_definition_id) the probe
			// fetched the candidate entity's whole value set and filtered it by
			// attribute, on every candidate entity.
			plan := explain(`SELECT 1 FROM flexitype_attribute_value v
			  WHERE v.tenant_id = $1 AND v.entity_id = $2
			    AND v.attribute_definition_id = $3 AND v.archived_at IS NULL`,
				"default", f.entity, f.attrIDs[0])
			So(plan, ShouldContainSubstring, "idx_flexitype_attribute_value_entity_attr")
			So(plan, ShouldContainSubstring, "attribute_definition_id")
		})

		Convey("Archived-inclusive entity lookups reach a non-partial index", func() {
			// PurgeEntity and media download both include archived rows by
			// design, and every other entity-leading index is partial on
			// archived_at IS NULL, so both sequentially scanned the value table.
			plan := explain(`DELETE FROM flexitype_attribute_value
			  WHERE tenant_id = $1 AND type_definition_id = $2 AND entity_id = $3`,
				"default", f.typeID, f.entity)
			So(plan, ShouldContainSubstring, "idx_flexitype_attribute_value_entity_all")
		})

		Convey("Media download authorization reaches the object-key index", func() {
			plan := explain(`SELECT DISTINCT attribute_definition_id
			   FROM flexitype_attribute_value
			  WHERE tenant_id = $1 AND data_type = 'media' AND value_json->>'object_key' = $2`,
				"default", "01ABCDEF.png")
			So(plan, ShouldContainSubstring, "idx_flexitype_attribute_value_media_key")
		})

		Convey("Cardinality counting reaches the endpoint index for each side", func() {
			// The entity must be in the WHERE clause of each count. Left in an
			// aggregate FILTER over a definition-wide WHERE, both counts read
			// every live link of the definition.
			plan := explain(`SELECT
			   (SELECT count(*) FROM flexitype_relationship
			     WHERE relationship_definition_id = $1 AND parent_entity_id = $2 AND archived_at IS NULL),
			   (SELECT count(*) FROM flexitype_relationship
			     WHERE relationship_definition_id = $1 AND child_entity_id = $2 AND archived_at IS NULL)`,
				f.relDefID, f.entity)
			So(plan, ShouldContainSubstring, "idx_flexitype_relationship_parent")
			So(plan, ShouldContainSubstring, "idx_flexitype_relationship_child")
			So(plan, ShouldContainSubstring, "parent_entity_id")

			Convey("And the aggregate-FILTER form it replaced read the whole definition", func() {
				old := explain(`SELECT
				   count(*) FILTER (WHERE parent_entity_id = $2) AS as_parent,
				   count(*) FILTER (WHERE child_entity_id = $2)  AS as_child
				 FROM flexitype_relationship
				 WHERE relationship_definition_id = $1 AND archived_at IS NULL`,
					f.relDefID, f.entity)
				// The endpoint never reaches an index condition, so neither
				// endpoint index can be used and every live link of the
				// definition is read before the FILTER discards it.
				So(old, ShouldNotContainSubstring, "idx_flexitype_relationship_parent")
				So(old, ShouldNotContainSubstring, "idx_flexitype_relationship_child")
			})
		})

		Convey("Outbox expansion reaches the active-subscription index", func() {
			plan := explain(`SELECT id, tenant_id, event_types
			   FROM flexitype_webhook_subscription
			  WHERE active AND tenant_id = ANY($1)`, pq.Array([]string{"default"}))
			So(plan, ShouldContainSubstring, "idx_flexitype_webhook_subscription_active")
		})

		Convey("The delivery cascade and the retention pruner reach the envelope index", func() {
			plan := explain(`SELECT 1 FROM flexitype_webhook_delivery WHERE envelope_id = $1`,
				f.envelopeID)
			So(plan, ShouldContainSubstring, "idx_flexitype_webhook_delivery_envelope")
		})
	})
}

// planFixture holds the ids the plan assertions bind against.
type planFixture struct {
	typeID      string
	attrIDs     []string
	entity      string
	relDefID    string
	envelopeID  string
	subID       string
	trigram     bool
	entityCount int
}

// seedPlanFixture writes a small but statistically meaningful data set: enough
// rows and enough distinct values that the planner's cost estimates separate an
// index scan from a sequential one, which they do not on an empty table.
func seedPlanFixture(t *testing.T, pool *sqlx.DB) planFixture {
	t.Helper()
	f := planFixture{
		typeID:     ulid.New().String(),
		entity:     "entity-0000",
		relDefID:   ulid.New().String(),
		envelopeID: ulid.New().String(),
		subID:      ulid.New().String(),
		attrIDs:    []string{ulid.New().String(), ulid.New().String(), ulid.New().String()},
	}
	f.entityCount = 400

	pool.MustExec(`INSERT INTO flexitype_type_definition
	  (id, tenant_id, internal_name, display_name, created_at, updated_at)
	  VALUES ($1, 'default', 'product', 'Product', now(), now())`, f.typeID)
	for i, id := range f.attrIDs {
		dataType := "string"
		if i == 2 {
			dataType = "media"
		}
		pool.MustExec(`INSERT INTO flexitype_attribute_definition
		  (id, tenant_id, type_definition_id, internal_name, display_name, data_type, created_at, updated_at)
		  VALUES ($1, 'default', $2, $3, $3, $4, now(), now())`,
			id, f.typeID, fmt.Sprintf("attr%d", i), dataType)
	}

	// Values: two text values per entity, plus one media value carrying an
	// object key, so every value-table predicate under test has rows to weigh.
	// Set-based rather than row-by-row: the entity-summary trigger fires per
	// row, so 1200 separate statements cost seconds for no extra fidelity.
	pool.MustExec(`INSERT INTO flexitype_attribute_value
	  (id, tenant_id, type_definition_id, attribute_definition_id, entity_id,
	   locale, channel, data_type, value_text, definition_version, created_at, updated_at)
	  SELECT lpad(to_hex(g * 3 + j), 26, '0'), 'default', $1,
	         CASE j WHEN 0 THEN $2::char(26) ELSE $3::char(26) END,
	         'entity-' || lpad(g::text, 4, '0'),
	         '', '', 'string', 'value-' || g || '-' || j, 1, now(), now()
	    FROM generate_series(0, $4 - 1) g, generate_series(0, 1) j`,
		f.typeID, f.attrIDs[0], f.attrIDs[1], f.entityCount)

	pool.MustExec(`INSERT INTO flexitype_attribute_value
	  (id, tenant_id, type_definition_id, attribute_definition_id, entity_id,
	   locale, channel, data_type, value_json, definition_version, created_at, updated_at)
	  SELECT lpad(to_hex(1000000 + g), 26, '0'), 'default', $1, $2,
	         'entity-' || lpad(g::text, 4, '0'), '', '', 'media',
	         jsonb_build_object('object_key', 'key-' || lpad(g::text, 4, '0') || '.png',
	                            'mime', 'image/png', 'size', 1),
	         1, now(), now()
	    FROM generate_series(0, $3 - 1) g`,
		f.typeID, f.attrIDs[2], f.entityCount)

	// Relationships: one definition, many distinct parents and children, so the
	// two endpoint indexes are genuinely selective.
	pool.MustExec(`INSERT INTO flexitype_relationship_definition
	  (id, tenant_id, internal_name, display_name, parent_type_id, child_type_id,
	   attribute_set_id, created_at, updated_at)
	  VALUES ($1, 'default', 'contains', 'Contains', $2, $2, $2, now(), now())`, f.relDefID, f.typeID)
	pool.MustExec(`INSERT INTO flexitype_relationship
	  (id, tenant_id, relationship_definition_id, parent_entity_id, child_entity_id, created_at, updated_at)
	  SELECT lpad(to_hex(2000000 + g), 26, '0'), 'default', $1,
	         'entity-' || lpad(g::text, 4, '0'),
	         'entity-' || lpad(((g + 1) % $2)::text, 4, '0'),
	         now(), now()
	    FROM generate_series(0, $2 - 1) g`, f.relDefID, f.entityCount)

	// Delivery machinery: one envelope with fan-out, plus subscriptions across
	// several tenants so the active-by-tenant index has something to narrow.
	pool.MustExec(`INSERT INTO flexitype_event_outbox
	  (id, tenant_id, event_type, aggregate_type, aggregate_id, payload, occurred_at, recorded_at)
	  VALUES ($1, 'default', 'flexitype.attribute_value.set', 'attribute_value', 'a', '{}'::jsonb, now(), now())`,
		f.envelopeID)
	pool.MustExec(`INSERT INTO flexitype_webhook_subscription
	  (id, tenant_id, name, url, secret, event_types, active, created_at, updated_at)
	  VALUES ($1, 'default', 'hook', 'https://example.test/hook', 's', '{}', true, now(), now())`, f.subID)
	pool.MustExec(`INSERT INTO flexitype_webhook_subscription
	  (id, tenant_id, name, url, secret, event_types, active, created_at, updated_at)
	  SELECT lpad(to_hex(3000000 + g), 26, '0'), 'tenant-' || lpad(g::text, 2, '0'),
	         'hook', 'https://example.test/hook', 's', '{}', true, now(), now()
	    FROM generate_series(1, 49) g`)
	pool.MustExec(`INSERT INTO flexitype_webhook_delivery
	  (id, subscription_id, envelope_id, tenant_id, event_type, feed_seq, status,
	   next_attempt_at, created_at, updated_at)
	  SELECT lpad(to_hex(4000000 + g), 26, '0'), $1, $2, 'default',
	         'flexitype.attribute_value.set', g, 'delivered', now(), now(), now()
	    FROM generate_series(1, 200) g`, f.subID, f.envelopeID)

	pool.MustExec(`ANALYZE flexitype_attribute_value, flexitype_entity_summary,
		flexitype_relationship, flexitype_webhook_subscription, flexitype_webhook_delivery`)

	if err := pool.Get(&f.trigram,
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')`); err != nil {
		t.Fatalf("probe pg_trgm: %v", err)
	}
	return f
}
