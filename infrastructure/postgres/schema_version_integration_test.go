package postgres_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	"github.com/zkrebbekx/flexitype/application/gql"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// TestGraphQLSchemaVersionReplicaPropagationIntegration proves the fix for
// issue #192: a definition change made on one replica is picked up by a second
// replica's GraphQL engine WITHOUT that replica restarting or receiving the
// (once-per-cluster) definition event. The reader engine (B) never subscribes
// to any dispatcher, so it relies solely on the trigger-maintained persisted
// schema version (flexitype_schema_version, migration 000020).
func TestGraphQLSchemaVersionReplicaPropagationIntegration(t *testing.T) {
	pool := openIntegrationDB(t)
	defer func() { _ = pool.Close() }()
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	if err := postgres.Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given two replicas over one database: writer A and reader B", t, func() {
		// goconvey re-runs this setup per leaf assertion; start from a clean
		// definition set (the trigger-maintained version resets with it).
		pool.MustExec(`TRUNCATE flexitype_relationship_definition, flexitype_attribute_definition,
			flexitype_type_definition, flexitype_schema_version CASCADE`)

		// Replica A owns the write path: a full service whose interactors commit
		// definition changes (firing the version trigger). Its own engine is
		// irrelevant here.
		svc := flexitype.New(pool)

		// Replica B is a bare GraphQL engine wired to NO dispatcher, so it never
		// receives a definition event. TTL 0 forces it to re-read the persisted
		// version on every query, making propagation observable deterministically.
		engB := gql.NewEngine(gql.WithMemoTTL(0))
		execB := func(query string) *gql.Result {
			// A fresh request-scoped interactor set per query, like the HTTP
			// middleware installs.
			c := application.WithInteractors(ctx, svc.Interactors(ctx))
			return engB.Execute(c, query, nil)
		}

		// A creates the "widget" type; B builds and caches a schema for it.
		widget, err := svc.Interactors(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "widget", DisplayName: "Widget",
		})
		So(err, ShouldBeNil)

		warm := execB(`{ widget { edges { node { entityId } } } }`)
		So(warm.Errors, ShouldBeEmpty)

		Convey("When A adds an attribute after B has cached the schema", func() {
			// Before the change, B's cached schema has no "size" field.
			before := execB(`{ widget { edges { node { size } } } }`)
			So(before.Errors, ShouldNotBeEmpty)

			// A adds "size" — committed, firing the version trigger. B gets no event.
			_, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: widget.ID.String(), InternalName: "size", DisplayName: "Size", DataType: "integer",
			})
			So(err, ShouldBeNil)

			Convey("Then B rebuilds from the persisted version and sees the new field without restarting", func() {
				after := execB(`{ widget { edges { node { size } } } }`)
				So(after.Errors, ShouldBeEmpty)
			})
		})
	})
}
