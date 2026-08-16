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

// duplicateSavedViewName creates one view twice and returns the second error.
func duplicateSavedViewName(ctx context.Context, svc *flexitype.Service) error {
	ia := svc.Interactors(ctx)
	in := appsavedview.Input{Name: "My view", RootType: "product", Query: ""}
	_, err := ia.SavedViews().Create(ctx, in)
	So(err, ShouldBeNil)
	_, second := ia.SavedViews().Create(ctx, in)
	return second
}

// renameSavedViewOntoAnother creates two views and renames the second onto the
// first's name, returning the error.
//
// The RENAME is the half #599 missed. Create translated the unique violation
// and Update did not, so the identical clash was a 409 through one operation
// and a 500 through the other — and the in-memory store accepted the rename
// outright, producing two views sharing a name that its own Create refuses.
func renameSavedViewOntoAnother(ctx context.Context, svc *flexitype.Service) error {
	ia := svc.Interactors(ctx)
	first, err := ia.SavedViews().Create(ctx,
		appsavedview.Input{Name: "Taken", RootType: "product"})
	So(err, ShouldBeNil)
	So(first, ShouldNotBeNil)
	second, err := ia.SavedViews().Create(ctx,
		appsavedview.Input{Name: "Mine", RootType: "product"})
	So(err, ShouldBeNil)

	_, rerr := ia.SavedViews().Update(ctx, second.ID.String(),
		appsavedview.Input{Name: "Taken", RootType: "product"})
	return rerr
}

// TestRenamingASavedViewOntoAnotherIsAConflict is the deterministic case: one
// caller, no race.
func TestRenamingASavedViewOntoAnotherIsAConflict(t *testing.T) {
	Convey("Given two saved views (memory)", t, func() {
		svc := flexitype.NewInMemory()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

		Convey("When one is renamed onto the other", func() {
			err := renameSavedViewOntoAnother(ctx, svc)

			Convey("Then it is refused, as creating that name would be", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
			})
		})
	})
}

// TestRenamingASavedViewOntoAnotherIsAConflictPostgres is where it answered
// 500: Update wrapped the unique violation that Create translated.
func TestRenamingASavedViewOntoAnotherIsAConflictPostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given two saved views (Postgres)", t, func() {
		truncateAll(t, pool, svc)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

		Convey("When one is renamed onto the other", func() {
			err := renameSavedViewOntoAnother(ctx, svc)

			Convey("Then it is a conflict, as it is in memory", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
			})

			Convey("Then the constraint name does not reach the caller", func() {
				So(err.Error(), ShouldNotContainSubstring, "flexitype_saved_view")
				So(err.Error(), ShouldNotContainSubstring, "23505")
			})
		})
	})
}

// TestDuplicateSavedViewNameIsAConflict covers issue #599.
//
// The Postgres store wrapped every insert error, so a duplicate name arrived
// at the HTTP layer as an opaque error and became a 500. The in-memory store
// returned a domain conflict and produced a 409. The backend decided the
// status code, which is the kind of divergence a client cannot work around.
func TestDuplicateSavedViewNameIsAConflict(t *testing.T) {
	Convey("Given a saved view that already exists (memory)", t, func() {
		svc := flexitype.NewInMemory()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

		Convey("When the name is reused", func() {
			err := duplicateSavedViewName(ctx, svc)

			Convey("Then it is a conflict", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
			})
		})
	})
}

// TestDuplicateSavedViewNameIsAConflictPostgres is the backend that answered
// 500. The parity is the point, so both run.
func TestDuplicateSavedViewNameIsAConflictPostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a saved view that already exists (Postgres)", t, func() {
		truncateAll(t, pool, svc)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

		Convey("When the name is reused", func() {
			err := duplicateSavedViewName(ctx, svc)

			Convey("Then it is a conflict, as it is in memory", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
			})

			Convey("Then the constraint name does not reach the caller", func() {
				// The index name is schema detail. It belongs in the server
				// log, not in a client-facing message.
				So(err.Error(), ShouldNotContainSubstring, "uq_")
				So(err.Error(), ShouldNotContainSubstring, "23505")
			})
		})
	})
}
