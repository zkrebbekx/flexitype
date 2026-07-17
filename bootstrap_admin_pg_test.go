package flexitype_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
)

// TestBootstrapAdminPostgres covers the hardened BootstrapAdmin: a second run
// is an idempotent no-op, and an unavailable account store fails closed
// (returns an error, never a fresh superuser token).
func TestBootstrapAdminPostgres(t *testing.T) {
	Convey("Given a fresh provisioning-backed service", t, func() {
		pool := openTestDB(t)
		svc := flexitype.New(pool)
		ctx := context.Background()
		So(svc.Migrate(ctx), ShouldBeNil)
		truncateAll(t, pool)

		Convey("The first BootstrapAdmin seeds one admin and returns its token", func() {
			token, err := svc.BootstrapAdmin(ctx, "default", "bootstrap-admin")
			So(err, ShouldBeNil)
			So(token, ShouldNotEqual, "")

			Convey("And a second run is an idempotent no-op — no new token, no new account", func() {
				token2, err := svc.BootstrapAdmin(ctx, "default", "bootstrap-admin")
				So(err, ShouldBeNil)
				So(token2, ShouldEqual, "")

				accts, err := svc.AdminInteractor().ListAccounts(ctx, "default")
				So(err, ShouldBeNil)
				So(len(accts), ShouldEqual, 1)
			})
		})

		Convey("When the account store is unavailable, BootstrapAdmin fails closed", func() {
			_ = pool.Close() // break the store so the existence check errors
			token, err := svc.BootstrapAdmin(ctx, "default", "bootstrap-admin")
			So(err, ShouldNotBeNil)
			So(token, ShouldEqual, "") // never mint a credential on a failed check
		})
	})
}
