package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// The dependency read paths are dataloader-backed: point lookups batch into
// ANY() queries and filtered lists collapse into one UNION ALL. These tests
// seed the schema directly with SQL so the assertions are about what the SQL
// returns — ordering, tenant scoping, archived filtering, keyset paging —
// rather than about the write-side usecases.

// seedTypeDefinition inserts a type definition and returns its id.
func seedTypeDefinition(pool *sqlx.DB, tenant, name string) ulid.ID {
	id := ulid.New()
	now := time.Now().UTC()
	pool.MustExec(
		`INSERT INTO flexitype_type_definition
		   (id, tenant_id, internal_name, display_name, created_at, updated_at)
		 VALUES ($1, $2, $3, $3, $4, $4)`,
		id.String(), tenant, name, now)
	return id
}

// seedAttributeDefinition inserts an attribute definition under a type.
func seedAttributeDefinition(pool *sqlx.DB, tenant string, typeID ulid.ID, name string) ulid.ID {
	id := ulid.New()
	now := time.Now().UTC()
	pool.MustExec(
		`INSERT INTO flexitype_attribute_definition
		   (id, tenant_id, type_definition_id, internal_name, display_name, data_type, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $4, 'string', $5, $5)`,
		id.String(), tenant, typeID.String(), name, now)
	return id
}

// seedDependency inserts a dependency edge, optionally archived.
func seedDependency(pool *sqlx.DB, tenant string, source, target ulid.ID, description string, archived bool) ulid.ID {
	id := ulid.New()
	now := time.Now().UTC()
	var archivedAt any
	if archived {
		archivedAt = now
	}
	pool.MustExec(
		`INSERT INTO flexitype_attribute_value_dependency
		   (id, tenant_id, source_attribute_id, target_attribute_id, conditions, effect,
		    description, version, created_at, updated_at, archived_at)
		 VALUES ($1, $2, $3, $4,
		         '[{"kind":"pattern","pattern":"^A.*"}]'::jsonb,
		         '{"required":true}'::jsonb,
		         $5, 1, $6, $6, $7)`,
		id.String(), tenant, source.String(), target.String(), description, now, archivedAt)
	return id
}

func truncateSchema(pool *sqlx.DB) {
	pool.MustExec(`TRUNCATE flexitype_attribute_value_dependency, flexitype_attribute_definition,
		flexitype_type_definition CASCADE`)
}

// dependencyFixture seeds a small dependency graph:
//
//	colour --> size      (live)
//	colour --> material  (live)
//	size   --> material  (archived)
//	plus one live edge on a second tenant.
type dependencyFixture struct {
	colour, size, material ulid.ID
	colourSize             ulid.ID
	colourMaterial         ulid.ID
	archived               ulid.ID
	foreign                ulid.ID
}

func seedDependencyGraph(pool *sqlx.DB) dependencyFixture {
	truncateSchema(pool)
	typeID := seedTypeDefinition(pool, "default", "product")
	f := dependencyFixture{
		colour:   seedAttributeDefinition(pool, "default", typeID, "colour"),
		size:     seedAttributeDefinition(pool, "default", typeID, "size"),
		material: seedAttributeDefinition(pool, "default", typeID, "material"),
	}
	f.colourSize = seedDependency(pool, "default", f.colour, f.size, "colour drives size", false)
	f.colourMaterial = seedDependency(pool, "default", f.colour, f.material, "colour drives material", false)
	f.archived = seedDependency(pool, "default", f.size, f.material, "retired rule", true)

	otherType := seedTypeDefinition(pool, "other", "product")
	oa := seedAttributeDefinition(pool, "other", otherType, "colour")
	ob := seedAttributeDefinition(pool, "other", otherType, "size")
	f.foreign = seedDependency(pool, "other", oa, ob, "other tenant rule", false)
	return f
}

func TestDependencyRepositoryReadIntegration(t *testing.T) {
	pool, transactor := controlPlaneFixture(t)
	ctx := context.Background()

	Convey("Given a seeded dependency graph with a live, an archived and a foreign-tenant edge", t, func() {
		f := seedDependencyGraph(pool)
		repo := postgres.NewDependencyRepository(pool)

		Convey("When a dependency is fetched by id through the loader", func() {
			got, err := repo.Get(ctx, valueobjects.DependencyID{ID: f.colourSize})

			Convey("Then the aggregate rehydrates with its decoded conditions and effect", func() {
				So(err, ShouldBeNil)
				snap := got.Snapshot()
				So(snap.ID.ID, ShouldEqual, f.colourSize)
				So(snap.TenantID, ShouldEqual, valueobjects.TenantID("default"))
				So(snap.SourceAttributeID.ID, ShouldEqual, f.colour)
				So(snap.TargetAttributeID.ID, ShouldEqual, f.size)
				So(snap.Description, ShouldEqual, "colour drives size")
				So(snap.Conditions, ShouldHaveLength, 1)
				So(snap.Conditions[0].Kind, ShouldEqual, domaindependency.CondPattern)
				So(snap.Conditions[0].Pattern, ShouldEqual, "^A.*")
				So(snap.Effect.Required, ShouldNotBeNil)
				So(*snap.Effect.Required, ShouldBeTrue)
				So(snap.ArchivedAt, ShouldBeNil)
			})
		})

		Convey("When an archived dependency is fetched by id", func() {
			got, err := repo.Get(ctx, valueobjects.DependencyID{ID: f.archived})

			Convey("Then the point lookup still resolves it and reports the archive stamp", func() {
				So(err, ShouldBeNil)
				So(got.Snapshot().ArchivedAt, ShouldNotBeNil)
			})
		})

		Convey("When an unknown id is fetched", func() {
			missing := ulid.New()
			_, err := repo.Get(ctx, valueobjects.DependencyID{ID: missing})

			Convey("Then the loader's empty snapshot becomes a not-found error", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, missing.String())
			})
		})

		Convey("When two ids are fetched in the same request scope", func() {
			a, errA := repo.Get(ctx, valueobjects.DependencyID{ID: f.colourSize})
			b, errB := repo.Get(ctx, valueobjects.DependencyID{ID: f.colourMaterial})

			Convey("Then both resolve through the batched ANY() query", func() {
				So(errA, ShouldBeNil)
				So(errB, ShouldBeNil)
				So(a.Snapshot().Description, ShouldEqual, "colour drives size")
				So(b.Snapshot().Description, ShouldEqual, "colour drives material")
			})
		})

		Convey("When dependencies are listed by their source attribute", func() {
			got, err := repo.ListBySource(ctx, valueobjects.AttributeDefinitionID{ID: f.colour})

			Convey("Then both live edges from that source return in id order", func() {
				So(err, ShouldBeNil)
				So(got, ShouldHaveLength, 2)
				So(got[0].Snapshot().ID.String(), ShouldBeLessThan, got[1].Snapshot().ID.String())
				for _, d := range got {
					So(d.Snapshot().SourceAttributeID.ID, ShouldEqual, f.colour)
				}
			})
		})

		Convey("When dependencies are listed by a source whose only edge is archived", func() {
			got, err := repo.ListBySource(ctx, valueobjects.AttributeDefinitionID{ID: f.size})

			Convey("Then the archived edge is excluded from the by-column load", func() {
				So(err, ShouldBeNil)
				So(got, ShouldBeEmpty)
			})
		})

		Convey("When dependencies are listed by their target attribute", func() {
			got, err := repo.ListByTarget(ctx, valueobjects.AttributeDefinitionID{ID: f.material})

			Convey("Then only the live edge into that target returns", func() {
				So(err, ShouldBeNil)
				So(got, ShouldHaveLength, 1)
				So(got[0].Snapshot().ID.ID, ShouldEqual, f.colourMaterial)
			})
		})

		Convey("When a tenant's dependencies are listed with a total", func() {
			got, total, err := repo.List(ctx,
				domaindependency.Filter{TenantID: "default"}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then archived edges and the other tenant's edge are excluded from both", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 2)
				So(got, ShouldHaveLength, 2)
				ids := []ulid.ID{got[0].Snapshot().ID.ID, got[1].Snapshot().ID.ID}
				So(ids, ShouldContain, f.colourSize)
				So(ids, ShouldContain, f.colourMaterial)
				So(ids, ShouldNotContain, f.archived)
				So(ids, ShouldNotContain, f.foreign)
			})
		})

		Convey("When archived dependencies are explicitly included", func() {
			got, total, err := repo.List(ctx,
				domaindependency.Filter{TenantID: "default", IncludeArchived: true},
				db.Page{Limit: 10, WantTotal: true})

			Convey("Then the archived edge joins the page and the total", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 3)
				So(got, ShouldHaveLength, 3)
			})
		})

		Convey("When a list is filtered by source attribute", func() {
			got, total, err := repo.List(ctx,
				domaindependency.Filter{
					TenantID:          "default",
					SourceAttributeID: valueobjects.AttributeDefinitionID{ID: f.colour},
				}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then only that source's edges return and the count agrees with the page", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 2)
				So(got, ShouldHaveLength, 2)
			})
		})

		Convey("When a list is filtered by target attribute", func() {
			got, total, err := repo.List(ctx,
				domaindependency.Filter{
					TenantID:          "default",
					TargetAttributeID: valueobjects.AttributeDefinitionID{ID: f.material},
				}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then only edges into that target return", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 1)
				So(got, ShouldHaveLength, 1)
				So(got[0].Snapshot().ID.ID, ShouldEqual, f.colourMaterial)
			})
		})

		Convey("When source and target filters are combined", func() {
			got, total, err := repo.List(ctx,
				domaindependency.Filter{
					TenantID:          "default",
					SourceAttributeID: valueobjects.AttributeDefinitionID{ID: f.colour},
					TargetAttributeID: valueobjects.AttributeDefinitionID{ID: f.size},
				}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then both predicates narrow the arm to the single matching edge", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 1)
				So(got, ShouldHaveLength, 1)
				So(got[0].Snapshot().ID.ID, ShouldEqual, f.colourSize)
			})
		})

		Convey("When no total is requested", func() {
			_, total, err := repo.List(ctx,
				domaindependency.Filter{TenantID: "default"}, db.Page{Limit: 10})

			Convey("Then the count query is skipped and the total stays zero", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 0)
			})
		})

		Convey("When the list is paged with a keyset cursor", func() {
			// Each request scope gets its own loader cache, so a second page
			// must come from a fresh repository.
			first, _, err := postgres.NewDependencyRepository(pool).List(ctx,
				domaindependency.Filter{TenantID: "default"}, db.Page{Limit: 1})
			So(err, ShouldBeNil)
			// FetchLimit is Limit+1: one row for the page, one lookahead.
			So(first, ShouldHaveLength, 2)
			cursor := db.EncodeKeyset(first[0].Snapshot().ID.String())
			second, _, err := postgres.NewDependencyRepository(pool).List(ctx,
				domaindependency.Filter{TenantID: "default"}, db.Page{Limit: 10, Cursor: cursor})

			Convey("Then the next page starts strictly after the cursor", func() {
				So(err, ShouldBeNil)
				So(second, ShouldHaveLength, 1)
				So(second[0].Snapshot().ID.String(), ShouldBeGreaterThan,
					first[0].Snapshot().ID.String())
			})
		})

		Convey("When a tenant with no dependencies lists them", func() {
			got, total, err := repo.List(ctx,
				domaindependency.Filter{TenantID: "empty"}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then an empty page and zero total come back without error", func() {
				So(err, ShouldBeNil)
				So(got, ShouldBeEmpty)
				So(total, ShouldEqual, 0)
			})
		})

		Convey("When GetForUpdate is called outside a transaction", func() {
			_, err := repo.GetForUpdate(ctx, valueobjects.DependencyID{ID: f.colourSize})

			Convey("Then it refuses rather than taking a lock it cannot hold", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "requires a transaction")
			})
		})
	})

	Convey("Given a transaction-bound dependency repository", t, func() {
		f := seedDependencyGraph(pool)
		repo := postgres.NewDependencyRepository(pool)

		Convey("When reads run inside the transaction", func() {
			err := transactor.InTransaction(ctx, func(tx db.Transactor) error {
				txRepo := repo.WithTx(tx)

				// Get bypasses the loader and queries directly.
				got, getErr := txRepo.Get(ctx, valueobjects.DependencyID{ID: f.colourSize})
				So(getErr, ShouldBeNil)
				So(got.Snapshot().Description, ShouldEqual, "colour drives size")

				// GetForUpdate takes the row lock.
				locked, lockErr := txRepo.GetForUpdate(ctx, valueobjects.DependencyID{ID: f.colourSize})
				So(lockErr, ShouldBeNil)
				So(locked.Snapshot().ID.ID, ShouldEqual, f.colourSize)

				// ListBySource bypasses the loader too.
				bySource, listErr := txRepo.ListBySource(ctx,
					valueobjects.AttributeDefinitionID{ID: f.colour})
				So(listErr, ShouldBeNil)
				So(bySource, ShouldHaveLength, 2)

				byTarget, targetErr := txRepo.ListByTarget(ctx,
					valueobjects.AttributeDefinitionID{ID: f.material})
				So(targetErr, ShouldBeNil)
				So(byTarget, ShouldHaveLength, 1)

				// List runs the UNION ALL arm directly.
				listed, total, lErr := txRepo.List(ctx,
					domaindependency.Filter{TenantID: "default"},
					db.Page{Limit: 10, WantTotal: true})
				So(lErr, ShouldBeNil)
				So(total, ShouldEqual, 2)
				So(listed, ShouldHaveLength, 2)

				return nil
			})

			Convey("Then every direct read path resolves the same rows the loaders do", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When a missing id is fetched inside a transaction", func() {
			missing := ulid.New()
			var getErr, lockErr error
			So(transactor.InTransaction(ctx, func(tx db.Transactor) error {
				txRepo := repo.WithTx(tx)
				_, getErr = txRepo.Get(ctx, valueobjects.DependencyID{ID: missing})
				_, lockErr = txRepo.GetForUpdate(ctx, valueobjects.DependencyID{ID: missing})
				return nil
			}), ShouldBeNil)

			Convey("Then both direct reads report not-found", func() {
				So(domainerrors.IsNotFound(getErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(lockErr), ShouldBeTrue)
			})
		})
	})

	Convey("Given a closed pool", t, func() {
		closed := openIntegrationDB(t)
		So(closed.Close(), ShouldBeNil)
		broken := postgres.NewDependencyRepository(closed)
		id := valueobjects.DependencyID{ID: ulid.New()}
		attr := valueobjects.AttributeDefinitionID{ID: ulid.New()}

		Convey("When each read path runs", func() {
			_, getErr := broken.Get(ctx, id)
			_, sourceErr := broken.ListBySource(ctx, attr)
			_, targetErr := broken.ListByTarget(ctx, attr)
			_, _, listErr := broken.List(ctx,
				domaindependency.Filter{TenantID: "default"}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then each wraps the driver failure with the batch it was running", func() {
				So(getErr, ShouldNotBeNil)
				So(getErr.Error(), ShouldContainSubstring, "batch dependencies by id")
				So(sourceErr, ShouldNotBeNil)
				So(sourceErr.Error(), ShouldContainSubstring, "batch dependencies by source_attribute_id")
				So(targetErr, ShouldNotBeNil)
				So(targetErr.Error(), ShouldContainSubstring, "batch dependencies by target_attribute_id")
				So(listErr, ShouldNotBeNil)
				So(listErr.Error(), ShouldContainSubstring, "batch list dependencies")
			})
		})
	})
}
