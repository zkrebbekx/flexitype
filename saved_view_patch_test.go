package flexitype_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appsavedview "github.com/zkrebbekx/flexitype/application/savedview"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestSavedViewPatchPreservesFields covers the field a PATCH used to clear.
//
// PATCH on a saved view was a full replace: the handler decoded into a value
// struct, so a field the caller omitted was written back as its zero value.
// Renaming a view through any client — the SDK, curl, a generated client —
// silently cleared the sort order configured through another. That is data
// loss on an unrelated edit, and a saved view's whole point is that it
// reproduces a view.
//
// The Go SDK made it worse by having no Sort field at all, so it could not
// even read back what it had just erased.
func TestSavedViewPatchPreservesFields(t *testing.T) {
	Convey("Given a saved view with a query, columns and a sort order", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		views := svc.Interactors(ctx).SavedViews()
		So(views, ShouldNotBeNil)

		created, err := views.Create(ctx, appsavedview.Input{
			Name:     "Cheap widgets",
			RootType: "widget",
			Query:    "price < 10",
			Columns:  []string{"sku", "price"},
			Sort:     "price asc",
		})
		So(err, ShouldBeNil)

		name := func(s string) *string { return &s }

		Convey("When only the name is patched", func() {
			patched, err := views.Patch(ctx, created.ID.String(), appsavedview.PatchInput{
				Name: name("Bargain widgets"),
			})

			Convey("Then the name changes", func() {
				So(err, ShouldBeNil)
				So(patched.Name, ShouldEqual, "Bargain widgets")
			})

			Convey("Then the sort order survives", func() {
				So(err, ShouldBeNil)
				So(patched.Sort, ShouldEqual, "price asc")
			})

			Convey("Then the query and columns survive", func() {
				So(err, ShouldBeNil)
				So(patched.Query, ShouldEqual, "price < 10")
				So(patched.Columns, ShouldResemble, []string{"sku", "price"})
			})

			Convey("Then a re-read agrees, so nothing was lost on the way out", func() {
				So(err, ShouldBeNil)
				got, gerr := svc.Interactors(ctx).SavedViews().Get(ctx, created.ID.String())
				So(gerr, ShouldBeNil)
				So(got.Sort, ShouldEqual, "price asc")
				So(got.Name, ShouldEqual, "Bargain widgets")
			})
		})

		Convey("When a field is patched to an explicit empty value", func() {
			empty := ""
			patched, err := views.Patch(ctx, created.ID.String(), appsavedview.PatchInput{
				Sort: &empty,
			})

			Convey("Then it is cleared, because clearing must stay possible", func() {
				So(err, ShouldBeNil)
				So(patched.Sort, ShouldBeEmpty)
			})
		})

		Convey("When a patch would leave the view without a name", func() {
			patched, err := views.Patch(ctx, created.ID.String(), appsavedview.PatchInput{
				Name: name(""),
			})

			Convey("Then it is refused, so a patch cannot corrupt the view", func() {
				So(err, ShouldNotBeNil)
				So(patched, ShouldBeNil)
			})
		})

		Convey("When the id is not a valid ULID", func() {
			_, err := views.Patch(ctx, "not-an-id", appsavedview.PatchInput{Name: name("x")})

			Convey("Then it is a validation error, not a store lookup", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When the view does not exist", func() {
			_, err := views.Patch(ctx, valueobjects.NewTypeDefinitionID().String(),
				appsavedview.PatchInput{Name: name("x")})

			Convey("Then the lookup failure surfaces", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})
}
