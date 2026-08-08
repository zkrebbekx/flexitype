package flexitype_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
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

// TestBootstrapAdminWithTokenPostgres covers the caller-supplied credential.
//
// A minted token is printed once and the shipped image is distroless, so an
// orchestrated stack cannot capture it: every service starts at the same
// moment, and the one that needs the admin credential needs it before the log
// line exists. A deployment can now decide the token itself, and hand the same
// value to the service and to whatever calls the admin API.
func TestBootstrapAdminWithTokenPostgres(t *testing.T) {
	Convey("Given a fresh provisioning-backed service", t, func() {
		pool := openTestDB(t)
		svc := flexitype.New(pool)
		ctx := context.Background()
		So(svc.Migrate(ctx), ShouldBeNil)
		truncateAll(t, pool)

		// The form the service mints, and the form `flexitype bootstrap-token`
		// prints: ft_<ULID>_<secret>.
		token := serviceaccount.MintToken(ulid.New().String(), "0123456789abcdef0123456789abcdef0123456789a")

		Convey("When the deployment supplies the token", func() {
			created, err := svc.BootstrapAdminWithToken(ctx, "default", "bootstrap-admin", token)

			Convey("Then the account is created", func() {
				So(err, ShouldBeNil)
				So(created, ShouldBeTrue)
			})

			Convey("And that exact token authenticates", func() {
				So(err, ShouldBeNil)
				account, aerr := svc.NewAccountLookup(0).Authenticate(token)
				So(aerr, ShouldBeNil)
				So(account.TenantID, ShouldEqual, "default")
				scopes := []string{}
				for _, scope := range account.Scopes {
					scopes = append(scopes, string(scope))
				}
				So(scopes, ShouldContain, "admin")
			})

			Convey("And a second run leaves the live credential alone", func() {
				So(err, ShouldBeNil)
				other := serviceaccount.MintToken(ulid.New().String(), "ffffffffffffffffffffffffffffffffffffffffffff")
				again, aerr := svc.BootstrapAdminWithToken(ctx, "default", "bootstrap-admin", other)
				So(aerr, ShouldBeNil)
				So(again, ShouldBeFalse)

				// An environment variable must not re-key a running
				// deployment: the first token still works, the second never
				// did.
				_, oerr := svc.NewAccountLookup(0).Authenticate(other)
				So(oerr, ShouldNotBeNil)
				_, ferr := svc.NewAccountLookup(0).Authenticate(token)
				So(ferr, ShouldBeNil)

				accounts, lerr := svc.AdminInteractor().ListAccounts(ctx, "default")
				So(lerr, ShouldBeNil)
				So(len(accounts), ShouldEqual, 1)
			})
		})

		Convey("When the supplied token is malformed", func() {
			created, err := svc.BootstrapAdminWithToken(ctx, "default", "bootstrap-admin", "not-a-token")

			Convey("Then it is refused at boot, and no account is created", func() {
				So(err, ShouldNotBeNil)
				So(created, ShouldBeFalse)
				accounts, lerr := svc.AdminInteractor().ListAccounts(ctx, "default")
				So(lerr, ShouldBeNil)
				So(accounts, ShouldBeEmpty)
			})
		})

		Convey("When the supplied secret is too short to be a credential", func() {
			weak := serviceaccount.MintToken(ulid.New().String(), "letmein")
			created, err := svc.BootstrapAdminWithToken(ctx, "default", "bootstrap-admin", weak)

			Convey("Then it is refused, rather than becoming a guessable superuser", func() {
				So(err, ShouldNotBeNil)
				So(created, ShouldBeFalse)
			})
		})

		Convey("When the id half is not a ULID", func() {
			created, err := svc.BootstrapAdminWithToken(ctx, "default", "bootstrap-admin",
				"ft_notaulid_0123456789abcdef0123456789abcdef0123456789a")

			Convey("Then it is refused", func() {
				So(err, ShouldNotBeNil)
				So(created, ShouldBeFalse)
			})
		})
	})
}
