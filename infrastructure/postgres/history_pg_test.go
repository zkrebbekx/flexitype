package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/changeset"
	"github.com/zkrebbekx/flexitype/application/revision"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// --- change-sets ------------------------------------------------------------

func TestChangeSetStoreListIntegration(t *testing.T) {
	pool, _ := controlPlaneFixture(t)
	ctx := context.Background()
	store := postgres.NewChangeSetStore(pool)

	Convey("Given change-sets created at different times across two tenants", t, func() {
		pool.MustExec(`TRUNCATE flexitype_changeset`)
		base := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
		mk := func(name string, state changeset.State, offset time.Duration, publishAt *time.Time) changeset.ChangeSet {
			return changeset.ChangeSet{
				ID: ulid.New(), TenantID: "default", Name: name, State: state,
				Author: "alice", PublishAt: publishAt,
				CreatedAt: base.Add(offset), UpdatedAt: base.Add(offset),
			}
		}
		oldest := mk("oldest", changeset.StateDraft, 0, nil)
		middle := mk("middle", changeset.StateDraft, time.Minute, nil)
		newest := mk("newest", changeset.StateDraft, 2*time.Minute, nil)
		foreign := mk("foreign", changeset.StateDraft, 3*time.Minute, nil)
		foreign.TenantID = "other"
		for _, cs := range []changeset.ChangeSet{oldest, middle, newest, foreign} {
			So(store.Create(ctx, cs), ShouldBeNil)
		}

		Convey("When a tenant's change-sets are listed", func() {
			got, err := store.List(ctx, "default")

			Convey("Then only its change-sets return newest-first", func() {
				So(err, ShouldBeNil)
				So(got, ShouldHaveLength, 3)
				So(got[0].ID, ShouldEqual, newest.ID)
				So(got[1].ID, ShouldEqual, middle.ID)
				So(got[2].ID, ShouldEqual, oldest.ID)
				for _, cs := range got {
					So(cs.Name, ShouldNotEqual, "foreign")
				}
			})
		})

		Convey("When a tenant with no change-sets lists them", func() {
			got, err := store.List(ctx, "empty")

			Convey("Then an empty, non-nil slice comes back", func() {
				So(err, ShouldBeNil)
				So(got, ShouldBeEmpty)
				So(got, ShouldNotBeNil)
			})
		})

		Convey("When a change-set is fetched by id", func() {
			got, err := store.Get(ctx, "default", middle.ID)

			Convey("Then its scalar fields round-trip", func() {
				So(err, ShouldBeNil)
				So(got.Name, ShouldEqual, "middle")
				So(got.State, ShouldEqual, changeset.StateDraft)
				So(got.Author, ShouldEqual, "alice")
				So(got.PublishAt, ShouldBeNil)
			})
		})

		Convey("When another tenant fetches it", func() {
			_, err := store.Get(ctx, "other", middle.ID)

			Convey("Then the tenant predicate hides it behind a not-found", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})
	})

	Convey("Given approved change-sets with and without a due publish time", t, func() {
		pool.MustExec(`TRUNCATE flexitype_changeset`)
		now := time.Now().UTC().Truncate(time.Millisecond)
		past := now.Add(-time.Hour)
		future := now.Add(time.Hour)

		due := changeset.ChangeSet{
			ID: ulid.New(), TenantID: "default", Name: "due", State: changeset.StateApproved,
			PublishAt: &past, CreatedAt: now, UpdatedAt: now,
		}
		notYet := changeset.ChangeSet{
			ID: ulid.New(), TenantID: "default", Name: "not-yet", State: changeset.StateApproved,
			PublishAt: &future, CreatedAt: now, UpdatedAt: now,
		}
		unscheduled := changeset.ChangeSet{
			ID: ulid.New(), TenantID: "default", Name: "unscheduled", State: changeset.StateApproved,
			CreatedAt: now, UpdatedAt: now,
		}
		draftButDue := changeset.ChangeSet{
			ID: ulid.New(), TenantID: "default", Name: "draft-due", State: changeset.StateDraft,
			PublishAt: &past, CreatedAt: now, UpdatedAt: now,
		}
		for _, cs := range []changeset.ChangeSet{due, notYet, unscheduled, draftButDue} {
			So(store.Create(ctx, cs), ShouldBeNil)
		}

		Convey("When the scheduler scans for sets due to publish", func() {
			got, err := store.DueForPublish(ctx, now)

			Convey("Then only approved sets whose publish time has arrived return", func() {
				So(err, ShouldBeNil)
				So(got, ShouldHaveLength, 1)
				So(got[0].ID, ShouldEqual, due.ID)
				So(got[0].PublishAt, ShouldNotBeNil)
			})
		})
	})

	Convey("Given a closed pool", t, func() {
		closed := openIntegrationDB(t)
		So(closed.Close(), ShouldBeNil)
		broken := postgres.NewChangeSetStore(closed)

		Convey("When each read path runs", func() {
			_, listErr := broken.List(ctx, "default")
			_, dueErr := broken.DueForPublish(ctx, time.Now())
			_, getErr := broken.Get(ctx, "default", ulid.New())

			Convey("Then each wraps the driver failure with its own operation", func() {
				So(listErr, ShouldNotBeNil)
				So(listErr.Error(), ShouldContainSubstring, "list change-sets")
				So(dueErr, ShouldNotBeNil)
				So(dueErr.Error(), ShouldContainSubstring, "list due change-sets")
				So(getErr, ShouldNotBeNil)
				So(getErr.Error(), ShouldContainSubstring, "get change-set")
			})
		})
	})
}

// --- entity revisions -------------------------------------------------------

func TestRevisionStoreAsOfIntegration(t *testing.T) {
	pool, transactor := controlPlaneFixture(t)
	ctx := context.Background()
	store := postgres.NewRevisionStore(pool)

	Convey("Given three revisions of one entity taken an hour apart", t, func() {
		pool.MustExec(`TRUNCATE flexitype_entity_revision`)
		base := time.Now().UTC().Add(-4 * time.Hour).Truncate(time.Millisecond)
		mk := func(seq int, label string, at time.Time) revision.Revision {
			return revision.Revision{
				ID: ulid.New(), TenantID: "default", TypeDefinitionID: "type-1",
				EntityID: "entity-1", Seq: seq, Label: label, CreatedAt: at,
				Values: []revision.Value{},
			}
		}
		first := mk(1, "created", base)
		second := mk(2, "renamed", base.Add(time.Hour))
		third := mk(3, "repriced", base.Add(2*time.Hour))
		other := mk(1, "other-entity", base.Add(3*time.Hour))
		other.EntityID = "entity-2"
		foreign := mk(1, "foreign", base.Add(3*time.Hour))
		foreign.TenantID = "other"
		foreign.EntityID = "entity-9"
		for _, r := range []revision.Revision{first, second, third, other, foreign} {
			So(store.Create(ctx, r), ShouldBeNil)
		}

		Convey("When the state is asked for an instant between two revisions", func() {
			got, err := store.AsOf(ctx, "default", "type-1", "entity-1", base.Add(90*time.Minute))

			Convey("Then the latest revision at or before that instant is returned", func() {
				So(err, ShouldBeNil)
				So(got.Seq, ShouldEqual, 2)
				So(got.Label, ShouldEqual, "renamed")
			})
		})

		Convey("When the state is asked for exactly a revision's timestamp", func() {
			got, err := store.AsOf(ctx, "default", "type-1", "entity-1", second.CreatedAt)

			Convey("Then that revision is included — the bound is inclusive", func() {
				So(err, ShouldBeNil)
				So(got.Seq, ShouldEqual, 2)
			})
		})

		Convey("When the state is asked for an instant after every revision", func() {
			got, err := store.AsOf(ctx, "default", "type-1", "entity-1", time.Now().UTC())

			Convey("Then the newest revision is returned", func() {
				So(err, ShouldBeNil)
				So(got.Seq, ShouldEqual, 3)
				So(got.Label, ShouldEqual, "repriced")
			})
		})

		Convey("When the state is asked for an instant before the first revision", func() {
			_, err := store.AsOf(ctx, "default", "type-1", "entity-1", base.Add(-time.Hour))

			Convey("Then a not-found error names the instant asked for", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "as-of")
			})
		})

		Convey("When another tenant asks for the same entity", func() {
			_, err := store.AsOf(ctx, "other", "type-1", "entity-1", time.Now().UTC())

			Convey("Then the tenant predicate keeps the history private", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When one entity's revisions are purged", func() {
			n, err := store.PurgeEntity(ctx, "default", "type-1", "entity-1")

			Convey("Then exactly that entity's revisions are removed", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 3)

				_, asOfErr := store.AsOf(ctx, "default", "type-1", "entity-1", time.Now().UTC())
				So(domainerrors.IsNotFound(asOfErr), ShouldBeTrue)

				survivors, listErr := store.List(ctx, "default", "type-1", "entity-2")
				So(listErr, ShouldBeNil)
				So(survivors, ShouldHaveLength, 1)
			})
		})

		Convey("When a whole tenant's revisions are purged", func() {
			n, err := store.PurgeTenant(ctx, "default")

			Convey("Then only that tenant's revisions go, and the count reports them", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 4)

				remaining, listErr := store.List(ctx, "other", "type-1", "entity-9")
				So(listErr, ShouldBeNil)
				So(remaining, ShouldHaveLength, 1)
			})
		})

		Convey("When a purge runs inside a transaction that rolls back", func() {
			err := transactor.InTransaction(ctx, func(tx db.Transactor) error {
				n, purgeErr := store.WithTx(tx).PurgeEntity(ctx, "default", "type-1", "entity-1")
				So(purgeErr, ShouldBeNil)
				So(n, ShouldEqual, 3)
				return errNotDurable
			})

			Convey("Then the revisions survive with the aborted unit of work", func() {
				So(err, ShouldEqual, errNotDurable)
				got, asOfErr := store.AsOf(ctx, "default", "type-1", "entity-1", time.Now().UTC())
				So(asOfErr, ShouldBeNil)
				So(got.Seq, ShouldEqual, 3)
			})
		})

		Convey("When the highest sequence is asked for", func() {
			seq, err := store.LastSeq(ctx, "default", "type-1", "entity-1")

			Convey("Then it reports the newest revision's sequence", func() {
				So(err, ShouldBeNil)
				So(seq, ShouldEqual, 3)
			})
		})

		Convey("When the highest sequence is asked for an entity with no history", func() {
			seq, err := store.LastSeq(ctx, "default", "type-1", "never-seen")

			Convey("Then it reports zero rather than failing", func() {
				So(err, ShouldBeNil)
				So(seq, ShouldEqual, 0)
			})
		})
	})

	Convey("Given a closed pool", t, func() {
		closed := openIntegrationDB(t)
		So(closed.Close(), ShouldBeNil)
		broken := postgres.NewRevisionStore(closed)

		Convey("When the as-of and purge paths run", func() {
			_, asOfErr := broken.AsOf(ctx, "default", "type-1", "entity-1", time.Now())
			_, purgeEntityErr := broken.PurgeEntity(ctx, "default", "type-1", "entity-1")
			_, purgeTenantErr := broken.PurgeTenant(ctx, "default")

			Convey("Then each wraps the driver failure with its own operation", func() {
				So(asOfErr, ShouldNotBeNil)
				So(asOfErr.Error(), ShouldContainSubstring, "get entity revision as-of")
				So(purgeEntityErr, ShouldNotBeNil)
				So(purgeEntityErr.Error(), ShouldContainSubstring, "purge entity revisions")
				So(purgeTenantErr, ShouldNotBeNil)
				So(purgeTenantErr.Error(), ShouldContainSubstring, "purge tenant revisions")
			})
		})
	})
}

// --- migration rollback -----------------------------------------------------

// TestMigrateDownIntegration runs the full up/down cycle in a DEDICATED
// PostgreSQL schema. MigrateDown drops every table it created, so it must
// never touch the shared search_path the other suites migrate into.
func TestMigrateDownIntegration(t *testing.T) {
	dsn := os.Getenv("FLEXITYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("FLEXITYPE_TEST_DSN not set; skipping database integration test")
	}
	ctx := context.Background()

	const schema = "flexitype_migrate_down_test"
	pool, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// Cleanups run last-in-first-out, so the schema is dropped before the
	// pool that drops it is closed.
	t.Cleanup(func() { _ = pool.Close() })

	pool.MustExec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
	pool.MustExec(`CREATE SCHEMA ` + schema)
	t.Cleanup(func() { pool.MustExec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) })

	// A second pool pinned to the isolated schema; every unqualified table
	// name in the migrations resolves inside it. public stays on the path so
	// extension-provided operator classes (pg_trgm's gin_trgm_ops) resolve.
	isolated, err := sqlx.Connect("postgres", dsn+"&search_path="+schema+",public")
	if err != nil {
		t.Fatalf("connect isolated: %v", err)
	}
	t.Cleanup(func() { _ = isolated.Close() })
	transactor := db.NewTransactor(isolated)

	appliedVersions := func() []int {
		var versions []int
		So(isolated.Select(&versions,
			`SELECT version FROM `+schema+`.flexitype_schema_migrations ORDER BY version`), ShouldBeNil)
		return versions
	}
	tableExists := func(name string) bool {
		var n int
		So(isolated.Get(&n,
			`SELECT count(*) FROM information_schema.tables
			 WHERE table_schema = $1 AND table_name = $2`, schema, name), ShouldBeNil)
		return n == 1
	}

	Convey("Given a freshly migrated schema", t, func() {
		So(postgres.Migrate(ctx, transactor), ShouldBeNil)
		all := appliedVersions()
		So(all, ShouldNotBeEmpty)
		So(tableExists("flexitype_type_definition"), ShouldBeTrue)
		So(tableExists("flexitype_unit_family"), ShouldBeTrue)

		Convey("When it is reverted down to a target version", func() {
			target := all[len(all)-1] - 1
			So(postgres.MigrateDown(ctx, transactor, target), ShouldBeNil)

			Convey("Then only migrations above the target are rolled back", func() {
				remaining := appliedVersions()
				So(remaining, ShouldHaveLength, len(all)-1)
				So(remaining[len(remaining)-1], ShouldEqual, target)
				// The first migration's tables are untouched by a partial revert.
				So(tableExists("flexitype_type_definition"), ShouldBeTrue)
			})

			Convey("And re-migrating restores the schema to the full version set", func() {
				So(postgres.Migrate(ctx, transactor), ShouldBeNil)
				So(appliedVersions(), ShouldResemble, all)
				So(tableExists("flexitype_unit_family"), ShouldBeTrue)
			})
		})

		Convey("When it is reverted all the way to zero", func() {
			So(postgres.MigrateDown(ctx, transactor, 0), ShouldBeNil)

			Convey("Then every migration is rolled back and its tables are gone", func() {
				So(appliedVersions(), ShouldBeEmpty)
				So(tableExists("flexitype_type_definition"), ShouldBeFalse)
				So(tableExists("flexitype_attribute_value"), ShouldBeFalse)
				So(tableExists("flexitype_unit_family"), ShouldBeFalse)
			})

			Convey("And a second revert to zero is a no-op", func() {
				So(postgres.MigrateDown(ctx, transactor, 0), ShouldBeNil)
				So(appliedVersions(), ShouldBeEmpty)
			})

			Convey("And migrating up again rebuilds the whole schema", func() {
				So(postgres.Migrate(ctx, transactor), ShouldBeNil)
				So(appliedVersions(), ShouldResemble, all)
				So(tableExists("flexitype_type_definition"), ShouldBeTrue)
			})
		})
	})
}
