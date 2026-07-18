package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// seedRelationshipDefinition inserts a relationship definition between two
// type definitions, reusing the parent type as the (unused here) attribute set.
func seedRelationshipDefinition(pool *sqlx.DB, tenant string, parentType, childType ulid.ID, name string) ulid.ID {
	id := ulid.New()
	now := time.Now().UTC()
	pool.MustExec(
		`INSERT INTO flexitype_relationship_definition
		   (id, tenant_id, internal_name, display_name, parent_type_id, child_type_id,
		    attribute_set_id, version, created_at, updated_at)
		 VALUES ($1, $2, $3, $3, $4, $5, $4, 1, $6, $6)`,
		id.String(), tenant, name, parentType.String(), childType.String(), now)
	return id
}

// seedRelationship inserts one link, optionally archived.
func seedRelationship(pool *sqlx.DB, tenant string, defID ulid.ID, parent, child string, archived bool) ulid.ID {
	id := ulid.New()
	now := time.Now().UTC()
	var archivedAt any
	if archived {
		archivedAt = now
	}
	pool.MustExec(
		`INSERT INTO flexitype_relationship
		   (id, tenant_id, relationship_definition_id, parent_entity_id, child_entity_id,
		    created_at, updated_at, archived_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $6, $7)`,
		id.String(), tenant, defID.String(), parent, child, now, archivedAt)
	return id
}

func truncateRelationships(pool *sqlx.DB) {
	pool.MustExec(`TRUNCATE flexitype_relationship, flexitype_relationship_definition,
		flexitype_attribute_value, flexitype_attribute_value_dependency,
		flexitype_attribute_definition, flexitype_type_definition CASCADE`)
}

func TestRelationshipRepositoryReadIntegration(t *testing.T) {
	pool, transactor := controlPlaneFixture(t)
	ctx := context.Background()

	Convey("Given links under two definitions, one archived, plus a foreign tenant", t, func() {
		truncateRelationships(pool)
		product := seedTypeDefinitionFull(pool, "default", "product", false)
		part := seedTypeDefinitionFull(pool, "default", "part", false)
		contains := seedRelationshipDefinition(pool, "default", product, part, "contains")
		relatedTo := seedRelationshipDefinition(pool, "default", product, part, "related_to")

		p1c1 := seedRelationship(pool, "default", contains, "prod-1", "part-1", false)
		p1c2 := seedRelationship(pool, "default", contains, "prod-1", "part-2", false)
		p2c1 := seedRelationship(pool, "default", contains, "prod-2", "part-1", false)
		related := seedRelationship(pool, "default", relatedTo, "prod-1", "part-3", false)
		archived := seedRelationship(pool, "default", contains, "prod-1", "part-9", true)

		otherProduct := seedTypeDefinitionFull(pool, "other", "product", false)
		otherPart := seedTypeDefinitionFull(pool, "other", "part", false)
		otherDef := seedRelationshipDefinition(pool, "other", otherProduct, otherPart, "contains")
		foreign := seedRelationship(pool, "other", otherDef, "prod-1", "part-1", false)

		repo := postgres.NewRelationshipRepository(pool)

		Convey("When a relationship is fetched by id through the loader", func() {
			got, err := repo.Get(ctx, valueobjects.RelationshipID{ID: p1c1})

			Convey("Then the aggregate rehydrates with both endpoints", func() {
				So(err, ShouldBeNil)
				snap := got.Snapshot()
				So(snap.ID.ID, ShouldEqual, p1c1)
				So(snap.ParentEntityID.String(), ShouldEqual, "prod-1")
				So(snap.ChildEntityID.String(), ShouldEqual, "part-1")
				So(snap.DefinitionID.ID, ShouldEqual, contains)
				So(snap.ArchivedAt, ShouldBeNil)
			})
		})

		Convey("When an unknown id is fetched", func() {
			missing := ulid.New()
			_, err := repo.Get(ctx, valueobjects.RelationshipID{ID: missing})

			Convey("Then the empty loader result becomes a not-found error", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, missing.String())
			})
		})

		Convey("When a tenant's links are listed with a total", func() {
			got, total, err := repo.List(ctx,
				domainrelationship.Filter{TenantID: "default"}, db.Page{Limit: 20, WantTotal: true})

			Convey("Then archived links and the other tenant are excluded from page and count", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 4)
				So(got, ShouldHaveLength, 4)
				ids := []ulid.ID{}
				for _, r := range got {
					ids = append(ids, r.Snapshot().ID.ID)
				}
				So(ids, ShouldContain, p1c1)
				So(ids, ShouldContain, p1c2)
				So(ids, ShouldContain, p2c1)
				So(ids, ShouldContain, related)
				So(ids, ShouldNotContain, archived)
				So(ids, ShouldNotContain, foreign)
			})
		})

		Convey("When links are filtered by relationship definition", func() {
			got, total, err := repo.List(ctx,
				domainrelationship.Filter{
					TenantID:     "default",
					DefinitionID: valueobjects.RelationshipDefinitionID{ID: relatedTo},
				}, db.Page{Limit: 20, WantTotal: true})

			Convey("Then only that definition's links return", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 1)
				So(got, ShouldHaveLength, 1)
				So(got[0].Snapshot().ID.ID, ShouldEqual, related)
			})
		})

		Convey("When links are filtered by parent entity", func() {
			_, total, err := repo.List(ctx,
				domainrelationship.Filter{TenantID: "default", ParentEntityID: "prod-1"},
				db.Page{Limit: 20, WantTotal: true})

			Convey("Then only that parent's live links are counted", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 3)
			})
		})

		Convey("When links are filtered by child entity", func() {
			got, total, err := repo.List(ctx,
				domainrelationship.Filter{TenantID: "default", ChildEntityID: "part-1"},
				db.Page{Limit: 20, WantTotal: true})

			Convey("Then both parents linking to that child return", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 2)
				So(got, ShouldHaveLength, 2)
			})
		})

		Convey("When parent, child and definition filters are combined", func() {
			got, total, err := repo.List(ctx,
				domainrelationship.Filter{
					TenantID:       "default",
					DefinitionID:   valueobjects.RelationshipDefinitionID{ID: contains},
					ParentEntityID: "prod-1",
					ChildEntityID:  "part-2",
				}, db.Page{Limit: 20, WantTotal: true})

			Convey("Then all three predicates narrow the arm to one link", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 1)
				So(got, ShouldHaveLength, 1)
				So(got[0].Snapshot().ID.ID, ShouldEqual, p1c2)
			})
		})

		Convey("When archived links are explicitly included", func() {
			_, total, err := repo.List(ctx,
				domainrelationship.Filter{TenantID: "default", IncludeArchived: true},
				db.Page{Limit: 20, WantTotal: true})

			Convey("Then the archived link joins the count", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 5)
			})
		})

		Convey("When the list is paged with a keyset cursor", func() {
			first, _, err := postgres.NewRelationshipRepository(pool).List(ctx,
				domainrelationship.Filter{TenantID: "default"}, db.Page{Limit: 2})
			So(err, ShouldBeNil)
			// FetchLimit is Limit+1, so a limit of 2 fetches 3 rows.
			So(first, ShouldHaveLength, 3)
			cursor := db.EncodeKeyset(first[1].Snapshot().ID.String())
			second, _, err := postgres.NewRelationshipRepository(pool).List(ctx,
				domainrelationship.Filter{TenantID: "default"}, db.Page{Limit: 20, Cursor: cursor})

			Convey("Then the second page continues strictly after the cursor", func() {
				So(err, ShouldBeNil)
				So(second, ShouldHaveLength, 2)
				for _, r := range second {
					So(r.Snapshot().ID.String(), ShouldBeGreaterThan,
						first[1].Snapshot().ID.String())
				}
			})
		})

		Convey("When every live link touching one entity is loaded", func() {
			got, err := repo.ListByEntity(ctx, domainrelationship.EntityLinksKey{
				TenantID: "default", EntityID: "prod-1",
			})

			Convey("Then links where it is the parent return, and the archived one does not", func() {
				So(err, ShouldBeNil)
				So(got, ShouldHaveLength, 3)
				ids := []ulid.ID{}
				for _, r := range got {
					ids = append(ids, r.Snapshot().ID.ID)
				}
				So(ids, ShouldContain, p1c1)
				So(ids, ShouldContain, p1c2)
				So(ids, ShouldContain, related)
				So(ids, ShouldNotContain, archived)
			})
		})

		Convey("When a live link is found by its definition and endpoints", func() {
			got, err := repo.FindLive(ctx,
				valueobjects.RelationshipDefinitionID{ID: contains}, "prod-1", "part-1")

			Convey("Then the matching link is returned", func() {
				So(err, ShouldBeNil)
				So(got, ShouldNotBeNil)
				So(got.Snapshot().ID.ID, ShouldEqual, p1c1)
			})
		})

		Convey("When the only link for a pair is archived", func() {
			got, err := repo.FindLive(ctx,
				valueobjects.RelationshipDefinitionID{ID: contains}, "prod-1", "part-9")

			Convey("Then no live link is reported, without an error", func() {
				So(err, ShouldBeNil)
				So(got, ShouldBeNil)
			})
		})

		Convey("When GetForUpdate is called outside a transaction", func() {
			_, err := repo.GetForUpdate(ctx, valueobjects.RelationshipID{ID: p1c1})

			Convey("Then it refuses rather than taking a lock it cannot hold", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "requires a transaction")
			})
		})

		Convey("When reads run inside a transaction", func() {
			var missingErr error
			So(transactor.InTransaction(ctx, func(tx db.Transactor) error {
				txRepo := repo.WithTx(tx)

				got, getErr := txRepo.Get(ctx, valueobjects.RelationshipID{ID: p1c1})
				So(getErr, ShouldBeNil)
				So(got.Snapshot().ID.ID, ShouldEqual, p1c1)

				locked, lockErr := txRepo.GetForUpdate(ctx, valueobjects.RelationshipID{ID: p1c1})
				So(lockErr, ShouldBeNil)
				So(locked.Snapshot().ChildEntityID.String(), ShouldEqual, "part-1")

				byEntity, entityErr := txRepo.ListByEntity(ctx, domainrelationship.EntityLinksKey{
					TenantID: "default", EntityID: "prod-1",
				})
				So(entityErr, ShouldBeNil)
				So(byEntity, ShouldHaveLength, 3)

				listed, total, listErr := txRepo.List(ctx,
					domainrelationship.Filter{TenantID: "default"},
					db.Page{Limit: 20, WantTotal: true})
				So(listErr, ShouldBeNil)
				So(total, ShouldEqual, 4)
				So(listed, ShouldHaveLength, 4)

				_, missingErr = txRepo.GetForUpdate(ctx,
					valueobjects.RelationshipID{ID: ulid.New()})
				return nil
			}), ShouldBeNil)

			Convey("Then the direct paths agree with the loaders and still report not-found", func() {
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})
	})

	Convey("Given relationship definitions over three type definitions", t, func() {
		truncateRelationships(pool)
		product := seedTypeDefinitionFull(pool, "default", "product", false)
		part := seedTypeDefinitionFull(pool, "default", "part", false)
		bundle := seedTypeDefinitionFull(pool, "default", "bundle", false)
		contains := seedRelationshipDefinition(pool, "default", product, part, "contains")
		bundles := seedRelationshipDefinition(pool, "default", bundle, product, "bundles")
		unrelated := seedRelationshipDefinition(pool, "default", bundle, bundle, "nests")

		otherType := seedTypeDefinitionFull(pool, "other", "product", false)
		foreignDef := seedRelationshipDefinition(pool, "other", otherType, otherType, "contains")

		repo := postgres.NewRelationshipDefinitionRepository(pool)

		Convey("When a tenant's definitions are listed with a total", func() {
			got, total, err := repo.List(ctx,
				domainrelationship.DefinitionFilter{TenantID: "default"},
				db.Page{Limit: 20, WantTotal: true})

			Convey("Then only its definitions return and the count agrees with the page", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 3)
				So(got, ShouldHaveLength, 3)
				ids := []ulid.ID{}
				for _, d := range got {
					ids = append(ids, d.Snapshot().ID.ID)
				}
				So(ids, ShouldContain, contains)
				So(ids, ShouldContain, bundles)
				So(ids, ShouldContain, unrelated)
				So(ids, ShouldNotContain, foreignDef)
			})
		})

		Convey("When definitions are filtered by type definition", func() {
			got, total, err := repo.List(ctx,
				domainrelationship.DefinitionFilter{
					TenantID:          "default",
					TypeDefinitionIDs: []valueobjects.TypeDefinitionID{{ID: product}},
				}, db.Page{Limit: 20, WantTotal: true})

			Convey("Then definitions matching on either endpoint return, and only those", func() {
				So(err, ShouldBeNil)
				// "contains" has product as parent; "bundles" has it as child.
				So(total, ShouldEqual, 2)
				So(got, ShouldHaveLength, 2)
				ids := []ulid.ID{got[0].Snapshot().ID.ID, got[1].Snapshot().ID.ID}
				So(ids, ShouldContain, contains)
				So(ids, ShouldContain, bundles)
				So(ids, ShouldNotContain, unrelated)
			})
		})

		Convey("When definitions are filtered by several type definitions", func() {
			_, total, err := repo.List(ctx,
				domainrelationship.DefinitionFilter{
					TenantID: "default",
					TypeDefinitionIDs: []valueobjects.TypeDefinitionID{
						{ID: product}, {ID: bundle},
					},
				}, db.Page{Limit: 20, WantTotal: true})

			Convey("Then the ANY() predicate widens to every definition touching them", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 3)
			})
		})

		Convey("When a type definition with no relationships is filtered on", func() {
			got, total, err := repo.List(ctx,
				domainrelationship.DefinitionFilter{
					TenantID:          "default",
					TypeDefinitionIDs: []valueobjects.TypeDefinitionID{{ID: ulid.New()}},
				}, db.Page{Limit: 20, WantTotal: true})

			Convey("Then an empty page and zero total come back", func() {
				So(err, ShouldBeNil)
				So(got, ShouldBeEmpty)
				So(total, ShouldEqual, 0)
			})
		})
	})

	Convey("Given a closed pool", t, func() {
		closed := openIntegrationDB(t)
		So(closed.Close(), ShouldBeNil)
		broken := postgres.NewRelationshipRepository(closed)

		Convey("When each read path runs", func() {
			_, getErr := broken.Get(ctx, valueobjects.RelationshipID{ID: ulid.New()})
			_, _, listErr := broken.List(ctx,
				domainrelationship.Filter{TenantID: "default"}, db.Page{Limit: 10, WantTotal: true})
			_, findErr := broken.FindLive(ctx,
				valueobjects.RelationshipDefinitionID{ID: ulid.New()}, "p", "c")

			Convey("Then each wraps the driver failure with its own operation", func() {
				So(getErr, ShouldNotBeNil)
				So(getErr.Error(), ShouldContainSubstring, "batch relationships by id")
				So(listErr, ShouldNotBeNil)
				So(listErr.Error(), ShouldContainSubstring, "batch list relationships")
				So(findErr, ShouldNotBeNil)
				So(findErr.Error(), ShouldContainSubstring, "find live relationship")
			})
		})
	})
}
