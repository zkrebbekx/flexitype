package flexitype_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestMediaKeyRefCountsPostgres covers the batched reference count the erasure
// blob GC runs once per chunk (issue #483): live rows only, across tenants,
// grouped by key, with unreferenced keys absent.
func TestMediaKeyRefCountsPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()
	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given media rows across tenants and archive states (Postgres)", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-a"))
		ia := svc.Interactors(ctx)
		doc, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "doc", DisplayName: "Doc",
		})
		So(err, ShouldBeNil)
		file, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: doc.ID.String(), InternalName: "file", DisplayName: "File",
			DataType: "media",
		})
		So(err, ShouldBeNil)

		insert := func(tenant, key string, archived bool) {
			meta, _ := json.Marshal(map[string]any{"object_key": key, "mime": "text/plain", "size": 1})
			archivedAt := "NULL"
			if archived {
				archivedAt = "now()"
			}
			_, ierr := pool.ExecContext(ctx,
				`INSERT INTO flexitype_attribute_value
				   (id, tenant_id, type_definition_id, attribute_definition_id, entity_id,
				    data_type, value_json, definition_version, created_at, updated_at, archived_at)
				 VALUES ($1, $2, $3, $4, $5, 'media', $6, 1, now(), now(), `+archivedAt+`)`,
				ulid.New().String(), tenant, doc.ID.String(), file.ID.String(), ulid.New().String(), meta)
			So(ierr, ShouldBeNil)
		}
		insert("tenant-a", "k1", false)
		insert("tenant-b", "k1", false) // cross-tenant: blob keys share one namespace
		insert("tenant-a", "k1", true)  // archived: never counted
		insert("tenant-a", "k2", true)  // archived-only key

		repo := postgres.NewAttributeValueRepository(pool)

		Convey("When the batched count runs over known and unknown keys", func() {
			counts, cerr := repo.MediaKeyRefCounts(ctx, []string{"k1", "k2", "k3"})
			So(cerr, ShouldBeNil)

			Convey("Then live rows count across tenants, grouped per key", func() {
				So(counts["k1"], ShouldEqual, 2)
			})

			Convey("Then archived-only and absent keys are absent from the result", func() {
				_, hasK2 := counts["k2"]
				_, hasK3 := counts["k3"]
				So(hasK2, ShouldBeFalse)
				So(hasK3, ShouldBeFalse)
			})
		})

		Convey("When the batch is empty", func() {
			counts, cerr := repo.MediaKeyRefCounts(ctx, nil)
			So(cerr, ShouldBeNil)
			So(counts, ShouldBeEmpty)
		})
	})
}

// TestMediaKeyLockPostgres covers the per-key advisory lock that serializes
// adoption against blob GC (issue #484): it requires a transaction, it is
// re-entrant inside one, and a second transaction waits for the holder's
// commit.
func TestMediaKeyLockPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()
	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given the Postgres value repository", t, func() {
		ctx := context.Background()
		repo := postgres.NewAttributeValueRepository(pool)
		transactor := db.NewTransactor(pool)

		Convey("When the lock is taken outside a transaction", func() {
			err := repo.LockMediaKey(ctx, "k1")

			Convey("Then it is refused", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "requires a transaction")
			})
		})

		Convey("When one transaction holds the lock", func() {
			tx1, err := transactor.Begin(ctx)
			So(err, ShouldBeNil)
			bound := repo.WithTx(tx1)
			So(bound.LockMediaKey(ctx, "k1"), ShouldBeNil)

			Convey("Then re-locking inside the same transaction returns at once", func() {
				So(bound.LockMediaKey(ctx, "k1"), ShouldBeNil)
				So(tx1.Rollback(ctx), ShouldBeNil)
			})

			Convey("Then a second transaction waits for the holder's commit", func() {
				var committed atomic.Bool
				sawCommit := make(chan bool, 1)
				go func() {
					tx2, gerr := transactor.Begin(ctx)
					if gerr != nil {
						sawCommit <- false
						return
					}
					defer func() { _ = tx2.Rollback(ctx) }()
					if lerr := repo.WithTx(tx2).LockMediaKey(ctx, "k1"); lerr != nil {
						sawCommit <- false
						return
					}
					sawCommit <- committed.Load()
				}()

				// Give the waiter time to block on the held lock, then commit.
				time.Sleep(150 * time.Millisecond)
				committed.Store(true)
				So(tx1.Commit(ctx), ShouldBeNil)

				Convey("And the waiter acquired it only after that commit", func() {
					So(<-sawCommit, ShouldBeTrue)
				})
			})
		})
	})
}
