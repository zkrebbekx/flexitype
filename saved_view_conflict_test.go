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
