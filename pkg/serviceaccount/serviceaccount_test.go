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

// TestResolveMergeRules pins the role merge that both authentication and the
// effective-permissions view run through.
func TestResolveMergeRules(t *testing.T) {
	Convey("Given an account holding two roles", t, func() {
		base := Account{
			ID: "a1", TenantID: "acme",
			Scopes:           []Scope{ScopeRead},
			FieldPermissions: map[string]string{"salary": "none"},
		}
		grants := []RoleGrant{
			{Name: "reader", Scopes: []Scope{ScopeRead},
				FieldPermissions: map[string]string{"salary": "read", "ssn": "none"}},
			{Name: "editor", Scopes: []Scope{ScopeWrite},
				FieldPermissions: map[string]string{"salary": "write"}},
		}

		Convey("When the roles are merged in", func() {
			got := Resolve(base, []string{"reader", "editor"}, grants)

			Convey("Then the scopes are the union of its own and both roles'", func() {
				So(got.HasScope(ScopeRead), ShouldBeTrue)
				So(got.HasScope(ScopeWrite), ShouldBeTrue)
			})

			Convey("Then the account's own entry beats every role", func() {
				So(got.FieldPermissions["salary"], ShouldEqual, "none")
			})

			Convey("Then a permission only one role grants still applies", func() {
				So(got.FieldPermissions["ssn"], ShouldEqual, "none")
			})

			Convey("Then nothing is unresolved", func() {
				So(got.UnresolvedRoles, ShouldBeEmpty)
			})
		})
	})

	Convey("Given a role whose stored row carries the admin scope", t, func() {
		// UpsertRole refuses admin, so this row can only come from a direct
		// database edit or from before that rule. Dropping it here means the
		// escalation cannot be reached either way.
		base := Account{ID: "a1", TenantID: "acme", Scopes: []Scope{ScopeRead},
			FieldPermissions: map[string]string{"salary": "none"}}
		grants := []RoleGrant{{Name: "ops", Scopes: []Scope{ScopeAdmin, ScopeWrite}}}

		Convey("When it is merged in", func() {
			got := Resolve(base, []string{"ops"}, grants)

			Convey("Then admin is not conferred, so the field ACL is not voided", func() {
				So(got.HasScope(ScopeWrite), ShouldBeTrue)
				held := false
				for _, sc := range got.Scopes {
					if sc == ScopeAdmin {
						held = true
					}
				}
				So(held, ShouldBeFalse)
				So(got.FieldPermissions["salary"], ShouldEqual, "none")
			})
		})
	})

	Convey("Given an account naming a role that no longer exists", t, func() {
		base := Account{ID: "a1", TenantID: "acme", Scopes: []Scope{ScopeRead}}

		Convey("When it is resolved", func() {
			got := Resolve(base, []string{"ghost"}, nil)

			Convey("Then the name is reported unresolved rather than ignored", func() {
				So(got.UnresolvedRoles, ShouldResemble, []string{"ghost"})
				So(got.FieldPermissions, ShouldBeEmpty)
			})
		})
	})

	Convey("Given an account with a role that grants only a scope", t, func() {
		base := Account{ID: "a1", TenantID: "acme"}
		grants := []RoleGrant{{Name: "reader", Scopes: []Scope{ScopeRead}}}

		Convey("When it is resolved", func() {
			got := Resolve(base, []string{"reader"}, grants)

			Convey("Then it stays unrestricted: no policy source means no restriction", func() {
				So(got.HasScope(ScopeRead), ShouldBeTrue)
				So(got.FieldPermissions, ShouldBeEmpty)
				So(got.UnresolvedRoles, ShouldBeEmpty)
			})
		})
	})
}
