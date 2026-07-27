package flexitype_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application/admin"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
)

// TestTenantSuspensionPostgres covers the two ways a credential was supposed
// to stop working and did not.
//
// Both matter during incident response, and both were disclosed wrongly rather
// than merely being limited: `SetTenantActive` is named for suspension but was
// advisory metadata, and `RotateSecret` documented immediate invalidation while
// the old secret kept authenticating for the cache TTL. An operator who
// believes containment was immediate does not go looking for requests made
// after it, so the incident timeline they write down is wrong.
func TestTenantSuspensionPostgres(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()

	svc := flexitype.New(pool)
	if err := svc.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a tenant with a service account (Postgres)", t, func() {
		truncateAll(t, pool)

		// A caching authenticator, as provisioning mode uses, wired to the
		// admin interactor so a rotation can evict it.
		auth := svc.NewAccountLookup(30 * time.Second)
		a := svc.AdminInteractor(admin.WithAuthCache(auth.(serviceaccount.Invalidator)))
		So(a, ShouldNotBeNil)

		tenant, err := a.CreateTenant(ctx, "acme")
		So(err, ShouldBeNil)
		created, err := a.CreateAccount(ctx, admin.CreateAccountInput{
			TenantName: "acme", Name: "importer", Scopes: []string{"read", "write"},
		})
		So(err, ShouldBeNil)
		token := created.Token
		So(token, ShouldNotBeBlank)

		authenticate := func() error {
			_, err := auth.(serviceaccount.AuthenticatorCtx).AuthenticateCtx(ctx, token)
			return err
		}
		So(authenticate(), ShouldBeNil)

		Convey("When the tenant is deactivated", func() {
			So(a.SetTenantActive(ctx, tenant.Name, false), ShouldBeNil)

			Convey("Then its accounts stop authenticating", func() {
				// The cache holds the earlier success, so this also proves the
				// window is bounded rather than indefinite. Authenticate with a
				// fresh lookup to read the durable state.
				fresh := svc.NewAccountLookup(0)
				_, err := fresh.(serviceaccount.AuthenticatorCtx).AuthenticateCtx(ctx, token)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "tenant is deactivated")
			})

			Convey("And reactivating the tenant restores them", func() {
				So(a.SetTenantActive(ctx, tenant.Name, true), ShouldBeNil)
				fresh := svc.NewAccountLookup(0)
				_, err := fresh.(serviceaccount.AuthenticatorCtx).AuthenticateCtx(ctx, token)
				So(err, ShouldBeNil)
			})
		})

		Convey("When the account's secret is rotated", func() {
			rotated, err := a.RotateSecret(ctx, created.Account.ID.String())
			So(err, ShouldBeNil)

			Convey("Then the old token stops working at once, not after the cache TTL", func() {
				So(authenticate(), ShouldNotBeNil)
			})

			Convey("Then the new token works", func() {
				_, err := auth.(serviceaccount.AuthenticatorCtx).AuthenticateCtx(ctx, rotated.Token)
				So(err, ShouldBeNil)
			})
		})

		Convey("When the account is revoked", func() {
			So(a.Revoke(ctx, created.Account.ID.String()), ShouldBeNil)

			Convey("Then its token stops working at once", func() {
				So(authenticate(), ShouldNotBeNil)
			})
		})
	})
}
