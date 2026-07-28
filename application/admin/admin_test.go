package admin_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/admin"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// fakeStore is an in-memory admin.Store for exercising the interactor.
type fakeStore struct {
	tenants  map[string]admin.Tenant
	accounts map[string]admin.ServiceAccount
	hashes   map[string]string
	roles    map[string]admin.Role
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		tenants:  map[string]admin.Tenant{},
		accounts: map[string]admin.ServiceAccount{},
		hashes:   map[string]string{},
		roles:    map[string]admin.Role{},
	}
}

func (f *fakeStore) CreateTenant(_ context.Context, t admin.Tenant) error {
	f.tenants[t.Name] = t
	return nil
}

func (f *fakeStore) ListTenants(context.Context) ([]admin.Tenant, error) {
	out := make([]admin.Tenant, 0, len(f.tenants))
	for _, t := range f.tenants {
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeStore) GetTenantByName(_ context.Context, name string) (admin.Tenant, error) {
	t, ok := f.tenants[name]
	if !ok {
		return admin.Tenant{}, domainerrors.NewNotFound("tenant", name)
	}
	return t, nil
}

func (f *fakeStore) SetTenantActive(_ context.Context, name string, active bool, now time.Time) error {
	t := f.tenants[name]
	t.Active = active
	t.UpdatedAt = now
	f.tenants[name] = t
	return nil
}

func (f *fakeStore) CreateAccount(_ context.Context, a admin.ServiceAccount, secretHash string) error {
	f.accounts[a.ID.String()] = a
	f.hashes[a.ID.String()] = secretHash
	return nil
}

func (f *fakeStore) ListAccounts(_ context.Context, tenant string) ([]admin.ServiceAccount, error) {
	out := []admin.ServiceAccount{}
	for _, a := range f.accounts {
		if a.TenantID == tenant {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeStore) GetAccount(_ context.Context, id ulid.ID) (admin.ServiceAccount, error) {
	a, ok := f.accounts[id.String()]
	if !ok {
		return admin.ServiceAccount{}, domainerrors.NewNotFound("service_account", id.String())
	}
	return a, nil
}

func (f *fakeStore) UpdateSecret(_ context.Context, id ulid.ID, secretHash string, now time.Time) error {
	f.hashes[id.String()] = secretHash
	a := f.accounts[id.String()]
	a.UpdatedAt = now
	f.accounts[id.String()] = a
	return nil
}

func (f *fakeStore) SetAccountActive(_ context.Context, id ulid.ID, active bool, now time.Time) error {
	a := f.accounts[id.String()]
	a.Active = active
	a.UpdatedAt = now
	f.accounts[id.String()] = a
	return nil
}

func (f *fakeStore) SetAccountRoles(_ context.Context, id ulid.ID, roles []string, perms map[string]string, now time.Time) error {
	a, ok := f.accounts[id.String()]
	if !ok {
		return domainerrors.NewNotFound("service_account", id.String())
	}
	a.Roles, a.FieldPermissions, a.UpdatedAt = roles, perms, now
	f.accounts[id.String()] = a
	return nil
}

func (f *fakeStore) UpsertRole(_ context.Context, r admin.Role) (admin.Role, error) {
	key := r.TenantID + "/" + r.Name
	// Mirror the SQL upsert: an update keeps the existing id and created_at.
	if prev, ok := f.roles[key]; ok {
		r.ID, r.CreatedAt = prev.ID, prev.CreatedAt
	}
	f.roles[key] = r
	return r, nil
}

func (f *fakeStore) ListRoles(_ context.Context, tenant string) ([]admin.Role, error) {
	out := []admin.Role{}
	for _, r := range f.roles {
		if r.TenantID == tenant {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteRole(_ context.Context, tenant, name string) error {
	key := tenant + "/" + name
	if _, ok := f.roles[key]; !ok {
		return domainerrors.NewNotFound("role", name)
	}
	delete(f.roles, key)
	return nil
}

func TestProvisioning(t *testing.T) {
	Convey("Given an admin interactor over an empty store", t, func() {
		store := newFakeStore()
		it := admin.NewInteractor(store)
		ctx := context.Background()

		Convey("When creating a tenant with a valid name", func() {
			tenant, err := it.CreateTenant(ctx, "acme")

			Convey("Then it is provisioned and active", func() {
				So(err, ShouldBeNil)
				So(tenant.Name, ShouldEqual, "acme")
				So(tenant.Active, ShouldBeTrue)
			})

			Convey("And creating a tenant with the same name conflicts", func() {
				_, err := it.CreateTenant(ctx, "acme")
				So(domainerrors.IsConflict(err), ShouldBeTrue)
			})
		})

		Convey("When creating a tenant with an invalid name", func() {
			_, err := it.CreateTenant(ctx, "Bad Name!")
			Convey("Then validation fails", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("When creating a service account under an existing tenant", func() {
			_, _ = it.CreateTenant(ctx, "acme")
			out, err := it.CreateAccount(ctx, admin.CreateAccountInput{
				TenantName: "acme", Name: "importer", Scopes: []string{"read", "write"},
			})

			Convey("Then a one-time token is returned that authenticates the account", func() {
				So(err, ShouldBeNil)
				So(out.Token, ShouldStartWith, serviceaccount.TokenPrefix)
				So(out.Account.TenantID, ShouldEqual, "acme")
				So(out.Account.Active, ShouldBeTrue)

				id, secret, splitErr := serviceaccount.SplitToken(out.Token)
				So(splitErr, ShouldBeNil)
				So(id, ShouldEqual, out.Account.ID.String())
				So(serviceaccount.VerifySecret(secret, store.hashes[id]), ShouldBeNil)
			})

			Convey("And the secret is never stored in plaintext", func() {
				So(store.hashes[out.Account.ID.String()], ShouldNotContainSubstring, out.Token)
				So(store.hashes[out.Account.ID.String()], ShouldHaveLength, 64) // hex sha-256
			})
		})

		Convey("When creating an account under a missing tenant", func() {
			_, err := it.CreateAccount(ctx, admin.CreateAccountInput{
				TenantName: "ghost", Name: "importer", Scopes: []string{"read"},
			})
			Convey("Then it is not found", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When creating an account with an unknown scope", func() {
			_, _ = it.CreateTenant(ctx, "acme")
			_, err := it.CreateAccount(ctx, admin.CreateAccountInput{
				TenantName: "acme", Name: "importer", Scopes: []string{"superuser"},
			})
			Convey("Then validation fails", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("When creating an account with no scopes", func() {
			_, _ = it.CreateTenant(ctx, "acme")
			_, err := it.CreateAccount(ctx, admin.CreateAccountInput{
				TenantName: "acme", Name: "importer",
			})
			Convey("Then validation fails", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("Given a provisioned account", func() {
			_, _ = it.CreateTenant(ctx, "acme")
			created, _ := it.CreateAccount(ctx, admin.CreateAccountInput{
				TenantName: "acme", Name: "importer", Scopes: []string{"read", "write"},
			})
			id := created.Account.ID.String()

			Convey("When its secret is rotated", func() {
				rotated, err := it.RotateSecret(ctx, id)

				Convey("Then a new token verifies and the old hash is replaced", func() {
					So(err, ShouldBeNil)
					So(rotated.Token, ShouldNotEqual, created.Token)
					_, newSecret, _ := serviceaccount.SplitToken(rotated.Token)
					So(serviceaccount.VerifySecret(newSecret, store.hashes[id]), ShouldBeNil)
					_, oldSecret, _ := serviceaccount.SplitToken(created.Token)
					So(serviceaccount.VerifySecret(oldSecret, store.hashes[id]), ShouldNotBeNil)
				})
			})

			Convey("When it is revoked", func() {
				err := it.Revoke(ctx, id)
				Convey("Then the account is marked inactive", func() {
					So(err, ShouldBeNil)
					So(store.accounts[id].Active, ShouldBeFalse)
				})
			})

			Convey("When rotating a malformed id", func() {
				_, err := it.RotateSecret(ctx, "not-a-ulid")
				Convey("Then validation fails", func() {
					So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				})
			})
		})
	})
}

func TestRoles(t *testing.T) {
	Convey("Given an admin interactor with a provisioned tenant", t, func() {
		store := newFakeStore()
		it := admin.NewInteractor(store)
		ctx := context.Background()
		_, err := it.CreateTenant(ctx, "acme")
		So(err, ShouldBeNil)

		Convey("When a role is created", func() {
			role, err := it.UpsertRole(ctx, admin.UpsertRoleInput{
				TenantName:       "acme",
				Name:             "reader",
				Description:      "read-only",
				Scopes:           []string{"read"},
				FieldPermissions: map[string]string{"salary": "read"},
			})

			Convey("Then it is stored under the tenant with its permission set", func() {
				So(err, ShouldBeNil)
				So(role.Name, ShouldEqual, "reader")
				So(role.TenantID, ShouldEqual, "acme")
				So(role.Scopes, ShouldResemble, []serviceaccount.Scope{serviceaccount.ScopeRead})
				So(role.FieldPermissions["salary"], ShouldEqual, "read")

				listed, lerr := it.ListRoles(ctx, "acme")
				So(lerr, ShouldBeNil)
				So(listed, ShouldHaveLength, 1)
			})
		})

		Convey("When a role names an unknown tenant", func() {
			_, err := it.UpsertRole(ctx, admin.UpsertRoleInput{
				TenantName: "ghost", Name: "reader",
			})

			Convey("Then it is refused as not found", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When a role carries field permissions and no scope", func() {
			role, err := it.UpsertRole(ctx, admin.UpsertRoleInput{
				TenantName: "acme", Name: "salary-blind",
				FieldPermissions: map[string]string{"salary": "none"},
			})

			Convey("Then it is accepted: a role may restrict without granting a scope", func() {
				So(err, ShouldBeNil)
				So(role.Scopes, ShouldBeEmpty)
			})
		})

		Convey("When a role name breaks the naming rule", func() {
			_, err := it.UpsertRole(ctx, admin.UpsertRoleInput{
				TenantName: "acme", Name: "Bad Name",
			})

			Convey("Then it is refused as a validation error", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a field permission uses a level that does not exist", func() {
			_, err := it.UpsertRole(ctx, admin.UpsertRoleInput{
				TenantName: "acme", Name: "reader",
				FieldPermissions: map[string]string{"salary": "readonly"},
			})

			Convey("Then it is refused rather than silently denying the field", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "none, read or write")
			})
		})

		Convey("And a reader role that exists", func() {
			_, err := it.UpsertRole(ctx, admin.UpsertRoleInput{
				TenantName: "acme", Name: "reader", Scopes: []string{"read"},
			})
			So(err, ShouldBeNil)

			Convey("When an account is created holding it and no scope of its own", func() {
				out, err := it.CreateAccount(ctx, admin.CreateAccountInput{
					TenantName: "acme", Name: "analyst", Roles: []string{"reader"},
				})

				Convey("Then it is accepted and records the role name", func() {
					So(err, ShouldBeNil)
					So(out.Account.Roles, ShouldResemble, []string{"reader"})
					So(out.Account.Scopes, ShouldBeEmpty)
				})
			})

			Convey("When an account is created with neither a scope nor a role", func() {
				_, err := it.CreateAccount(ctx, admin.CreateAccountInput{
					TenantName: "acme", Name: "idle",
				})

				Convey("Then it is refused: the credential would grant nothing", func() {
					So(domainerrors.IsValidation(err), ShouldBeTrue)
					So(err.Error(), ShouldContainSubstring, "at least one scope or role")
				})
			})

			Convey("When an account is created holding a role that does not exist", func() {
				_, err := it.CreateAccount(ctx, admin.CreateAccountInput{
					TenantName: "acme", Name: "analyst", Roles: []string{"raeder"},
				})

				Convey("Then the typo is refused rather than granting nothing silently", func() {
					So(domainerrors.IsNotFound(err), ShouldBeTrue)
					So(err.Error(), ShouldContainSubstring, "raeder")
				})
			})

			Convey("And an account with no roles", func() {
				out, err := it.CreateAccount(ctx, admin.CreateAccountInput{
					TenantName: "acme", Name: "analyst", Scopes: []string{"read"},
				})
				So(err, ShouldBeNil)

				Convey("When roles are assigned to it", func() {
					err := it.AssignRoles(ctx, admin.AssignRolesInput{
						AccountID:        out.Account.ID.String(),
						Roles:            []string{"reader"},
						FieldPermissions: map[string]string{"salary": "none"},
					})

					Convey("Then both lists are replaced on the account", func() {
						So(err, ShouldBeNil)
						stored, gerr := store.GetAccount(ctx, out.Account.ID)
						So(gerr, ShouldBeNil)
						So(stored.Roles, ShouldResemble, []string{"reader"})
						So(stored.FieldPermissions["salary"], ShouldEqual, "none")
					})
				})

				Convey("When an unknown role is assigned", func() {
					err := it.AssignRoles(ctx, admin.AssignRolesInput{
						AccountID: out.Account.ID.String(), Roles: []string{"ghost"},
					})

					Convey("Then the assignment is refused", func() {
						So(domainerrors.IsNotFound(err), ShouldBeTrue)
					})
				})

				Convey("When roles are assigned with a malformed account id", func() {
					err := it.AssignRoles(ctx, admin.AssignRolesInput{AccountID: "not-a-ulid"})

					Convey("Then it is refused as a validation error", func() {
						So(domainerrors.IsValidation(err), ShouldBeTrue)
					})
				})
			})

			Convey("When the role is deleted", func() {
				err := it.DeleteRole(ctx, "acme", "reader")

				Convey("Then it is gone from the tenant's roles", func() {
					So(err, ShouldBeNil)
					listed, lerr := it.ListRoles(ctx, "acme")
					So(lerr, ShouldBeNil)
					So(listed, ShouldBeEmpty)
				})
			})

			Convey("When a role that does not exist is deleted", func() {
				err := it.DeleteRole(ctx, "acme", "ghost")

				Convey("Then a not-found error is returned", func() {
					So(domainerrors.IsNotFound(err), ShouldBeTrue)
				})
			})
		})
	})
}

func TestFieldPermissionRanking(t *testing.T) {
	Convey("Given the three field-permission levels", t, func() {
		Convey("When they are ordered", func() {
			Convey("Then write outranks read, and read outranks none", func() {
				So(serviceaccount.MorePermissive("write", "read"), ShouldBeTrue)
				So(serviceaccount.MorePermissive("read", "none"), ShouldBeTrue)
				So(serviceaccount.MorePermissive("read", "write"), ShouldBeFalse)
				So(serviceaccount.MorePermissive("none", "none"), ShouldBeFalse)
			})
		})

		Convey("When an unrecognised level is compared", func() {
			Convey("Then it ranks below every real level, so a typo denies", func() {
				So(serviceaccount.MorePermissive("readonly", "none"), ShouldBeFalse)
				So(serviceaccount.MorePermissive("none", "readonly"), ShouldBeTrue)
			})
		})

		Convey("When an absent level is compared with a real one", func() {
			Convey("Then the real level wins, so an unlisted role adds nothing", func() {
				So(serviceaccount.MorePermissive("read", ""), ShouldBeTrue)
				So(serviceaccount.MorePermissive("", "read"), ShouldBeFalse)
			})
		})
	})
}

func (f *fakeStore) CountAccountsWithRole(_ context.Context, tenant, name string) (int, error) {
	n := 0
	for _, a := range f.accounts {
		if a.TenantID != tenant {
			continue
		}
		for _, held := range a.Roles {
			if held == name {
				n++
			}
		}
	}
	return n, nil
}

// fakeInvalidator records the evictions the interactor asks for.
type fakeInvalidator struct {
	accounts []string
	tenants  []string
}

func (f *fakeInvalidator) Invalidate(accountID string)      { f.accounts = append(f.accounts, accountID) }
func (f *fakeInvalidator) InvalidateTenant(tenantID string) { f.tenants = append(f.tenants, tenantID) }

func TestRoleSafety(t *testing.T) {
	Convey("Given an admin interactor with a tenant and an auth cache", t, func() {
		store := newFakeStore()
		cache := &fakeInvalidator{}
		it := admin.NewInteractor(store, admin.WithAuthCache(cache))
		ctx := context.Background()
		_, err := it.CreateTenant(ctx, "acme")
		So(err, ShouldBeNil)

		Convey("When a role tries to grant the admin scope", func() {
			_, err := it.UpsertRole(ctx, admin.UpsertRoleInput{
				TenantName: "acme", Name: "ops", Scopes: []string{"admin"},
			})

			Convey("Then it is refused: admin would void the account's own field permissions", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "may not grant the admin scope")
			})
		})

		Convey("And an existing role", func() {
			created, err := it.UpsertRole(ctx, admin.UpsertRoleInput{
				TenantName: "acme", Name: "reader", Scopes: []string{"read"},
				FieldPermissions: map[string]string{"salary": "none"},
			})
			So(err, ShouldBeNil)

			Convey("When it is upserted again", func() {
				again, err := it.UpsertRole(ctx, admin.UpsertRoleInput{
					TenantName: "acme", Name: "reader", Scopes: []string{"read", "write"},
				})

				Convey("Then the response describes the stored row, not a fresh one", func() {
					So(err, ShouldBeNil)
					So(again.ID, ShouldEqual, created.ID)
					So(again.CreatedAt, ShouldEqual, created.CreatedAt)
				})
			})

			Convey("When a role is written", func() {
				Convey("Then the tenant's cached authentications are dropped", func() {
					So(cache.tenants, ShouldContain, "acme")
				})
			})

			Convey("And an account holding it", func() {
				out, err := it.CreateAccount(ctx, admin.CreateAccountInput{
					TenantName: "acme", Name: "analyst", Roles: []string{"reader"},
				})
				So(err, ShouldBeNil)

				Convey("When the role is deleted", func() {
					err := it.DeleteRole(ctx, "acme", "reader")

					Convey("Then it is refused: the account would gain unrestricted access", func() {
						So(domainerrors.IsConflict(err), ShouldBeTrue)
						So(err.Error(), ShouldContainSubstring, "still assigned")
					})

					Convey("Then the role survives, so the restriction stays in force", func() {
						roles, lerr := it.ListRoles(ctx, "acme")
						So(lerr, ShouldBeNil)
						So(roles, ShouldHaveLength, 1)
					})
				})

				Convey("When the account is reassigned first and the role is then deleted", func() {
					So(it.AssignRoles(ctx, admin.AssignRolesInput{
						AccountID: out.Account.ID.String(),
					}), ShouldBeNil)
					err := it.DeleteRole(ctx, "acme", "reader")

					Convey("Then the delete succeeds and evicts the tenant's cache", func() {
						So(err, ShouldBeNil)
						So(cache.tenants, ShouldContain, "acme")
					})
				})

				Convey("When the account's effective permissions are read", func() {
					view, err := it.EffectiveAccount(ctx, out.Account.ID.String())

					Convey("Then the role's grants are resolved, not just named", func() {
						So(err, ShouldBeNil)
						So(view.Roles, ShouldResemble, []string{"reader"})
						So(view.Scopes, ShouldContain, serviceaccount.ScopeRead)
						So(view.FieldPermissions["salary"], ShouldEqual, "none")
						So(view.UnresolvedRoles, ShouldBeEmpty)
					})
				})
			})
		})

		Convey("When a tenant is deactivated", func() {
			cache.tenants = nil
			err := it.SetTenantActive(ctx, "acme", false)

			Convey("Then its cached authentications are dropped, so the suspension is immediate", func() {
				So(err, ShouldBeNil)
				So(cache.tenants, ShouldResemble, []string{"acme"})
			})
		})

		Convey("When an account's effective permissions are read with a bad id", func() {
			_, err := it.EffectiveAccount(ctx, "not-a-ulid")

			Convey("Then it is a validation error", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})
	})
}
