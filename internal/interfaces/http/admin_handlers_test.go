package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/admin"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/health"
	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// Provisioning is database-backed, so Service.AdminInteractor() is nil for an
// in-memory service and the root-package suite can only reach the 501 branch.
// These in-package tests give the router a real admin.Interactor over the
// in-test store below, so the provisioning handlers' success paths — and the
// admin-scope gate in front of them — are exercised for real.

// adminStore is an in-memory admin.Store.
type adminStore struct {
	mu       sync.Mutex
	tenants  map[string]admin.Tenant
	accounts map[ulid.ID]admin.ServiceAccount
	hashes   map[ulid.ID]string
	roles    map[string]admin.Role
}

func newAdminStore() *adminStore {
	return &adminStore{
		tenants:  map[string]admin.Tenant{},
		accounts: map[ulid.ID]admin.ServiceAccount{},
		hashes:   map[ulid.ID]string{},
		roles:    map[string]admin.Role{},
	}
}

func (s *adminStore) CreateTenant(_ context.Context, t admin.Tenant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenants[t.Name] = t
	return nil
}

func (s *adminStore) ListTenants(context.Context) ([]admin.Tenant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]admin.Tenant, 0, len(s.tenants))
	for _, t := range s.tenants {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *adminStore) GetTenantByName(_ context.Context, name string) (admin.Tenant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[name]
	if !ok {
		return admin.Tenant{}, domainerrors.NewNotFound("tenant", name)
	}
	return t, nil
}

func (s *adminStore) SetTenantActive(_ context.Context, name string, active bool, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[name]
	if !ok {
		return domainerrors.NewNotFound("tenant", name)
	}
	t.Active, t.UpdatedAt = active, now
	s.tenants[name] = t
	return nil
}

func (s *adminStore) CreateAccount(_ context.Context, a admin.ServiceAccount, secretHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[a.ID] = a
	s.hashes[a.ID] = secretHash
	return nil
}

func (s *adminStore) ListAccounts(_ context.Context, tenant string) ([]admin.ServiceAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []admin.ServiceAccount
	for _, a := range s.accounts {
		if a.TenantID == tenant {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *adminStore) GetAccount(_ context.Context, id ulid.ID) (admin.ServiceAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	if !ok {
		return admin.ServiceAccount{}, domainerrors.NewNotFound("service_account", id.String())
	}
	return a, nil
}

func (s *adminStore) UpdateSecret(_ context.Context, id ulid.ID, secretHash string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	if !ok {
		return domainerrors.NewNotFound("service_account", id.String())
	}
	a.UpdatedAt = now
	s.accounts[id] = a
	s.hashes[id] = secretHash
	return nil
}

func (s *adminStore) SetAccountActive(_ context.Context, id ulid.ID, active bool, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	if !ok {
		return domainerrors.NewNotFound("service_account", id.String())
	}
	a.Active, a.UpdatedAt = active, now
	s.accounts[id] = a
	return nil
}

// hashOf exposes the stored secret hash so a rotation can be proven to have
// actually replaced it.
func (s *adminStore) hashOf(id ulid.ID) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hashes[id]
}

// newProvisioningHarness builds the router with a live provisioning interactor
// and an authenticator holding the given accounts.
func newProvisioningHarness(t *testing.T, accounts serviceaccount.Authenticator) (*deliveryHarness, *adminStore) {
	t.Helper()
	store := memory.NewStore()
	provisioning := newAdminStore()

	factory := application.NewFactory(application.FactoryConfig{
		Transactor:      store.Transactor(),
		NewRepositories: func() application.Repositories { return store.Repositories() },
		ActivityLog:     store.ActivityLog(),
	})

	srv := httptest.NewServer(buildRouter(ServerConfig{
		Factory:  factory,
		Logger:   logger.New(logger.Config{Level: "error"}),
		Health:   health.NewService("flexitype", "test"),
		Accounts: accounts,
		Admin:    admin.NewInteractor(provisioning),
	}))
	t.Cleanup(srv.Close)

	return &deliveryHarness{t: t, srv: srv}, provisioning
}

// TestProvisioningRoutes covers the tenant and service-account control plane
// with a live admin interactor behind it: creation, listing, activation,
// secret rotation and revocation — plus the once-only token contract.
func TestProvisioningRoutes(t *testing.T) {
	Convey("Given a provisioning-enabled API in development mode (implicit admin)", t, func() {
		h, store := newProvisioningHarness(t, nil)

		Convey("When a tenant is created", func() {
			resp := h.post("/api/v1/tenants", map[string]any{"name": "acme"})

			Convey("Then it is 201 and starts active", func() {
				So(resp.Status, ShouldEqual, http.StatusCreated)
				obj := resp.object(t)
				So(obj["name"], ShouldEqual, "acme")
				So(obj["active"], ShouldBeTrue)
			})

			Convey("And it appears in the tenant list", func() {
				list := h.get("/api/v1/tenants")
				So(list.Status, ShouldEqual, http.StatusOK)
				items := list.object(t)["items"].([]any)
				So(len(items), ShouldEqual, 1)
				So(items[0].(map[string]any)["name"], ShouldEqual, "acme")
			})

			Convey("And creating it twice conflicts", func() {
				dup := h.post("/api/v1/tenants", map[string]any{"name": "acme"})
				So(dup.Status, ShouldEqual, http.StatusConflict)
				So(dup.errorCode(), ShouldEqual, "CONFLICT")
			})

			Convey("And it can be deactivated and reactivated", func() {
				off := h.patch("/api/v1/tenants/acme", map[string]any{"active": false})
				So(off.Status, ShouldEqual, http.StatusOK)
				So(off.object(t)["active"], ShouldBeFalse)
				So(off.object(t)["name"], ShouldEqual, "acme")

				on := h.patch("/api/v1/tenants/acme", map[string]any{"active": true})
				So(on.Status, ShouldEqual, http.StatusOK)
				So(on.object(t)["active"], ShouldBeTrue)
			})
		})

		Convey("When the tenant name is not a valid slug", func() {
			resp := h.post("/api/v1/tenants", map[string]any{"name": "Acme Corp"})

			Convey("Then it is 422 VALIDATION", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When an unknown tenant's active flag is set", func() {
			resp := h.patch("/api/v1/tenants/nosuchtenant", map[string]any{"active": false})

			Convey("Then it is 404", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
				So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
			})
		})

		Convey("When provisioning bodies are malformed", func() {
			Convey("Then create-tenant and set-active are 422 VALIDATION", func() {
				So(h.post("/api/v1/tenants", `{`).Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(h.patch("/api/v1/tenants/acme", `{`).Status, ShouldEqual, http.StatusUnprocessableEntity)
			})
		})

		Convey("When the empty tenant list is read", func() {
			resp := h.get("/api/v1/tenants")

			Convey("Then it is an empty array, not null", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(string(resp.Body), ShouldContainSubstring, `"items":[]`)
			})
		})

		Convey("Given an existing tenant", func() {
			So(h.post("/api/v1/tenants", map[string]any{"name": "acme"}).Status, ShouldEqual, http.StatusCreated)

			Convey("When a service account is provisioned", func() {
				resp := h.post("/api/v1/service-accounts", map[string]any{
					"tenant_name": "acme", "name": "ingest-bot", "scopes": []string{"read", "write"},
				})

				Convey("Then it is 201 and the token is returned exactly once", func() {
					So(resp.Status, ShouldEqual, http.StatusCreated)
					obj := resp.object(t)
					token := obj["token"].(string)
					So(token, ShouldStartWith, serviceaccount.TokenPrefix)

					acct := obj["account"].(map[string]any)
					So(acct["name"], ShouldEqual, "ingest-bot")
					So(acct["tenant_id"], ShouldEqual, "acme")
					So(acct["active"], ShouldBeTrue)
					So(acct["scopes"], ShouldResemble, []any{"read", "write"})
				})

				Convey("Then the account's stored form never carries the secret", func() {
					acct := resp.object(t)["account"].(map[string]any)
					_, leaked := acct["secret_hash"]
					So(leaked, ShouldBeFalse)
				})

				Convey("And the returned token authenticates as that account", func() {
					token := resp.object(t)["token"].(string)
					id, secret, err := serviceaccount.SplitToken(token)
					So(err, ShouldBeNil)
					So(serviceaccount.VerifySecret(secret, store.hashOf(ulid.MustParse(id))), ShouldBeNil)
				})

				Convey("And it appears in the tenant's account list", func() {
					list := h.get("/api/v1/service-accounts?tenant_name=acme")
					So(list.Status, ShouldEqual, http.StatusOK)
					So(len(list.object(t)["items"].([]any)), ShouldEqual, 1)
				})

				Convey("And rotating its secret issues a new token and invalidates the old", func() {
					obj := resp.object(t)
					id := obj["account"].(map[string]any)["id"].(string)
					oldToken := obj["token"].(string)
					oldHash := store.hashOf(ulid.MustParse(id))

					rotated := h.post("/api/v1/service-accounts/"+id+"/rotate", nil)
					So(rotated.Status, ShouldEqual, http.StatusOK)

					newToken := rotated.object(t)["token"].(string)
					So(newToken, ShouldNotEqual, oldToken)
					So(store.hashOf(ulid.MustParse(id)), ShouldNotEqual, oldHash)

					_, oldSecret, err := serviceaccount.SplitToken(oldToken)
					So(err, ShouldBeNil)
					So(serviceaccount.VerifySecret(oldSecret, store.hashOf(ulid.MustParse(id))), ShouldNotBeNil)
				})

				Convey("And revoking it deactivates the account", func() {
					id := resp.object(t)["account"].(map[string]any)["id"].(string)

					revoked := h.delete("/api/v1/service-accounts/" + id)
					So(revoked.Status, ShouldEqual, http.StatusNoContent)

					list := h.get("/api/v1/service-accounts?tenant_name=acme")
					items := list.object(t)["items"].([]any)
					So(len(items), ShouldEqual, 1)
					So(items[0].(map[string]any)["active"], ShouldBeFalse)
				})
			})

			Convey("When the account list is requested without a tenant", func() {
				resp := h.get("/api/v1/service-accounts")

				Convey("Then it is 422 asking for tenant_name", func() {
					So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
					So(resp.errorCode(), ShouldEqual, "VALIDATION")
					So(string(resp.Body), ShouldContainSubstring, "tenant_name is required")
				})
			})

			Convey("When the requested scope is not a known one", func() {
				resp := h.post("/api/v1/service-accounts", map[string]any{
					"tenant_name": "acme", "name": "bot", "scopes": []string{"superuser"},
				})

				Convey("Then it is 422 naming the unknown scope", func() {
					So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
					So(resp.errorCode(), ShouldEqual, "VALIDATION")
					So(string(resp.Body), ShouldContainSubstring, "superuser")
				})
			})

			Convey("When no scope is requested at all", func() {
				resp := h.post("/api/v1/service-accounts", map[string]any{
					"tenant_name": "acme", "name": "bot",
				})

				Convey("Then it is 422 — an account with no scope is useless", func() {
					So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
					So(resp.errorCode(), ShouldEqual, "VALIDATION")
				})
			})

			Convey("When the account is provisioned under an unknown tenant", func() {
				resp := h.post("/api/v1/service-accounts", map[string]any{
					"tenant_name": "nosuchtenant", "name": "bot", "scopes": []string{"read"},
				})

				Convey("Then it is 404", func() {
					So(resp.Status, ShouldEqual, http.StatusNotFound)
					So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
				})
			})

			Convey("When an unknown account is rotated or revoked", func() {
				Convey("Then both are 404", func() {
					So(h.post("/api/v1/service-accounts/"+absentULID+"/rotate", nil).Status,
						ShouldEqual, http.StatusNotFound)
					So(h.delete("/api/v1/service-accounts/"+absentULID).Status,
						ShouldEqual, http.StatusNotFound)
				})
			})

			Convey("When the account id is not a ULID", func() {
				Convey("Then rotate and revoke are 422", func() {
					So(h.post("/api/v1/service-accounts/not-a-ulid/rotate", nil).Status,
						ShouldEqual, http.StatusUnprocessableEntity)
					So(h.delete("/api/v1/service-accounts/not-a-ulid").Status,
						ShouldEqual, http.StatusUnprocessableEntity)
				})
			})

			Convey("When the create-account body is malformed", func() {
				resp := h.post("/api/v1/service-accounts", `{"name":`)

				Convey("Then it is 422 VALIDATION", func() {
					So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
					So(resp.errorCode(), ShouldEqual, "VALIDATION")
				})
			})
		})
	})
}

// TestProvisioningRequiresAdminScope covers the scope gate in front of a
// configured control plane: an ordinary writer is refused 403 on every
// provisioning route, while an admin-scoped caller is let through.
func TestProvisioningRequiresAdminScope(t *testing.T) {
	writerSecret, adminSecret := "writer-secret", "admin-secret"
	writerID, adminID := "01KXWRITER0000000000000000", "01KXADMIN00000000000000000"
	accounts := serviceaccount.NewStore([]serviceaccount.Account{
		{ID: writerID, Name: "writer", TenantID: "acme",
			Scopes:     []serviceaccount.Scope{serviceaccount.ScopeRead, serviceaccount.ScopeWrite},
			SecretHash: serviceaccount.HashSecret(writerSecret)},
		{ID: adminID, Name: "root", TenantID: "acme",
			Scopes:     []serviceaccount.Scope{serviceaccount.ScopeAdmin},
			SecretHash: serviceaccount.HashSecret(adminSecret)},
	})
	writerToken := serviceaccount.MintToken(writerID, writerSecret)
	adminToken := serviceaccount.MintToken(adminID, adminSecret)

	// authedGet sends a GET with a bearer token.
	authedGet := func(h *deliveryHarness, token, path string) rawResponse {
		h.t.Helper()
		req, err := http.NewRequest(http.MethodGet, h.srv.URL+path, nil)
		So(err, ShouldBeNil)
		req.Header.Set("Authorization", "Bearer "+token)
		r, err := h.srv.Client().Do(req)
		So(err, ShouldBeNil)
		defer func() { _ = r.Body.Close() }()
		body, err := io.ReadAll(r.Body)
		So(err, ShouldBeNil)
		return rawResponse{Status: r.StatusCode, Header: r.Header, Body: body}
	}

	Convey("Given a provisioning-enabled API behind authentication", t, func() {
		h, _ := newProvisioningHarness(t, accounts)

		Convey("When a write-scoped account reads the tenant list", func() {
			resp := authedGet(h, writerToken, "/api/v1/tenants")

			Convey("Then it is 403 naming the admin scope it lacks", func() {
				So(resp.Status, ShouldEqual, http.StatusForbidden)
				So(resp.errorCode(), ShouldEqual, "FORBIDDEN")
				So(string(resp.Body), ShouldContainSubstring, "missing scope admin")
			})
		})

		Convey("When a write-scoped account reads the account list", func() {
			resp := authedGet(h, writerToken, "/api/v1/service-accounts?tenant_name=acme")

			Convey("Then it is 403 FORBIDDEN", func() {
				So(resp.Status, ShouldEqual, http.StatusForbidden)
				So(resp.errorCode(), ShouldEqual, "FORBIDDEN")
			})
		})

		Convey("When an admin-scoped account reads the tenant list", func() {
			resp := authedGet(h, adminToken, "/api/v1/tenants")

			Convey("Then it is allowed through to the control plane", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
			})
		})

		Convey("When no credentials are presented at all", func() {
			resp := h.get("/api/v1/tenants")

			Convey("Then authentication answers first with 401", func() {
				So(resp.Status, ShouldEqual, http.StatusUnauthorized)
				So(resp.errorCode(), ShouldEqual, "UNAUTHENTICATED")
			})
		})
	})
}

func (s *adminStore) SetAccountRoles(_ context.Context, id ulid.ID, roles []string, perms map[string]string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	if !ok {
		return domainerrors.NewNotFound("service_account", id.String())
	}
	a.Roles, a.FieldPermissions, a.UpdatedAt = roles, perms, now
	s.accounts[id] = a
	return nil
}

func (s *adminStore) UpsertRole(_ context.Context, r admin.Role) (admin.Role, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := r.TenantID + "/" + r.Name
	// Mirror the SQL upsert: an update keeps the existing id and created_at.
	if prev, ok := s.roles[key]; ok {
		r.ID, r.CreatedAt = prev.ID, prev.CreatedAt
	}
	s.roles[key] = r
	return r, nil
}

func (s *adminStore) ListRoles(_ context.Context, tenant string) ([]admin.Role, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []admin.Role{}
	for _, r := range s.roles {
		if r.TenantID == tenant {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *adminStore) DeleteRole(_ context.Context, tenant, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenant + "/" + name
	if _, ok := s.roles[key]; !ok {
		return domainerrors.NewNotFound("role", name)
	}
	delete(s.roles, key)
	return nil
}

// TestRoleRoutes covers the role control plane over the same live interactor:
// creation, listing, replacement, deletion and assignment to an account.
func TestRoleRoutes(t *testing.T) {
	Convey("Given a provisioning-enabled API with a tenant", t, func() {
		h, _ := newProvisioningHarness(t, nil)
		So(h.post("/api/v1/tenants", map[string]any{"name": "acme"}).Status,
			ShouldEqual, http.StatusCreated)

		Convey("When a role is created", func() {
			resp := h.put("/api/v1/roles", map[string]any{
				"tenant_name": "acme", "name": "reader",
				"description":       "read-only",
				"scopes":            []string{"read"},
				"field_permissions": map[string]string{"salary": "none"},
			})

			Convey("Then it is 200 with the stored permission set", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				obj := resp.object(t)
				So(obj["name"], ShouldEqual, "reader")
				So(obj["tenant_id"], ShouldEqual, "acme")
				perms := obj["field_permissions"].(map[string]any)
				So(perms["salary"], ShouldEqual, "none")
			})

			Convey("And it appears in the tenant's role list", func() {
				list := h.get("/api/v1/roles?tenant_name=acme")
				So(list.Status, ShouldEqual, http.StatusOK)
				items := list.object(t)["items"].([]any)
				So(len(items), ShouldEqual, 1)
				So(items[0].(map[string]any)["name"], ShouldEqual, "reader")
			})

			Convey("And a second write under the same name replaces it", func() {
				again := h.put("/api/v1/roles", map[string]any{
					"tenant_name": "acme", "name": "reader",
					"scopes": []string{"read", "write"},
				})
				So(again.Status, ShouldEqual, http.StatusOK)

				list := h.get("/api/v1/roles?tenant_name=acme")
				items := list.object(t)["items"].([]any)
				So(len(items), ShouldEqual, 1)
				So(items[0].(map[string]any)["field_permissions"], ShouldBeNil)
			})

			Convey("And it can be deleted", func() {
				del := h.delete("/api/v1/roles/reader?tenant_name=acme")
				So(del.Status, ShouldEqual, http.StatusNoContent)

				list := h.get("/api/v1/roles?tenant_name=acme")
				So(string(list.Body), ShouldContainSubstring, `"items":[]`)
			})

			Convey("And an account can be given the role", func() {
				created := h.post("/api/v1/service-accounts", map[string]any{
					"tenant_name": "acme", "name": "analyst", "roles": []string{"reader"},
				})
				So(created.Status, ShouldEqual, http.StatusCreated)
				id := created.object(t)["account"].(map[string]any)["id"].(string)

				assigned := h.put("/api/v1/service-accounts/"+id+"/roles", map[string]any{
					"roles":             []string{"reader"},
					"field_permissions": map[string]string{"ssn": "none"},
				})
				So(assigned.Status, ShouldEqual, http.StatusNoContent)

				list := h.get("/api/v1/service-accounts?tenant_name=acme")
				acct := list.object(t)["items"].([]any)[0].(map[string]any)
				So(acct["roles"].([]any)[0], ShouldEqual, "reader")
				So(acct["field_permissions"].(map[string]any)["ssn"], ShouldEqual, "none")
			})

			Convey("And assigning it to an unknown account is 404", func() {
				resp := h.put("/api/v1/service-accounts/"+ulid.New().String()+"/roles",
					map[string]any{"roles": []string{"reader"}})
				So(resp.Status, ShouldEqual, http.StatusNotFound)
			})
		})

		Convey("When a role is listed without a tenant", func() {
			resp := h.get("/api/v1/roles")

			Convey("Then it is 422 rather than a cross-tenant listing", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When a role is deleted without a tenant", func() {
			resp := h.delete("/api/v1/roles/reader")

			Convey("Then it is 422", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
			})
		})

		Convey("When a role that does not exist is deleted", func() {
			resp := h.delete("/api/v1/roles/ghost?tenant_name=acme")

			Convey("Then it is 404, not a silent 204", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
				So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
			})
		})

		Convey("When a field permission carries a level that does not exist", func() {
			resp := h.put("/api/v1/roles", map[string]any{
				"tenant_name": "acme", "name": "reader",
				"field_permissions": map[string]string{"salary": "readonly"},
			})

			Convey("Then it is 422 rather than a silent denial", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When an account names a role the tenant does not have", func() {
			resp := h.post("/api/v1/service-accounts", map[string]any{
				"tenant_name": "acme", "name": "analyst", "roles": []string{"ghost"},
			})

			Convey("Then it is 404 rather than a credential that grants nothing", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
			})
		})

		Convey("When the role bodies are malformed", func() {
			Convey("Then upsert and assign are 422 VALIDATION", func() {
				So(h.put("/api/v1/roles", `{`).Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(h.put("/api/v1/service-accounts/"+ulid.New().String()+"/roles", `{`).Status,
					ShouldEqual, http.StatusUnprocessableEntity)
			})
		})
	})
}

// TestRoleRoutesWithoutProvisioning covers the branch a deployment gives when
// no database-backed control plane is wired: the role endpoints are absent,
// not broken.
func TestRoleRoutesWithoutProvisioning(t *testing.T) {
	Convey("Given an API built without a provisioning interactor", t, func() {
		store := memory.NewStore()
		factory := application.NewFactory(application.FactoryConfig{
			Transactor:      store.Transactor(),
			NewRepositories: func() application.Repositories { return store.Repositories() },
			ActivityLog:     store.ActivityLog(),
		})
		srv := httptest.NewServer(buildRouter(ServerConfig{
			Factory: factory,
			Logger:  logger.New(logger.Config{Level: "error"}),
			Health:  health.NewService("flexitype", "test"),
			// Admin deliberately left nil.
		}))
		t.Cleanup(srv.Close)
		h := &deliveryHarness{t: t, srv: srv}

		Convey("When any role route is called", func() {
			Convey("Then every one is 501 FEATURE_DISABLED", func() {
				for _, call := range []rawResponse{
					h.get("/api/v1/roles?tenant_name=acme"),
					h.put("/api/v1/roles", map[string]any{"tenant_name": "acme", "name": "reader"}),
					h.delete("/api/v1/roles/reader?tenant_name=acme"),
					h.put("/api/v1/service-accounts/"+ulid.New().String()+"/roles",
						map[string]any{"roles": []string{"reader"}}),
				} {
					So(call.Status, ShouldEqual, http.StatusNotImplemented)
					So(call.errorCode(), ShouldEqual, "FEATURE_DISABLED")
				}
			})
		})
	})
}

func (s *adminStore) CountAccountsWithRole(_ context.Context, tenant, name string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, a := range s.accounts {
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

// TestAccessForFailsClosedOnUnresolvedRole covers the fail-open path a role
// deletion used to open.
//
// A role that resolves to nothing contributes no field permissions, and an
// empty permission map otherwise reads as "unrestricted" — so an account
// restricted only by a deleted role read every attribute that role was
// hiding, with no error, no log line and no change to the account row.
func TestAccessForFailsClosedOnUnresolvedRole(t *testing.T) {
	Convey("Given a principal whose role no longer exists", t, func() {
		acct := serviceaccount.Account{
			ID: "a1", TenantID: "acme",
			Scopes:          []serviceaccount.Scope{serviceaccount.ScopeRead},
			UnresolvedRoles: []string{"nosalary"},
		}

		Convey("When its field access is derived", func() {
			access := accessFor(acct)

			Convey("Then it is denied every attribute rather than granted every one", func() {
				So(access.Admin, ShouldBeFalse)
				So(access.Default, ShouldEqual, uow.PermNone)
				So(access.CanRead("salary"), ShouldBeFalse)
				So(access.CanWrite("salary"), ShouldBeFalse)
			})
		})
	})

	Convey("Given a principal with a resolved restriction", t, func() {
		acct := serviceaccount.Account{
			ID: "a1", TenantID: "acme",
			Scopes:           []serviceaccount.Scope{serviceaccount.ScopeRead},
			FieldPermissions: map[string]string{"salary": "none"},
		}

		Convey("When its field access is derived", func() {
			access := accessFor(acct)

			Convey("Then only the named attribute is restricted", func() {
				So(access.CanRead("salary"), ShouldBeFalse)
				So(access.CanRead("name"), ShouldBeTrue)
			})
		})
	})

	Convey("Given a principal with no policy at all", t, func() {
		acct := serviceaccount.Account{
			ID: "a1", TenantID: "acme",
			Scopes: []serviceaccount.Scope{serviceaccount.ScopeRead},
		}

		Convey("When its field access is derived", func() {
			access := accessFor(acct)

			Convey("Then it stays unrestricted: no policy source means no restriction", func() {
				So(access.Admin, ShouldBeTrue)
			})
		})
	})
}

// TestRoleSafetyRoutes covers the API-level guards on the role control plane.
func TestRoleSafetyRoutes(t *testing.T) {
	Convey("Given a provisioning-enabled API with a tenant", t, func() {
		h, _ := newProvisioningHarness(t, nil)
		So(h.post("/api/v1/tenants", map[string]any{"name": "acme"}).Status,
			ShouldEqual, http.StatusCreated)

		Convey("When a role asks for the admin scope", func() {
			resp := h.put("/api/v1/roles", map[string]any{
				"tenant_name": "acme", "name": "ops", "scopes": []string{"admin"},
			})

			Convey("Then it is 422: admin must be granted where the account row shows it", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("And a role held by an account", func() {
			So(h.put("/api/v1/roles", map[string]any{
				"tenant_name": "acme", "name": "reader", "scopes": []string{"read"},
				"field_permissions": map[string]string{"salary": "none"},
			}).Status, ShouldEqual, http.StatusOK)
			created := h.post("/api/v1/service-accounts", map[string]any{
				"tenant_name": "acme", "name": "analyst", "roles": []string{"reader"},
			})
			So(created.Status, ShouldEqual, http.StatusCreated)
			id := created.object(t)["account"].(map[string]any)["id"].(string)

			Convey("When the role is deleted", func() {
				resp := h.delete("/api/v1/roles/reader?tenant_name=acme")

				Convey("Then it is 409, naming how many accounts still hold it", func() {
					So(resp.Status, ShouldEqual, http.StatusConflict)
					So(resp.errorCode(), ShouldEqual, "CONFLICT")
					So(string(resp.Body), ShouldContainSubstring, "still assigned")
				})
			})

			Convey("When the account's effective permissions are read", func() {
				resp := h.get("/api/v1/service-accounts/" + id + "/effective")

				Convey("Then the role's grants are resolved into scopes and field permissions", func() {
					So(resp.Status, ShouldEqual, http.StatusOK)
					obj := resp.object(t)
					So(obj["roles"].([]any)[0], ShouldEqual, "reader")
					So(obj["scopes"].([]any), ShouldContain, "read")
					So(obj["field_permissions"].(map[string]any)["salary"], ShouldEqual, "none")
				})
			})

			Convey("When the account is reassigned and the role is then deleted", func() {
				So(h.put("/api/v1/service-accounts/"+id+"/roles",
					map[string]any{"roles": []string{}}).Status, ShouldEqual, http.StatusNoContent)
				resp := h.delete("/api/v1/roles/reader?tenant_name=acme")

				Convey("Then the delete succeeds", func() {
					So(resp.Status, ShouldEqual, http.StatusNoContent)
				})
			})
		})

		Convey("When the effective permissions of an unknown account are read", func() {
			resp := h.get("/api/v1/service-accounts/" + ulid.New().String() + "/effective")

			Convey("Then it is 404", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
			})
		})
	})
}

// TestClientKey covers the identifier the pre-authentication limiter buckets
// on. It is deliberately RemoteAddr and not X-Forwarded-For: a header is
// attacker-supplied, so trusting it would let one client spread its attempts
// across unlimited keys and defeat the limiter entirely.
func TestClientKey(t *testing.T) {
	Convey("Given requests from various remote addresses", t, func() {
		Convey("When the address carries a port", func() {
			Convey("Then the host alone is the key, so one client is one bucket", func() {
				r := &http.Request{RemoteAddr: "203.0.113.7:54321"}
				So(clientKey(r), ShouldEqual, "203.0.113.7")
			})
		})

		Convey("When the address is IPv6", func() {
			Convey("Then the host is still extracted", func() {
				r := &http.Request{RemoteAddr: "[2001:db8::1]:443"}
				So(clientKey(r), ShouldEqual, "2001:db8::1")
			})
		})

		Convey("When the address carries no port", func() {
			Convey("Then it is used whole rather than dropped, so the bucket still exists", func() {
				r := &http.Request{RemoteAddr: "203.0.113.7"}
				So(clientKey(r), ShouldEqual, "203.0.113.7")
			})
		})

		Convey("When a forwarding header claims another address", func() {
			Convey("Then it is ignored", func() {
				r := &http.Request{RemoteAddr: "203.0.113.7:1", Header: http.Header{}}
				r.Header.Set("X-Forwarded-For", "198.51.100.9")
				So(clientKey(r), ShouldEqual, "203.0.113.7")
			})
		})
	})
}
