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
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		tenants:  map[string]admin.Tenant{},
		accounts: map[string]admin.ServiceAccount{},
		hashes:   map[string]string{},
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
