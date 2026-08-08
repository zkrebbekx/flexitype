package postgres_test

import (
	"context"
	"testing"

	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/activity"
	appoutbox "github.com/zkrebbekx/flexitype/application/outbox"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	appwebhook "github.com/zkrebbekx/flexitype/application/webhook"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/fql"
)

// TestCursorRejectionPropagatesFromEveryList pins issue #502 at the repository
// boundary: EVERY paginated PostgreSQL read must return the cursor validation
// error, not swallow it.
//
// keysetWhere used to discard the error from db.KeysetPredicate and return the
// WHERE slice unchanged. The query then ran with no keyset predicate and
// served page 1 again, on all 13 of its call sites. The error now travels out
// of each one, so this test walks them and asserts a VALIDATION code.
//
// A rejected cursor never reaches the database, so no row has to exist for
// these assertions to hold — the schema only has to be migrated.
func TestCursorRejectionPropagatesFromEveryList(t *testing.T) {
	pool, transactor := controlPlaneFixture(t)
	ctx := context.Background()
	tenant := valueobjects.DefaultTenant

	// Both cursors pass the shape check in PageArgs.Resolve.
	// twoValues has the wrong arity for every single-column (id) ordering.
	// badTime parses as neither a timestamp nor anything a "::timestamptz"
	// cast accepts, which is what used to reach PostgreSQL as SQLSTATE 22007.
	twoValues := db.Page{Limit: 5, Cursor: db.EncodeKeyset("first", "second")}
	badTime := db.Page{Limit: 5, Cursor: db.EncodeKeyset("not-a-time", "e1")}

	rejected := func(err error) {
		So(err, ShouldNotBeNil)
		So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
		So(err.Error(), ShouldNotContainSubstring, "not-a-time")
	}

	Convey("Given a migrated schema and a cursor no list can use", t, func() {
		Convey("When each id-ordered list is asked for a page", func() {
			Convey("Then every one of them returns a validation error", func() {
				_, _, err := postgres.NewTypeDefinitionRepository(pool).
					List(ctx, domaintypedef.Filter{TenantID: tenant}, twoValues)
				rejected(err)

				_, _, err = postgres.NewAttributeDefinitionRepository(pool).
					List(ctx, domainattribute.Filter{TenantID: tenant}, twoValues)
				rejected(err)

				_, _, err = postgres.NewAttributeDefinitionRepository(pool).
					ListByTypeDefinition(ctx, valueobjects.TypeDefinitionID{}, twoValues)
				rejected(err)

				values := postgres.NewAttributeValueRepository(pool).(application.ValueReader)
				_, _, err = values.List(ctx, domainvalue.Filter{TenantID: tenant}, twoValues)
				rejected(err)

				_, _, err = values.ListByDefinition(ctx, valueobjects.AttributeDefinitionID{}, twoValues)
				rejected(err)

				_, _, err = postgres.NewDependencyRepository(pool).
					List(ctx, domaindependency.Filter{TenantID: tenant}, twoValues)
				rejected(err)

				_, _, err = postgres.NewRelationshipDefinitionRepository(pool).
					List(ctx, domainrelationship.DefinitionFilter{TenantID: tenant}, twoValues)
				rejected(err)

				_, _, err = postgres.NewRelationshipRepository(pool).
					List(ctx, domainrelationship.Filter{TenantID: tenant}, twoValues)
				rejected(err)

				_, _, err = postgres.NewDeliveryStore(pool).
					List(ctx, appwebhook.DeliveryFilter{TenantID: tenant}, twoValues)
				rejected(err)

				parked, ok := postgres.NewOutboxStore(transactor).(appoutbox.OpsStore)
				So(ok, ShouldBeTrue)
				_, _, err = parked.ListParked(ctx, appoutbox.ParkedFilter{TenantID: tenant}, twoValues)
				rejected(err)
			})
		})

		Convey("When the windowed nested-connection query is asked for a page", func() {
			links, err := postgres.NewRelationshipRepository(pool).
				WindowedLinks(ctx, domainrelationship.LinkWindow{
					TenantID: tenant,
					Side:     domainrelationship.ParentSide,
					Page:     twoValues,
				}, []valueobjects.EntityID{"e1"})

			Convey("Then the arm builder returns the error rather than paging from the top", func() {
				rejected(err)
				So(links, ShouldBeNil)
			})
		})

		Convey("When a timestamp-ordered list is given a value the cast cannot parse", func() {
			Convey("Then the activity log rejects it instead of failing on the cast", func() {
				_, _, err := postgres.NewActivityLog(pool).
					List(ctx, activity.Filter{TenantID: tenant}, badTime)
				rejected(err)
			})

			Convey("And the entity listing rejects it too", func() {
				values := postgres.NewAttributeValueRepository(pool).(application.ValueReader)
				_, _, err := values.ListEntities(ctx, tenant,
					[]valueobjects.TypeDefinitionID{{}}, badTime)
				rejected(err)
			})

			Convey("And the FQL entity query rejects it too", func() {
				// An empty AND compiles cleanly, so the cursor check — which
				// runs after the tree compiles — is what this leaf reaches.
				node := &appquery.BoundLogical{Op: fql.OpAnd}
				_, _, err := postgres.NewQueryRepository(pool).
					Search(ctx, tenant, []valueobjects.TypeDefinitionID{{}}, node,
						valueobjects.Scope{}, badTime)
				rejected(err)
			})
		})
	})
}
