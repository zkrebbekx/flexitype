package flexitype_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appsavedview "github.com/zkrebbekx/flexitype/application/savedview"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestSavedViewsParityPostgres re-runs the saved-view suite
// (infrastructure/memory/saved_view_test.go) against the Postgres saved-view
// store: round-tripping a query/columns/sort, listing, rename+delete, input
// validation, and per-tenant isolation of the store.
func TestSavedViewsParityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given the saved-view usecases over the Postgres store", t, func() {
		truncateAll(t, pool)
		ctxA := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-a"))
		ctxB := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-b"))
		a := svc.Interactors(ctxA).SavedViews()
		So(a, ShouldNotBeNil)

		Convey("When a view is saved with a query and columns", func() {
			v, err := a.Create(ctxA, appsavedview.Input{
				Name: "Active bikes", RootType: "product", Query: `category = "bike"`,
				Columns: []string{"name", "price", "status"}, Sort: "name",
			})

			Convey("Then it can be read back with its filter, columns and sort", func() {
				So(err, ShouldBeNil)
				got, err := a.Get(ctxA, v.ID.String())
				So(err, ShouldBeNil)
				So(got.Query, ShouldEqual, `category = "bike"`)
				So(got.Columns, ShouldResemble, []string{"name", "price", "status"})
				So(got.Sort, ShouldEqual, "name")
			})

			Convey("And it appears in the list", func() {
				list, err := a.List(ctxA)
				So(err, ShouldBeNil)
				So(list, ShouldHaveLength, 1)
			})

			Convey("And renaming then deleting works", func() {
				_, err := a.Update(ctxA, v.ID.String(), appsavedview.Input{
					Name: "Bikes", RootType: "product", Query: v.Query, Columns: v.Columns,
				})
				So(err, ShouldBeNil)
				renamed, _ := a.Get(ctxA, v.ID.String())
				So(renamed.Name, ShouldEqual, "Bikes")

				So(a.Delete(ctxA, v.ID.String()), ShouldBeNil)
				_, err = a.Get(ctxA, v.ID.String())
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When a name or root type is missing", func() {
			_, err1 := a.Create(ctxA, appsavedview.Input{Name: "", RootType: "product"})
			_, err2 := a.Create(ctxA, appsavedview.Input{Name: "x", RootType: ""})
			Convey("Then it is rejected", func() {
				So(domainerrors.CodeOf(err1), ShouldEqual, domainerrors.CodeValidation)
				So(domainerrors.CodeOf(err2), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("When tenant A saves a view", func() {
			v, err := a.Create(ctxA, appsavedview.Input{Name: "A view", RootType: "product"})
			So(err, ShouldBeNil)

			Convey("Then tenant B cannot see or fetch it", func() {
				b := svc.Interactors(ctxB).SavedViews()
				list, err := b.List(ctxB)
				So(err, ShouldBeNil)
				So(list, ShouldBeEmpty)
				_, err = b.Get(ctxB, v.ID.String())
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})
	})
}
