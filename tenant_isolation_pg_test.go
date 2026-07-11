package flexitype_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// openTestDB connects to FLEXITYPE_TEST_DSN or skips. Every flexitype_*
// table is truncated so each goconvey leaf re-execution starts clean.
func openTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("FLEXITYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("FLEXITYPE_TEST_DSN not set; skipping database integration test")
	}
	pool, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return pool
}

func truncateAll(t *testing.T, pool *sqlx.DB) {
	t.Helper()
	var stmt string
	err := pool.Get(&stmt, `SELECT 'TRUNCATE ' || string_agg(format('%I', tablename), ', ') || ' CASCADE'
		FROM pg_tables WHERE schemaname = 'public' AND tablename LIKE 'flexitype_%'
		  AND tablename <> 'flexitype_schema_migrations'`)
	if err != nil {
		t.Fatalf("build truncate: %v", err)
	}
	pool.MustExec(stmt)
}

// TestTenantIsolationPostgres re-runs the isolation matrix over the real
// Postgres repositories, so a missing tenant filter in a by-ID SQL query
// (the original IDOR) is caught here and not only in the memory twin.
func TestTenantIsolationPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given tenant A's schema, values and relationships in Postgres", t, func() {
		truncateAll(t, pool)

		ctxA := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-a"))
		ctxB := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-b"))
		a := svc.Interactors(ctxA)
		b := svc.Interactors(ctxB)

		typeA, err := a.TypeDefinitions().Create(ctxA, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		attrA, err := a.Attributes().Create(ctxA, appattribute.CreateInput{
			TypeDefinitionID: typeA.ID.String(), InternalName: "name", DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)
		valA, err := a.Values().Set(ctxA, appvalue.SetInput{
			AttributeDefinitionID: attrA.ID.String(), EntityID: "p-1", Value: json.RawMessage(`"Widget"`),
		})
		So(err, ShouldBeNil)
		relDefA, err := a.Relationships().CreateDefinition(ctxA, apprelationship.CreateDefinitionInput{
			InternalName: "uses", DisplayName: "Uses",
			ParentTypeID: typeA.ID.String(), ChildTypeID: typeA.ID.String(),
		})
		So(err, ShouldBeNil)
		linkA, err := a.Relationships().Link(ctxA, apprelationship.LinkInput{
			DefinitionID: relDefA.ID.String(), ParentEntity: "p-1", ChildEntity: "p-2",
		})
		So(err, ShouldBeNil)

		notFound := func(err error) {
			So(err, ShouldNotBeNil)
			So(domainerrors.IsNotFound(err), ShouldBeTrue)
		}

		Convey("Then tenant B cannot read any of tenant A's aggregates by ID", func() {
			_, err := b.TypeDefinitions().Get(ctxB, typeA.ID.String())
			notFound(err)
			_, err = b.TypeDefinitions().EffectiveAttributes(ctxB, typeA.ID.String())
			notFound(err)
			_, err = b.Attributes().Get(ctxB, attrA.ID.String())
			notFound(err)
			_, err = b.Attributes().ListByTypeDefinition(ctxB, typeA.ID.String(), db.PageArgs{})
			notFound(err)
			_, err = b.Values().Get(ctxB, valA.ID.String())
			notFound(err)
			_, err = b.Relationships().GetDefinition(ctxB, relDefA.ID.String())
			notFound(err)
			_, err = b.Relationships().Get(ctxB, linkA.ID.String())
			notFound(err)
		})

		Convey("Then tenant B cannot mutate tenant A's aggregates", func() {
			_, err := b.TypeDefinitions().Update(ctxB, apptypedef.UpdateInput{
				ID: typeA.ID.String(), DisplayName: "Hijacked",
			})
			notFound(err)
			_, err = b.Attributes().Archive(ctxB, attrA.ID.String())
			notFound(err)
			_, err = b.Values().Set(ctxB, appvalue.SetInput{
				AttributeDefinitionID: attrA.ID.String(), EntityID: "b-1", Value: json.RawMessage(`"x"`),
			})
			notFound(err)
			_, err = b.Relationships().Link(ctxB, apprelationship.LinkInput{
				DefinitionID: relDefA.ID.String(), ParentEntity: "b-1", ChildEntity: "b-2",
			})
			notFound(err)

			Convey("And tenant A's data is untouched", func() {
				got, err := a.TypeDefinitions().Get(ctxA, typeA.ID.String())
				So(err, ShouldBeNil)
				So(got.DisplayName, ShouldEqual, "Product")
				So(got.ArchivedAt, ShouldBeNil)
			})
		})

		Convey("Then tenant B's own identically-named type is independent", func() {
			typeB, err := b.TypeDefinitions().Create(ctxB, apptypedef.CreateInput{
				InternalName: "product", DisplayName: "B Product",
			})
			So(err, ShouldBeNil)
			So(typeB.ID.String(), ShouldNotEqual, typeA.ID.String())
			got, err := b.TypeDefinitions().Get(ctxB, typeB.ID.String())
			So(err, ShouldBeNil)
			So(got.DisplayName, ShouldEqual, "B Product")
		})
	})
}
