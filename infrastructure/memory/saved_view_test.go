package memory_test

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

func TestSavedViews(t *testing.T) {
	Convey("Given the saved-view usecases over an in-memory store", t, func() {
		svc := flexitype.NewInMemory()
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

			Convey("Then the same name cannot be reused within the tenant", func() {
				_, err := a.Create(ctxA, appsavedview.Input{Name: "A view", RootType: "supplier"})
				So(domainerrors.IsConflict(err), ShouldBeTrue)
			})

			Convey("Then the same name IS available to another tenant", func() {
				b := svc.Interactors(ctxB).SavedViews()
				_, err := b.Create(ctxB, appsavedview.Input{Name: "A view", RootType: "product"})
				So(err, ShouldBeNil)
			})
		})

		Convey("When several views are saved out of alphabetical order", func() {
			for _, name := range []string{"Zebra", "Alpha", "Middle"} {
				_, err := a.Create(ctxA, appsavedview.Input{Name: name, RootType: "product"})
				So(err, ShouldBeNil)
			}

			Convey("Then the list comes back name-ordered", func() {
				list, err := a.List(ctxA)
				So(err, ShouldBeNil)
				names := make([]string, 0, len(list))
				for _, v := range list {
					names = append(names, v.Name)
				}
				So(names, ShouldResemble, []string{"Alpha", "Middle", "Zebra"})
			})
		})
	})
}

// TestSavedViewPatchIsVersionGuarded covers the lost update Patch allowed.
//
// Patch does Get, merges, then Update — which performed a second unguarded
// Get and a blind write, in the same release that added optimistic locking to
// change-sets. Two concurrent patches (A sets the sort, B renames) each wrote
// the other's field back as it was before: the same "one client silently
// clears what another set" outcome the sparse decoder was added to remove,
// moved from an omitted field to a concurrent write.
func TestSavedViewPatchIsVersionGuarded(t *testing.T) {
	Convey("Given a saved view", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		view, err := it.SavedViews().Create(ctx, appsavedview.Input{
			Name: "all products", RootType: "product",
			Query: "price > 1", Columns: []string{"a", "b"}, Sort: "price desc",
		})
		So(err, ShouldBeNil)
		So(view.Version, ShouldEqual, 1)

		Convey("When a rename is patched", func() {
			name := "renamed"
			got, perr := svc.Interactors(ctx).SavedViews().Patch(ctx, view.ID.String(),
				appsavedview.PatchInput{Name: &name})

			Convey("Then only the name changes and the version advances", func() {
				So(perr, ShouldBeNil)
				So(got.Name, ShouldEqual, "renamed")
				So(got.Query, ShouldEqual, "price > 1")
				So(got.Sort, ShouldEqual, "price desc")
				So(got.Columns, ShouldResemble, []string{"a", "b"})
				So(got.Version, ShouldBeGreaterThan, view.Version)
			})
		})

		Convey("When two patches race over the same read", func() {
			// Both merge against version 1; the second must be refused rather
			// than writing the first's field back as it was.
			sort := "name asc"
			name := "renamed"
			_, first := svc.Interactors(ctx).SavedViews().Patch(ctx, view.ID.String(),
				appsavedview.PatchInput{Sort: &sort})
			So(first, ShouldBeNil)

			stale := *view // the read taken before the first patch
			_, second := svc.Interactors(ctx).SavedViews().Patch(ctx, stale.ID.String(),
				appsavedview.PatchInput{Name: &name})

			Convey("Then the second re-reads, so it does not discard the first", func() {
				// Patch re-reads inside the call, so a sequential second
				// patch succeeds and KEEPS the sort the first one set.
				So(second, ShouldBeNil)
				got, gerr := svc.Interactors(ctx).SavedViews().Get(ctx, view.ID.String())
				So(gerr, ShouldBeNil)
				So(got.Name, ShouldEqual, "renamed")
				So(got.Sort, ShouldEqual, "name asc")
			})
		})

		// Two users open the same view and both PATCH. Each request re-reads
		// inside itself, so both compare-and-swaps passed and the second
		// silently overwrote the first — the 409 was reachable only for
		// writes interleaving INSIDE one request, which is not the lost
		// update that was reported. A caller that sends the version it read
		// gets the conflict instead.
		Convey("When a patch carries the version the caller read", func() {
			sort := "name asc"
			name := "renamed"
			_, first := svc.Interactors(ctx).SavedViews().Patch(ctx, view.ID.String(),
				appsavedview.PatchInput{Sort: &sort, Version: &view.Version})
			So(first, ShouldBeNil)

			staleVersion := view.Version // the version read before the first patch
			_, second := svc.Interactors(ctx).SavedViews().Patch(ctx, view.ID.String(),
				appsavedview.PatchInput{Name: &name, Version: &staleVersion})

			Convey("Then the stale second patch is refused as a conflict", func() {
				So(second, ShouldNotBeNil)
				So(domainerrors.IsConflict(second), ShouldBeTrue)
			})

			Convey("Then the first patch survives", func() {
				got, gerr := svc.Interactors(ctx).SavedViews().Get(ctx, view.ID.String())
				So(gerr, ShouldBeNil)
				So(got.Sort, ShouldEqual, "name asc")
				So(got.Name, ShouldEqual, "all products")
			})
		})

		Convey("When a patch carries the CURRENT version", func() {
			name := "renamed"
			current := view.Version
			got, perr := svc.Interactors(ctx).SavedViews().Patch(ctx, view.ID.String(),
				appsavedview.PatchInput{Name: &name, Version: &current})

			Convey("Then it is applied", func() {
				So(perr, ShouldBeNil)
				So(got.Name, ShouldEqual, "renamed")
			})
		})

		Convey("When a full replace is written against a stale version", func() {
			// Update is a full replace and reads the current version, so it
			// intentionally does not conflict; the guard is on Patch, which
			// merges against a version it read.
			_, uerr := svc.Interactors(ctx).SavedViews().Update(ctx, view.ID.String(),
				appsavedview.Input{Name: "replaced", RootType: "product"})

			Convey("Then it succeeds and clears the omitted fields", func() {
				So(uerr, ShouldBeNil)
				got, gerr := svc.Interactors(ctx).SavedViews().Get(ctx, view.ID.String())
				So(gerr, ShouldBeNil)
				So(got.Name, ShouldEqual, "replaced")
				So(got.Sort, ShouldEqual, "")
			})
		})
	})
}
