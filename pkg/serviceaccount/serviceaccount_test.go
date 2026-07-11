package serviceaccount

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestServiceAccounts(t *testing.T) {
	Convey("Given a store with one provisioned account", t, func() {
		secret := "super-secret-value"
		store := NewStore([]Account{{
			ID:         "ci",
			Name:       "CI Importer",
			TenantID:   "acme",
			Scopes:     []Scope{ScopeRead, ScopeWrite},
			SecretHash: HashSecret(secret),
		}})

		Convey("When the minted token is presented", func() {
			account, err := store.Authenticate(MintToken("ci", secret))

			Convey("Then authentication succeeds with the account's identity", func() {
				So(err, ShouldBeNil)
				So(account.Name, ShouldEqual, "CI Importer")
				So(account.Tenant().String(), ShouldEqual, "acme")
			})
		})

		Convey("When the secret is wrong", func() {
			_, err := store.Authenticate(MintToken("ci", "guess"))

			Convey("Then authentication fails", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When the account is unknown", func() {
			_, err := store.Authenticate(MintToken("ghost", secret))

			Convey("Then authentication fails", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When malformed tokens are presented", func() {
			for _, token := range []string{"", "bearer", "ft_", "ft_ci", "wrong_ci_secret"} {
				_, err := store.Authenticate(token)
				So(err, ShouldNotBeNil)
			}
		})

		Convey("When scopes are checked", func() {
			account, err := store.Authenticate(MintToken("ci", secret))
			So(err, ShouldBeNil)

			admin := Account{Scopes: []Scope{ScopeAdmin}}

			Convey("Then granted scopes pass and admin implies everything", func() {
				So(account.HasScope(ScopeRead), ShouldBeTrue)
				So(account.HasScope(ScopeWrite), ShouldBeTrue)
				So(account.HasScope(ScopeAdmin), ShouldBeFalse)
				So(admin.HasScope(ScopeRead), ShouldBeTrue)
				So(admin.HasScope(ScopeWrite), ShouldBeTrue)
			})
		})
	})
}
