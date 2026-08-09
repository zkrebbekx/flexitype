package postgres_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"

	"github.com/zkrebbekx/flexitype/internal/testdb"
)

// countValues reports how many value rows survive for the tenant.
func countValues(t *testing.T, pool *sqlx.DB) int {
	t.Helper()
	var n int
	if err := pool.Get(&n, `SELECT count(*) FROM flexitype_attribute_value WHERE tenant_id = 'default'`); err != nil {
		t.Fatalf("count values: %v", err)
	}
	return n
}

// TestPurgeSkipsNothingIntegration proves a purge deletes every matching row
// even when a concurrent transaction updates one of them mid-statement.
//
// The chunk key used to be the ctid. A ctid is not stable across an UPDATE:
// the DELETE blocks on the updated row, and when the other transaction
// commits, Postgres re-checks the qualification against the new tuple
// version, whose ctid is not in the hashed set. The row was skipped and the
// purge still reported success — a right-to-erasure receipt for data that is
// still there.
func TestPurgeSkipsNothingIntegration(t *testing.T) {
	pool := openIntegrationDB(t)
	defer func() { _ = pool.Close() }()
	ctx := context.Background()

	if err := postgres.Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	typeID := valueobjects.NewTypeDefinitionID()
	attrID := ulid.New().String()
	repo := postgres.NewAttributeValueRepository(pool)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	Convey("Given one value of an entity, updated by an uncommitted transaction", t, func() {
		testdb.TruncateTablesCascade(t, pool, "flexitype_attribute_value", "flexitype_entity_summary", "flexitype_attribute_definition", "flexitype_type_definition")
		seedSummarySchema(t, pool, typeID.String(), attrID)
		// One row is the minimal reproduction: if the only row in a chunk is
		// skipped, the chunk returns nothing and an empty chunk used to mean
		// "done".
		insertLiveValue(t, pool, typeID.String(), attrID, "eX", 1, base)
		var victim string
		So(pool.Get(&victim, `SELECT id FROM flexitype_attribute_value ORDER BY id LIMIT 1`), ShouldBeNil)

		tx, err := pool.Beginx()
		So(err, ShouldBeNil)
		_, err = tx.Exec(`UPDATE flexitype_attribute_value SET value_int = 99 WHERE id = $1`, victim)
		So(err, ShouldBeNil)

		Convey("When the entity is purged while that transaction commits", func() {
			type result struct {
				n   int
				err error
			}
			done := make(chan result, 1)
			go func() {
				_, n, perr := repo.PurgeEntity(ctx, domainvalue.EntityKey{
					TenantID:         valueobjects.DefaultTenant,
					TypeDefinitionID: typeID,
					EntityID:         valueobjects.EntityID("eX"),
				})
				done <- result{n: n, err: perr}
			}()

			// Let the DELETE reach the locked row before releasing it, so the
			// purge observes the post-UPDATE tuple version.
			time.Sleep(300 * time.Millisecond)
			So(tx.Commit(), ShouldBeNil)
			got := <-done

			Convey("Then every row is deleted and the count is honest", func() {
				So(got.err, ShouldBeNil)
				So(got.n, ShouldEqual, 1)
				So(countValues(t, pool), ShouldEqual, 0)
			})
		})
	})
}

// TestPurgeChunkingCompletesIntegration proves the chunk loop clears a row
// count larger than one chunk, and that the closing count is what decides
// completion.
func TestPurgeChunkingCompletesIntegration(t *testing.T) {
	pool := openIntegrationDB(t)
	defer func() { _ = pool.Close() }()
	ctx := context.Background()

	if err := postgres.Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	typeID := valueobjects.NewTypeDefinitionID()
	attrID := ulid.New().String()
	repo := postgres.NewAttributeValueRepository(pool)

	Convey("Given more value rows than one purge chunk holds", t, func() {
		testdb.TruncateTablesCascade(t, pool, "flexitype_attribute_value", "flexitype_entity_summary", "flexitype_attribute_definition", "flexitype_type_definition")
		seedSummarySchema(t, pool, typeID.String(), attrID)
		// 5200 rows over 13 entities: more than purgeChunk (5000), so the
		// loop runs at least twice.
		pool.MustExec(`
			INSERT INTO flexitype_attribute_value
			    (id, tenant_id, type_definition_id, attribute_definition_id, entity_id, data_type,
			     value_int, definition_version, created_at, updated_at)
			SELECT lpad(to_hex(g), 26, '0'), 'default', $1, $2, 'e' || lpad((g % 13)::text, 3, '0'),
			       'integer', g, 1, now(), now()
			  FROM generate_series(1, 5200) g`, typeID.String(), attrID)
		So(countValues(t, pool), ShouldEqual, 5200)

		Convey("When the tenant is purged", func() {
			_, n, err := repo.PurgeTenant(ctx, valueobjects.DefaultTenant)

			Convey("Then every chunk is counted and nothing is left behind", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 5200)
				So(countValues(t, pool), ShouldEqual, 0)
			})
		})
	})
}

// TestPurgeTakesEntityOrderIntegration proves a purge and a canonical-order
// batch write no longer deadlock.
//
// Every value write refreshes a shared entity-summary row. The canonical rule
// in application/value/lockorder.go orders those writes by entity id, but the
// purges deleted in an arbitrary order, so the two transactions took the same
// summary rows in opposite orders. Before the ORDER BY, this shape deadlocked
// in 4 rounds out of 4.
func TestPurgeTakesEntityOrderIntegration(t *testing.T) {
	pool := openIntegrationDB(t)
	defer func() { _ = pool.Close() }()
	ctx := context.Background()

	if err := postgres.Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	typeID := valueobjects.NewTypeDefinitionID()
	attrID := ulid.New().String()
	repo := postgres.NewAttributeValueRepository(pool)

	Convey("Given many entities and a writer that inserts in canonical entity order", t, func() {
		testdb.TruncateTablesCascade(t, pool, "flexitype_attribute_value", "flexitype_entity_summary", "flexitype_attribute_definition", "flexitype_type_definition")
		seedSummarySchema(t, pool, typeID.String(), attrID)
		pool.MustExec(`
			INSERT INTO flexitype_attribute_value
			    (id, tenant_id, type_definition_id, attribute_definition_id, entity_id, data_type,
			     value_int, definition_version, created_at, updated_at)
			SELECT lpad(to_hex(g), 26, '0'), 'default', $1, $2, 'e' || lpad((1201 - g)::text, 6, '0'),
			       'integer', g, 1, now(), now()
			  FROM generate_series(1, 1200) g`, typeID.String(), attrID)

		Convey("When a tenant purge runs concurrently with that writer, two rounds", func() {
			var errs []string
			for round := 0; round < 2; round++ {
				if round > 0 {
					testdb.TruncateTablesCascade(t, pool, "flexitype_attribute_value", "flexitype_entity_summary")
					pool.MustExec(`
						INSERT INTO flexitype_attribute_value
						    (id, tenant_id, type_definition_id, attribute_definition_id, entity_id, data_type,
						     value_int, definition_version, created_at, updated_at)
						SELECT lpad(to_hex(g), 26, '0'), 'default', $1, $2, 'e' || lpad((1201 - g)::text, 6, '0'),
						       'integer', g, 1, now(), now()
						  FROM generate_series(1, 1200) g`, typeID.String(), attrID)
				}

				var wg sync.WaitGroup
				var mu sync.Mutex
				record := func(what string, err error) {
					if err == nil {
						return
					}
					mu.Lock()
					errs = append(errs, what+": "+err.Error())
					mu.Unlock()
				}

				wg.Add(2)
				go func() {
					defer wg.Done()
					// Let the writer take its first rows, so the two
					// transactions genuinely overlap.
					time.Sleep(20 * time.Millisecond)
					_, _, err := repo.PurgeTenant(ctx, valueobjects.DefaultTenant)
					record("purge", err)
				}()
				go func() {
					defer wg.Done()
					tx, err := pool.Beginx()
					if err != nil {
						record("begin", err)
						return
					}
					// Canonical order: ascending entity id, one statement per
					// entity, so the value and summary rows lock in that
					// order. The writer updates rows the purge also deletes,
					// which is what puts the two transactions in conflict.
					for i := 1; i <= 60; i++ {
						if _, err := tx.Exec(`
							UPDATE flexitype_attribute_value
							   SET value_int = value_int + 1, updated_at = now()
							 WHERE tenant_id = 'default' AND entity_id = 'e' || lpad($1::text, 6, '0')`, i); err != nil {
							_ = tx.Rollback()
							record("batch write", err)
							return
						}
						time.Sleep(time.Millisecond)
					}
					record("commit", tx.Commit())
				}()
				wg.Wait()
			}

			Convey("Then neither side reports a deadlock", func() {
				for _, e := range errs {
					So(strings.Contains(e, "40P01"), ShouldBeFalse)
					So(strings.Contains(e, "deadlock"), ShouldBeFalse)
				}
			})
		})
	})
}

// TestPurgeReportsAStallIntegration proves a purge that cannot delete its
// rows reports that, rather than answering success.
//
// Completion used to be decided by an empty chunk. A chunk can come back
// empty while rows still match — a row skipped by the delete, or a writer
// that committed after the chunk's snapshot — so an empty chunk was not
// evidence that the predicate was empty, and erasure is the operation least
// able to tolerate a false success. A trigger that suppresses the delete
// reproduces the state deterministically.
func TestPurgeReportsAStallIntegration(t *testing.T) {
	pool := openIntegrationDB(t)
	defer func() { _ = pool.Close() }()
	ctx := context.Background()

	if err := postgres.Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	typeID := valueobjects.NewTypeDefinitionID()
	attrID := ulid.New().String()
	repo := postgres.NewAttributeValueRepository(pool)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	Convey("Given a table whose deletes are suppressed", t, func() {
		testdb.TruncateTablesCascade(t, pool, "flexitype_attribute_value", "flexitype_entity_summary", "flexitype_attribute_definition", "flexitype_type_definition")
		seedSummarySchema(t, pool, typeID.String(), attrID)
		insertLiveValue(t, pool, typeID.String(), attrID, "eX", 1, base)

		pool.MustExec(`CREATE OR REPLACE FUNCTION flexitype_test_block_delete() RETURNS trigger
			LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$`)
		pool.MustExec(`CREATE TRIGGER flexitype_test_block_delete
			BEFORE DELETE ON flexitype_attribute_value
			FOR EACH ROW EXECUTE FUNCTION flexitype_test_block_delete()`)
		Reset(func() {
			pool.MustExec(`DROP TRIGGER IF EXISTS flexitype_test_block_delete ON flexitype_attribute_value`)
			pool.MustExec(`DROP FUNCTION IF EXISTS flexitype_test_block_delete()`)
		})

		Convey("When the entity is purged", func() {
			_, n, err := repo.PurgeEntity(ctx, domainvalue.EntityKey{
				TenantID:         valueobjects.DefaultTenant,
				TypeDefinitionID: typeID,
				EntityID:         valueobjects.EntityID("eX"),
			})

			Convey("Then it reports the stall instead of a success receipt", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "still match")
				So(n, ShouldEqual, 0)
				So(countValues(t, pool), ShouldEqual, 1)
			})
		})
	})
}
