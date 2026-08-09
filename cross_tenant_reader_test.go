package flexitype_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appadmin "github.com/zkrebbekx/flexitype/application/admin"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
)

// TestCrossTenantReader covers #549.
//
// A tenant comes from the token, so there is no request that reads two
// tenants. That is a property, not a gap — but it makes a cross-tenant READ
// MODEL (a marketplace storefront, a group-wide search index, a billing
// rollup) hold one full read/write credential PER TENANT, purely to re-read
// entities. Concentrating every tenant's credential in one service is a far
// larger exposure than the read it needs.
//
// `read_any_tenant` is the narrow answer: read anything, write nothing. These
// cases pin both halves — that it can read across tenants, and that it cannot
// be turned into anything more.
func TestCrossTenantReader(t *testing.T) {
	Convey("Given two tenants, each with its own product", t, func() {
		svc := flexitype.NewInMemory()

		accounts := []serviceaccount.Account{
			readerAccount("acct-a", "merchant-a", "secret-a", serviceaccount.ScopeRead, serviceaccount.ScopeWrite),
			readerAccount("acct-b", "merchant-b", "secret-b", serviceaccount.ScopeRead, serviceaccount.ScopeWrite),
			// The read model. One credential, no tenant of its own worth
			// naming, and no ability to write.
			readerAccount("acct-r", "reader-home", "secret-r", serviceaccount.ScopeReadAnyTenant),
		}
		handler := svc.APIHandler(flexitype.APIConfig{
			Accounts: serviceaccount.NewStore(accounts),
		})

		typeA := seedTenant(svc, "merchant-a", "Jacket A")
		typeB := seedTenant(svc, "merchant-b", "Kettle B")

		read := func(token, tenant, path string) (int, string) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			if tenant != "" {
				req.Header.Set(serviceaccount.TenantHeader, tenant)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			return rec.Code, rec.Body.String()
		}

		readerToken := serviceaccount.MintToken("acct-r", "secret-r")
		merchantAToken := serviceaccount.MintToken("acct-a", "secret-a")

		Convey("When the read model names one tenant", func() {
			status, body := read(readerToken, "merchant-a", "/api/v1/entities/"+typeA+"/p-1/values")

			Convey("Then it reads that tenant's data", func() {
				So(status, ShouldEqual, http.StatusOK)
				So(body, ShouldContainSubstring, "Jacket A")
			})
		})

		Convey("When the SAME credential names the other tenant", func() {
			status, body := read(readerToken, "merchant-b", "/api/v1/entities/"+typeB+"/p-1/values")

			Convey("Then it reads that one too: one credential, every tenant", func() {
				So(status, ShouldEqual, http.StatusOK)
				So(body, ShouldContainSubstring, "Kettle B")
			})
		})

		Convey("When the read model tries to WRITE", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/type-definitions",
				strings.NewReader(`{"internal_name":"sneaky","display_name":"Sneaky"}`))
			req.Header.Set("Authorization", "Bearer "+readerToken)
			req.Header.Set(serviceaccount.TenantHeader, "merchant-a")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			Convey("Then it is refused, whatever tenant it named", func() {
				So(rec.Code, ShouldEqual, http.StatusForbidden)
				So(rec.Body.String(), ShouldContainSubstring, "may only read")
			})
		})

		Convey("When the read model tries to PURGE, which is the irreversible one", func() {
			req := httptest.NewRequest(http.MethodPost,
				"/api/v1/entities/"+typeA+"/p-1/purge", nil)
			req.Header.Set("Authorization", "Bearer "+readerToken)
			req.Header.Set(serviceaccount.TenantHeader, "merchant-a")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			Convey("Then it is refused before any handler runs", func() {
				So(rec.Code, ShouldEqual, http.StatusForbidden)
			})
		})

		Convey("When an ORDINARY merchant credential sends the tenant header", func() {
			// The header must never widen a normal credential. If it did, every
			// merchant token in the system would become a cross-tenant one.
			status, body := read(merchantAToken, "merchant-b", "/api/v1/entities/"+typeB+"/p-1/values")

			Convey("Then it is ignored, and the token's own tenant is read", func() {
				So(status, ShouldEqual, http.StatusOK)
				So(body, ShouldNotContainSubstring, "Kettle B")
			})
		})

		Convey("When the read model names no tenant", func() {
			status, _ := read(readerToken, "", "/api/v1/type-definitions")

			Convey("Then it reads its own tenant, like any other account", func() {
				So(status, ShouldEqual, http.StatusOK)
			})
		})

		Convey("When the read model names a tenant that cannot be parsed", func() {
			status, _ := read(readerToken, "not a tenant!", "/api/v1/type-definitions")

			Convey("Then the request is refused rather than silently falling back", func() {
				// Falling back to its own tenant would answer a question the
				// caller did not ask, with data from the wrong tenant.
				So(status, ShouldEqual, http.StatusUnprocessableEntity)
			})
		})
	})
}

// TestCrossTenantReaderCannotAlsoWrite pins the creation-time half.
//
// A credential that reads every tenant AND writes would be strictly worse than
// the per-tenant credentials it replaces: one leak would mutate every tenant
// instead of reading them.
func TestCrossTenantReaderCannotAlsoWrite(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	Convey("Given a provisioning-backed service", t, func() {
		svc := flexitype.New(pool)
		ctx := context.Background()
		So(svc.Migrate(ctx), ShouldBeNil)
		truncateAll(t, pool)
		admin := svc.AdminInteractor()
		_, err := admin.CreateTenant(ctx, "reader-tenant")
		So(err, ShouldBeNil)

		Convey("When an account is created with read_any_tenant and write", func() {
			_, err := admin.CreateAccount(ctx, appadmin.CreateAccountInput{
				TenantName: "reader-tenant", Name: "bad-reader",
				Scopes: []string{"read_any_tenant", "write"},
			})

			Convey("Then it is refused, naming the scope that conflicts", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "must write none")
			})
		})

		Convey("When an account is created with read_any_tenant and admin", func() {
			_, err := admin.CreateAccount(ctx, appadmin.CreateAccountInput{
				TenantName: "reader-tenant", Name: "bad-admin",
				Scopes: []string{"read_any_tenant", "admin"},
			})

			Convey("Then it is refused too: admin writes", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When an account is created with read_any_tenant alone", func() {
			out, err := admin.CreateAccount(ctx, appadmin.CreateAccountInput{
				TenantName: "reader-tenant", Name: "good-reader",
				Scopes: []string{"read_any_tenant"},
			})

			Convey("Then it is created", func() {
				So(err, ShouldBeNil)
				So(out.Token, ShouldNotBeEmpty)
			})
		})
	})
}

// readerAccount builds a static service account for the in-memory account store.
func readerAccount(id, tenant, secret string, scopes ...serviceaccount.Scope) serviceaccount.Account {
	return serviceaccount.Account{
		ID: id, Name: id, TenantID: tenant, Scopes: scopes,
		SecretHash: serviceaccount.HashSecret(secret),
	}
}

// seedTenant creates a product type with one named entity in a tenant, and
// returns the type definition id.
func seedTenant(svc *flexitype.Service, tenant, name string) string {
	ctx := uow.WithTenant(context.Background(), valueobjects.TenantID(tenant))
	product, err := svc.Interactors(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "product", DisplayName: "Product",
	})
	So(err, ShouldBeNil)
	attr, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
		TypeDefinitionID: product.ID.String(), InternalName: "name",
		DisplayName: "Name", DataType: "string",
	})
	So(err, ShouldBeNil)
	raw, merr := json.Marshal(name)
	So(merr, ShouldBeNil)
	_, err = svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
		AttributeDefinitionID: attr.ID.String(), EntityID: "p-1",
		TypeDefinitionID: product.ID.String(), Value: raw,
	})
	So(err, ShouldBeNil)
	return product.ID.String()
}
