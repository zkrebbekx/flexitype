package memory_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/activity"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// TestCursorRejectionPropagatesInMemory is the in-memory twin of
// infrastructure/postgres/cursor_propagation_pg_test.go, for issue #502.
//
// paginate used to decode the cursor and, on any error, page from the start.
// A cursor of the wrong arity therefore re-served page 1, and a value that
// PostgreSQL would reject on its cast was compared here as a plain string. The
// two backends gave two different answers for one bad cursor, which the parity
// suites exist to prevent. Both now return a validation error.
func TestCursorRejectionPropagatesInMemory(t *testing.T) {
	ctx := context.Background()

	// twoValues has the wrong arity for every single-column (id) ordering.
	// badTime is well formed but carries a value no timestamp column accepts.
	twoValues := db.Page{Limit: 5, Cursor: db.EncodeKeyset("first", "second")}
	badTime := db.Page{Limit: 5, Cursor: db.EncodeKeyset("not-a-time", "e1")}

	rejected := func(err error) {
		So(err, ShouldNotBeNil)
		So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
		So(err.Error(), ShouldNotContainSubstring, "not-a-time")
	}

	Convey("Given an empty in-memory store and a cursor no list can use", t, func() {
		store := memory.NewStore()
		repos := store.Repositories()
		values := repos.Values.(application.ValueReader)

		Convey("When each id-ordered list is asked for a page", func() {
			Convey("Then every one of them returns a validation error", func() {
				_, _, err := repos.TypeDefinitions.List(ctx,
					domaintypedef.Filter{TenantID: tenantA}, twoValues)
				rejected(err)

				_, _, err = repos.Attributes.List(ctx,
					domainattribute.Filter{TenantID: tenantA}, twoValues)
				rejected(err)

				_, _, err = repos.Attributes.ListByTypeDefinition(ctx,
					valueobjects.TypeDefinitionID{}, twoValues)
				rejected(err)

				_, _, err = values.List(ctx,
					domainvalue.Filter{TenantID: tenantA}, twoValues)
				rejected(err)

				_, _, err = values.ListByDefinition(ctx,
					valueobjects.AttributeDefinitionID{}, twoValues)
				rejected(err)

				_, _, err = repos.Dependencies.List(ctx,
					domaindependency.Filter{TenantID: tenantA}, twoValues)
				rejected(err)

				_, _, err = repos.RelationshipDefinitions.List(ctx,
					domainrelationship.DefinitionFilter{TenantID: tenantA}, twoValues)
				rejected(err)

				_, _, err = repos.Relationships.List(ctx,
					domainrelationship.Filter{TenantID: tenantA}, twoValues)
				rejected(err)

				_, _, err = store.ActivityLog().List(ctx,
					activity.Filter{TenantID: tenantA}, twoValues)
				rejected(err)
			})
		})

		Convey("When the windowed nested-connection query is asked for a page", func() {
			links, err := repos.Relationships.WindowedLinks(ctx,
				domainrelationship.LinkWindow{
					TenantID: tenantA,
					Side:     domainrelationship.ParentSide,
					Page:     twoValues,
				}, []valueobjects.EntityID{"e1"})

			Convey("Then it returns the error rather than paging from the top", func() {
				rejected(err)
				So(links, ShouldBeNil)
			})
		})

		Convey("When a timestamp-ordered list is given a value no timestamp accepts", func() {
			Convey("Then the entity listing rejects it, matching Postgres", func() {
				_, _, err := values.ListEntities(ctx, tenantA,
					[]valueobjects.TypeDefinitionID{{}}, badTime)
				rejected(err)
			})

			Convey("And the activity log rejects it too", func() {
				_, _, err := store.ActivityLog().List(ctx,
					activity.Filter{TenantID: tenantA}, badTime)
				rejected(err)
			})
		})

		Convey("When a stable full sweep is given a two-column cursor", func() {
			// A stable sweep pages on the immutable entity id alone, so the
			// newest-first cursor shape cannot address a row in it.
			page := twoValues
			page.Stable = true
			_, _, err := values.ListEntities(ctx, tenantA,
				[]valueobjects.TypeDefinitionID{{}}, page)

			Convey("Then it is rejected rather than sweeping from the start", func() {
				rejected(err)
			})
		})
	})
}
