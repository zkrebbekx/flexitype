package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/appctx"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// These suites drive the schema read ports — type definitions, attribute
// definitions and attribute values — against directly seeded rows, so each
// assertion is about the SQL the repository emits: which filters reach the
// WHERE clause, whether the separate count query agrees with the page, and
// whether archived rows and other tenants stay out.

// seedTypeDefinitionFull inserts a type definition with explicit archive state.
func seedTypeDefinitionFull(pool *sqlx.DB, tenant, name string, archived bool) ulid.ID {
	id := ulid.New()
	now := time.Now().UTC()
	var archivedAt any
	if archived {
		archivedAt = now
	}
	pool.MustExec(
		`INSERT INTO flexitype_type_definition
		   (id, tenant_id, internal_name, display_name, description, version, created_at, updated_at, archived_at)
		 VALUES ($1, $2, $3, $3, '', 1, $4, $4, $5)`,
		id.String(), tenant, name, now, archivedAt)
	return id
}

// seedAttributeDefinitionFull inserts an attribute definition with an explicit
// data type and archive state.
func seedAttributeDefinitionFull(pool *sqlx.DB, tenant string, typeID ulid.ID, name, dataType string, archived bool) ulid.ID {
	id := ulid.New()
	now := time.Now().UTC()
	var archivedAt any
	if archived {
		archivedAt = now
	}
	pool.MustExec(
		`INSERT INTO flexitype_attribute_definition
		   (id, tenant_id, type_definition_id, internal_name, display_name, data_type,
		    required, multi_valued, is_unique, constraints, version, created_at, updated_at, archived_at)
		 VALUES ($1, $2, $3, $4, $4, $5, FALSE, FALSE, FALSE, '[]'::jsonb, 1, $6, $6, $7)`,
		id.String(), tenant, typeID.String(), name, dataType, now, archivedAt)
	return id
}

// seedTextValue inserts a text attribute value for an entity.
func seedTextValue(pool *sqlx.DB, tenant string, typeID, attrID ulid.ID, entityID, text string, archived bool) ulid.ID {
	id := ulid.New()
	now := time.Now().UTC()
	var archivedAt any
	if archived {
		archivedAt = now
	}
	pool.MustExec(
		`INSERT INTO flexitype_attribute_value
		   (id, tenant_id, type_definition_id, attribute_definition_id, entity_id,
		    data_type, value_text, definition_version, created_at, updated_at, archived_at)
		 VALUES ($1, $2, $3, $4, $5, 'string', $6, 1, $7, $7, $8)`,
		id.String(), tenant, typeID.String(), attrID.String(), entityID, text, now, archivedAt)
	return id
}

func truncateValues(pool *sqlx.DB) {
	pool.MustExec(`TRUNCATE flexitype_attribute_value, flexitype_attribute_value_dependency,
		flexitype_attribute_definition, flexitype_type_definition CASCADE`)
}

func TestTypeDefinitionRepositoryReadIntegration(t *testing.T) {
	pool, _ := controlPlaneFixture(t)
	ctx := context.Background()

	Convey("Given live, archived and foreign-tenant type definitions", t, func() {
		truncateValues(pool)
		product := seedTypeDefinitionFull(pool, "default", "product", false)
		variant := seedTypeDefinitionFull(pool, "default", "variant", false)
		retired := seedTypeDefinitionFull(pool, "default", "retired", true)
		foreign := seedTypeDefinitionFull(pool, "other", "product", false)
		repo := postgres.NewTypeDefinitionRepository(pool)

		Convey("When a tenant's type definitions are listed with a total", func() {
			got, total, err := repo.List(ctx,
				domaintypedef.Filter{TenantID: "default"}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then archived and foreign-tenant types are excluded from page and count alike", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 2)
				So(got, ShouldHaveLength, 2)
				ids := []ulid.ID{}
				for _, td := range got {
					ids = append(ids, td.Snapshot().ID.ID)
				}
				So(ids, ShouldContain, product)
				So(ids, ShouldContain, variant)
				So(ids, ShouldNotContain, retired)
				So(ids, ShouldNotContain, foreign)
			})
		})

		Convey("When archived types are included", func() {
			_, total, err := repo.List(ctx,
				domaintypedef.Filter{TenantID: "default", IncludeArchived: true},
				db.Page{Limit: 10, WantTotal: true})

			Convey("Then the archived type joins the count", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 3)
			})
		})

		Convey("When the list is filtered by internal name", func() {
			got, total, err := repo.List(ctx,
				domaintypedef.Filter{TenantID: "default", InternalNames: []string{"variant"}},
				db.Page{Limit: 10, WantTotal: true})

			Convey("Then only the named type returns and the count agrees", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 1)
				So(got, ShouldHaveLength, 1)
				So(got[0].Snapshot().ID.ID, ShouldEqual, variant)
			})
		})

		Convey("When the list is filtered by several internal names", func() {
			_, total, err := repo.List(ctx,
				domaintypedef.Filter{TenantID: "default", InternalNames: []string{"product", "variant"}},
				db.Page{Limit: 10, WantTotal: true})

			Convey("Then the ANY() predicate matches both", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 2)
			})
		})

		Convey("When a type definition is fetched by its internal name", func() {
			got, err := repo.GetByInternalName(ctx, "default", "product")

			Convey("Then the right type is returned for that tenant only", func() {
				So(err, ShouldBeNil)
				So(got.Snapshot().ID.ID, ShouldEqual, product)
				So(got.Snapshot().ID.ID, ShouldNotEqual, foreign)
			})
		})

		Convey("When an unknown internal name is fetched", func() {
			_, err := repo.GetByInternalName(ctx, "default", "nothing")

			Convey("Then a not-found error names it", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "nothing")
			})
		})
	})
}

func TestAttributeDefinitionRepositoryReadIntegration(t *testing.T) {
	pool, transactor := controlPlaneFixture(t)
	ctx := context.Background()

	Convey("Given attributes of several data types, one archived, across two types", t, func() {
		truncateValues(pool)
		product := seedTypeDefinitionFull(pool, "default", "product", false)
		other := seedTypeDefinitionFull(pool, "default", "variant", false)
		colour := seedAttributeDefinitionFull(pool, "default", product, "colour", "string", false)
		weight := seedAttributeDefinitionFull(pool, "default", product, "weight", "decimal", false)
		count := seedAttributeDefinitionFull(pool, "default", product, "count", "integer", false)
		retired := seedAttributeDefinitionFull(pool, "default", product, "retired", "string", true)
		elsewhere := seedAttributeDefinitionFull(pool, "default", other, "sku", "string", false)
		repo := postgres.NewAttributeDefinitionRepository(pool)

		Convey("When an attribute is fetched by its internal name within a type", func() {
			got, err := repo.GetByInternalName(ctx,
				valueobjects.TypeDefinitionID{ID: product}, "colour")

			Convey("Then the loader resolves it with its declared data type", func() {
				So(err, ShouldBeNil)
				snap := got.Snapshot()
				So(snap.ID.ID, ShouldEqual, colour)
				So(snap.InternalName, ShouldEqual, "colour")
				So(snap.DataType, ShouldEqual, valueobjects.DataTypeString)
			})
		})

		Convey("When two names are fetched within the same request scope", func() {
			a, errA := repo.GetByInternalName(ctx, valueobjects.TypeDefinitionID{ID: product}, "colour")
			b, errB := repo.GetByInternalName(ctx, valueobjects.TypeDefinitionID{ID: product}, "weight")

			Convey("Then both resolve through the batched by-name query", func() {
				So(errA, ShouldBeNil)
				So(errB, ShouldBeNil)
				So(a.Snapshot().ID.ID, ShouldEqual, colour)
				So(b.Snapshot().ID.ID, ShouldEqual, weight)
			})
		})

		Convey("When a name that belongs to a different type is fetched", func() {
			_, err := repo.GetByInternalName(ctx, valueobjects.TypeDefinitionID{ID: product}, "sku")

			Convey("Then the type scope hides it behind a not-found", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "sku")
			})
		})

		Convey("When an archived attribute is fetched by name", func() {
			_, err := repo.GetByInternalName(ctx, valueobjects.TypeDefinitionID{ID: product}, "retired")

			Convey("Then the by-name lookup excludes archived rows", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When attributes are listed for a tenant with a total", func() {
			got, total, err := repo.List(ctx,
				domainattribute.Filter{TenantID: "default"}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then live attributes across both types return and archived ones do not", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 4)
				So(got, ShouldHaveLength, 4)
				ids := []ulid.ID{}
				for _, a := range got {
					ids = append(ids, a.Snapshot().ID.ID)
				}
				So(ids, ShouldContain, elsewhere)
				So(ids, ShouldNotContain, retired)
			})
		})

		Convey("When attributes are listed scoped to one type definition", func() {
			_, total, err := repo.List(ctx,
				domainattribute.Filter{
					TenantID:         "default",
					TypeDefinitionID: valueobjects.TypeDefinitionID{ID: product},
				}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then only that type's live attributes are counted", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 3)
			})
		})

		Convey("When attributes are filtered by internal name", func() {
			got, total, err := repo.List(ctx,
				domainattribute.Filter{TenantID: "default", InternalNames: []string{"colour", "count"}},
				db.Page{Limit: 10, WantTotal: true})

			Convey("Then the ANY() name predicate reaches both the page and the count", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 2)
				So(got, ShouldHaveLength, 2)
				ids := []ulid.ID{got[0].Snapshot().ID.ID, got[1].Snapshot().ID.ID}
				So(ids, ShouldContain, colour)
				So(ids, ShouldContain, count)
			})
		})

		Convey("When attributes are filtered by data type", func() {
			got, total, err := repo.List(ctx,
				domainattribute.Filter{
					TenantID:  "default",
					DataTypes: []valueobjects.DataType{valueobjects.DataTypeDecimal},
				}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then only attributes of that data type return", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 1)
				So(got, ShouldHaveLength, 1)
				So(got[0].Snapshot().ID.ID, ShouldEqual, weight)
			})
		})

		Convey("When archived attributes are explicitly included", func() {
			_, total, err := repo.List(ctx,
				domainattribute.Filter{TenantID: "default", IncludeArchived: true},
				db.Page{Limit: 10, WantTotal: true})

			Convey("Then the archived attribute joins the count", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 5)
			})
		})

		Convey("When attributes are listed per type definition with a total", func() {
			got, total, err := repo.ListByTypeDefinition(ctx,
				valueobjects.TypeDefinitionID{ID: product}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then the windowed per-parent query returns that type's live attributes", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 3)
				So(got, ShouldHaveLength, 3)
				for _, a := range got {
					So(a.Snapshot().TypeDefinitionID.ID, ShouldEqual, product)
				}
			})
		})

		Convey("When the by-name lookup runs inside a transaction", func() {
			var found, missing error
			var got *domainattribute.Definition
			So(transactor.InTransaction(ctx, func(tx db.Transactor) error {
				txRepo := repo.WithTx(tx)
				got, found = txRepo.GetByInternalName(ctx,
					valueobjects.TypeDefinitionID{ID: product}, "colour")
				_, missing = txRepo.GetByInternalName(ctx,
					valueobjects.TypeDefinitionID{ID: product}, "absent")
				return nil
			}), ShouldBeNil)

			Convey("Then the direct query resolves the same row and still reports not-found", func() {
				So(found, ShouldBeNil)
				So(got.Snapshot().ID.ID, ShouldEqual, colour)
				So(domainerrors.IsNotFound(missing), ShouldBeTrue)
			})
		})
	})
}

func TestAttributeValueRepositoryListIntegration(t *testing.T) {
	pool, transactor := controlPlaneFixture(t)
	ctx := context.Background()

	Convey("Given values for two entities of one type, one archived, plus another tenant", t, func() {
		truncateValues(pool)
		product := seedTypeDefinitionFull(pool, "default", "product", false)
		variant := seedTypeDefinitionFull(pool, "default", "variant", false)
		colour := seedAttributeDefinitionFull(pool, "default", product, "colour", "string", false)
		size := seedAttributeDefinitionFull(pool, "default", product, "size", "string", false)
		sku := seedAttributeDefinitionFull(pool, "default", variant, "sku", "string", false)

		e1Colour := seedTextValue(pool, "default", product, colour, "entity-1", "red", false)
		e1Size := seedTextValue(pool, "default", product, size, "entity-1", "large", false)
		e2Colour := seedTextValue(pool, "default", product, colour, "entity-2", "blue", false)
		archived := seedTextValue(pool, "default", product, colour, "entity-1", "green", true)
		variantValue := seedTextValue(pool, "default", variant, sku, "entity-3", "SKU-1", false)

		otherType := seedTypeDefinitionFull(pool, "other", "product", false)
		otherAttr := seedAttributeDefinitionFull(pool, "other", otherType, "colour", "string", false)
		foreign := seedTextValue(pool, "other", otherType, otherAttr, "entity-9", "black", false)

		repo := postgres.NewAttributeValueRepository(pool).(appctx.ValueReader)

		Convey("When a tenant's values are listed with a total", func() {
			got, total, err := repo.List(ctx,
				domainvalue.Filter{TenantID: "default"}, db.Page{Limit: 20, WantTotal: true})

			Convey("Then archived values and the other tenant stay out of page and count", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 4)
				So(got, ShouldHaveLength, 4)
				ids := []ulid.ID{}
				for _, v := range got {
					ids = append(ids, v.Snapshot().ID.ID)
				}
				So(ids, ShouldContain, e1Colour)
				So(ids, ShouldContain, e1Size)
				So(ids, ShouldContain, e2Colour)
				So(ids, ShouldContain, variantValue)
				So(ids, ShouldNotContain, archived)
				So(ids, ShouldNotContain, foreign)
			})
		})

		Convey("When values are filtered by entity", func() {
			got, total, err := repo.List(ctx,
				domainvalue.Filter{TenantID: "default", EntityID: valueobjects.EntityID("entity-1")},
				db.Page{Limit: 20, WantTotal: true})

			Convey("Then only that entity's live values return", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 2)
				So(got, ShouldHaveLength, 2)
				for _, v := range got {
					So(v.Snapshot().EntityID.String(), ShouldEqual, "entity-1")
				}
			})
		})

		Convey("When values are filtered by type definition", func() {
			_, total, err := repo.List(ctx,
				domainvalue.Filter{
					TenantID:         "default",
					TypeDefinitionID: valueobjects.TypeDefinitionID{ID: variant},
				}, db.Page{Limit: 20, WantTotal: true})

			Convey("Then only that type's values are counted", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 1)
			})
		})

		Convey("When values are filtered by attribute definition", func() {
			got, total, err := repo.List(ctx,
				domainvalue.Filter{
					TenantID:              "default",
					AttributeDefinitionID: valueobjects.AttributeDefinitionID{ID: colour},
				}, db.Page{Limit: 20, WantTotal: true})

			Convey("Then both entities' live colour values return, without the archived one", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 2)
				So(got, ShouldHaveLength, 2)
			})
		})

		Convey("When entity and attribute filters are combined", func() {
			got, total, err := repo.List(ctx,
				domainvalue.Filter{
					TenantID:              "default",
					EntityID:              valueobjects.EntityID("entity-1"),
					AttributeDefinitionID: valueobjects.AttributeDefinitionID{ID: colour},
				}, db.Page{Limit: 20, WantTotal: true})

			Convey("Then both predicates narrow the arm to a single value", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 1)
				So(got, ShouldHaveLength, 1)
				So(got[0].Snapshot().ID.ID, ShouldEqual, e1Colour)
			})
		})

		Convey("When archived values are explicitly included", func() {
			_, total, err := repo.List(ctx,
				domainvalue.Filter{TenantID: "default", IncludeArchived: true},
				db.Page{Limit: 20, WantTotal: true})

			Convey("Then the archived value joins the count", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 5)
			})
		})

		Convey("When no total is requested", func() {
			got, total, err := repo.List(ctx,
				domainvalue.Filter{TenantID: "default"}, db.Page{Limit: 20})

			Convey("Then the page still returns and the count query is skipped", func() {
				So(err, ShouldBeNil)
				So(got, ShouldHaveLength, 4)
				So(total, ShouldEqual, 0)
			})
		})

		Convey("When the list is paged with a keyset cursor", func() {
			first, _, err := postgres.NewAttributeValueRepository(pool).(appctx.ValueReader).List(ctx,
				domainvalue.Filter{TenantID: "default"}, db.Page{Limit: 2})
			So(err, ShouldBeNil)
			// FetchLimit is Limit+1, so a limit of 2 fetches 3 rows.
			So(first, ShouldHaveLength, 3)
			cursor := db.EncodeKeyset(first[1].Snapshot().ID.String())
			second, _, err := postgres.NewAttributeValueRepository(pool).(appctx.ValueReader).List(ctx,
				domainvalue.Filter{TenantID: "default"}, db.Page{Limit: 20, Cursor: cursor})

			Convey("Then the second page continues strictly after the cursor with no overlap", func() {
				So(err, ShouldBeNil)
				So(second, ShouldHaveLength, 2)
				for _, v := range second {
					So(v.Snapshot().ID.String(), ShouldBeGreaterThan,
						first[1].Snapshot().ID.String())
				}
			})
		})

		Convey("When values are listed inside a transaction", func() {
			var got []*domainvalue.AttributeValue
			var total int
			aggregate := postgres.NewAttributeValueRepository(pool)
			So(transactor.InTransaction(ctx, func(tx db.Transactor) error {
				// Reads bound to the unit of work go through the aggregate
				// repository's ValueReader view, so they see uncommitted writes.
				reader := aggregate.WithTx(tx).(appctx.ValueReader)
				var err error
				got, total, err = reader.List(ctx,
					domainvalue.Filter{TenantID: "default"}, db.Page{Limit: 20, WantTotal: true})
				return err
			}), ShouldBeNil)

			Convey("Then the direct UNION ALL arm returns the same rows the loader does", func() {
				So(total, ShouldEqual, 4)
				So(got, ShouldHaveLength, 4)
			})
		})

		Convey("When values are looked up for several entities at once", func() {
			got, err := repo.ListByEntities(ctx, "default", []valueobjects.EntityID{"entity-1", "entity-2"})

			Convey("Then live values for every requested entity return in id order", func() {
				So(err, ShouldBeNil)
				So(got, ShouldHaveLength, 3)
				for i := 1; i < len(got); i++ {
					So(got[i].Snapshot().ID.String(), ShouldBeGreaterThan,
						got[i-1].Snapshot().ID.String())
				}
			})
		})

		Convey("When values are looked up for an empty entity list", func() {
			got, err := repo.ListByEntities(ctx, "default", nil)

			Convey("Then the query is skipped entirely", func() {
				So(err, ShouldBeNil)
				So(got, ShouldBeEmpty)
			})
		})

		Convey("When values are found by definition and entity", func() {
			got, err := repo.FindByDefinitionAndEntity(ctx,
				valueobjects.AttributeDefinitionID{ID: colour},
				valueobjects.EntityID("entity-1"))

			Convey("Then only the live value for that pair returns", func() {
				So(err, ShouldBeNil)
				So(got, ShouldHaveLength, 1)
				So(got[0].Snapshot().ID.ID, ShouldEqual, e1Colour)
			})
		})
	})

	Convey("Given a closed pool", t, func() {
		closed := openIntegrationDB(t)
		So(closed.Close(), ShouldBeNil)
		broken := postgres.NewAttributeValueRepository(closed).(appctx.ValueReader)

		Convey("When each direct read path runs", func() {
			_, _, listErr := broken.List(ctx,
				domainvalue.Filter{TenantID: "default"}, db.Page{Limit: 10, WantTotal: true})
			_, entitiesErr := broken.ListByEntities(ctx, "default", []valueobjects.EntityID{"e1"})
			_, findErr := broken.FindByDefinitionAndEntity(ctx,
				valueobjects.AttributeDefinitionID{ID: ulid.New()}, valueobjects.EntityID("e1"))

			Convey("Then each wraps the driver failure with its own operation", func() {
				So(listErr, ShouldNotBeNil)
				So(listErr.Error(), ShouldContainSubstring, "batch list values")
				So(entitiesErr, ShouldNotBeNil)
				So(entitiesErr.Error(), ShouldContainSubstring, "list values by entities")
				So(findErr, ShouldNotBeNil)
				So(findErr.Error(), ShouldContainSubstring, "find values by definition and entity")
			})
		})
	})
}
