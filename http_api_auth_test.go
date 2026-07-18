package flexitype_test

import (
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/pkg/metrics"
	"github.com/zkrebbekx/flexitype/pkg/ratelimit"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
)

// account builds a file-backed service account and the bearer token that
// authenticates as it. Secrets are stored as hex(sha256(secret)), exactly as
// the JSON account file holds them.
func account(id, name, tenant string, scopes []serviceaccount.Scope, fieldPerms map[string]string) (serviceaccount.Account, string) {
	secret := "s3cret-" + id
	return serviceaccount.Account{
		ID:               id,
		Name:             name,
		TenantID:         tenant,
		Scopes:           scopes,
		FieldPermissions: fieldPerms,
		SecretHash:       serviceaccount.HashSecret(secret),
	}, serviceaccount.MintToken(id, secret)
}

// TestHTTPAuthentication covers the bearer-token gate in front of /api/v1:
// missing and bad credentials, the read/write scope split derived from the
// HTTP method, and the tenant an account's requests are pinned to.
func TestHTTPAuthentication(t *testing.T) {
	reader, readerToken := account("01KXREADER0000000000000000", "reader", "acme",
		[]serviceaccount.Scope{serviceaccount.ScopeRead}, nil)
	writer, writerToken := account("01KXWRITER0000000000000000", "writer", "acme",
		[]serviceaccount.Scope{serviceaccount.ScopeRead, serviceaccount.ScopeWrite}, nil)
	root, rootToken := account("01KXADMIN00000000000000000", "root", "acme",
		[]serviceaccount.Scope{serviceaccount.ScopeAdmin}, nil)

	accounts := serviceaccount.NewStore([]serviceaccount.Account{reader, writer, root})

	Convey("Given an API guarded by service-account authentication", t, func() {
		a := newAPI(t, flexitype.APIConfig{Accounts: accounts})

		Convey("When no Authorization header is sent", func() {
			resp := a.get("/api/v1/type-definitions")

			Convey("Then it is 401 UNAUTHENTICATED", func() {
				So(resp.Status, ShouldEqual, http.StatusUnauthorized)
				So(resp.errorCode(), ShouldEqual, "UNAUTHENTICATED")
				So(resp.errorMessage(), ShouldContainSubstring, "missing bearer token")
			})
		})

		Convey("When the token is not a flexitype token", func() {
			resp := a.as("some-opaque-string").get("/api/v1/type-definitions")

			Convey("Then it is 401 with a non-committal message", func() {
				So(resp.Status, ShouldEqual, http.StatusUnauthorized)
				So(resp.errorCode(), ShouldEqual, "UNAUTHENTICATED")
				So(resp.errorMessage(), ShouldEqual, "invalid credentials")
			})
		})

		Convey("When the account id is known but the secret is wrong", func() {
			resp := a.as(serviceaccount.MintToken(writer.ID, "wrong")).get("/api/v1/type-definitions")

			Convey("Then it is 401, indistinguishable from an unknown account", func() {
				So(resp.Status, ShouldEqual, http.StatusUnauthorized)
				So(resp.errorCode(), ShouldEqual, "UNAUTHENTICATED")
				So(resp.errorMessage(), ShouldEqual, "invalid credentials")
			})
		})

		Convey("When a read-scoped account reads", func() {
			resp := a.as(readerToken).get("/api/v1/type-definitions")

			Convey("Then the read scope suffices", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
			})
		})

		Convey("When a read-scoped account writes", func() {
			resp := a.as(readerToken).post("/api/v1/type-definitions", map[string]any{
				"internal_name": "product", "display_name": "Product",
			})

			Convey("Then it is 403 FORBIDDEN naming the missing scope", func() {
				So(resp.Status, ShouldEqual, http.StatusForbidden)
				So(resp.errorCode(), ShouldEqual, "FORBIDDEN")
				So(resp.errorMessage(), ShouldContainSubstring, "missing scope write")
			})
		})

		Convey("When a write-scoped account writes", func() {
			resp := a.as(writerToken).post("/api/v1/type-definitions", map[string]any{
				"internal_name": "product", "display_name": "Product",
			})

			Convey("Then the write is accepted and stamped with the account's tenant", func() {
				So(resp.Status, ShouldEqual, http.StatusCreated)
				So(resp.object(t)["tenant_id"], ShouldEqual, "acme")
			})

			Convey("And the audit trail names the account, not the system", func() {
				log := a.as(writerToken).get("/api/v1/activity")
				So(log.Status, ShouldEqual, http.StatusOK)
				items := log.items(t)
				So(len(items), ShouldEqual, 1)
				So(items[0].(map[string]any)["actor"], ShouldContainSubstring, "writer")
			})
		})

		Convey("When an admin-scoped account writes", func() {
			resp := a.as(rootToken).post("/api/v1/type-definitions", map[string]any{
				"internal_name": "product", "display_name": "Product",
			})

			Convey("Then admin implies write", func() {
				So(resp.Status, ShouldEqual, http.StatusCreated)
			})
		})

		Convey("When two accounts of different tenants write the same name", func() {
			other, otherToken := account("01KXOTHER00000000000000000", "other", "globex",
				[]serviceaccount.Scope{serviceaccount.ScopeWrite, serviceaccount.ScopeRead}, nil)
			multi := newAPI(t, flexitype.APIConfig{
				Accounts: serviceaccount.NewStore([]serviceaccount.Account{writer, other}),
			})

			mine := multi.as(writerToken).post("/api/v1/type-definitions", map[string]any{
				"internal_name": "product", "display_name": "Ours",
			})
			theirs := multi.as(otherToken).post("/api/v1/type-definitions", map[string]any{
				"internal_name": "product", "display_name": "Theirs",
			})

			Convey("Then both succeed — tenants are isolated, not in conflict", func() {
				So(mine.Status, ShouldEqual, http.StatusCreated)
				So(theirs.Status, ShouldEqual, http.StatusCreated)
				So(mine.object(t)["tenant_id"], ShouldEqual, "acme")
				So(theirs.object(t)["tenant_id"], ShouldEqual, "globex")
			})

			Convey("And neither tenant can see the other's type", func() {
				ours := multi.as(writerToken).get("/api/v1/type-definitions")
				So(len(ours.items(t)), ShouldEqual, 1)
				So(ours.items(t)[0].(map[string]any)["display_name"], ShouldEqual, "Ours")

				So(multi.as(otherToken).get("/api/v1/type-definitions/"+mine.str(t, "id")).Status,
					ShouldEqual, http.StatusNotFound)
			})
		})

		Convey("When the public OpenAPI document is fetched without credentials", func() {
			resp := a.get("/api/v1/openapi.json")

			Convey("Then it is served anyway — the contract is public by design", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
			})
		})

		Convey("When the health probes are called without credentials", func() {
			Convey("Then they answer, being outside the authenticated API", func() {
				So(a.get("/healthz").Status, ShouldEqual, http.StatusOK)
				So(a.get("/readyz").Status, ShouldEqual, http.StatusOK)
			})
		})
	})
}

// TestHTTPAdminScopedRoutes covers the endpoints that demand the admin scope
// even from an otherwise fully-privileged writer: irreversible erasure and
// the provisioning control plane.
func TestHTTPAdminScopedRoutes(t *testing.T) {
	writer, writerToken := account("01KXWRITER0000000000000000", "writer", "acme",
		[]serviceaccount.Scope{serviceaccount.ScopeRead, serviceaccount.ScopeWrite}, nil)
	root, rootToken := account("01KXADMIN00000000000000000", "root", "acme",
		[]serviceaccount.Scope{serviceaccount.ScopeAdmin}, nil)
	accounts := serviceaccount.NewStore([]serviceaccount.Account{writer, root})

	Convey("Given an authenticated API with a writer and an admin", t, func() {
		a := newAPI(t, flexitype.APIConfig{Accounts: accounts})

		admin := a.as(rootToken)
		typeID := admin.mustCreateType("product", "Product")
		nameID := admin.mustCreateAttr(typeID, "name", "string", nil)
		admin.mustSetValue(typeID, nameID, "sku-1", "Widget")

		Convey("When a writer purges an entity", func() {
			resp := a.as(writerToken).post("/api/v1/entities/"+typeID+"/sku-1/purge", nil)

			Convey("Then it is 403 — erasure needs admin, not merely write", func() {
				So(resp.Status, ShouldEqual, http.StatusForbidden)
				So(resp.errorCode(), ShouldEqual, "FORBIDDEN")
				So(resp.errorMessage(), ShouldContainSubstring, "missing scope admin")
			})

			Convey("And the entity's data survives the refused call", func() {
				values := a.as(writerToken).get("/api/v1/entities/" + typeID + "/sku-1/values")
				So(len(values.items(t)), ShouldEqual, 1)
			})
		})

		Convey("When an admin purges the same entity", func() {
			resp := admin.post("/api/v1/entities/"+typeID+"/sku-1/purge", nil)

			Convey("Then the erasure is carried out", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(resp.object(t)["values_purged"], ShouldEqual, float64(1))
			})
		})

		Convey("When a writer purges the whole tenant", func() {
			resp := a.as(writerToken).post("/api/v1/admin/purge", nil)

			Convey("Then it is 403 FORBIDDEN", func() {
				So(resp.Status, ShouldEqual, http.StatusForbidden)
				So(resp.errorCode(), ShouldEqual, "FORBIDDEN")
			})
		})

		Convey("When an admin purges the whole tenant", func() {
			resp := admin.post("/api/v1/admin/purge", nil)

			Convey("Then every value is erased", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(resp.object(t)["values_purged"], ShouldEqual, float64(1))
			})
		})

		Convey("When a writer calls the provisioning API of a service without one", func() {
			resp := a.as(writerToken).get("/api/v1/tenants")

			Convey("Then the feature gate answers first with 501, not leaking that a scope was also missing", func() {
				So(resp.Status, ShouldEqual, http.StatusNotImplemented)
				So(resp.errorCode(), ShouldEqual, "FEATURE_DISABLED")
			})
		})
	})
}

// TestHTTPFieldPermissions covers attribute-level access control: an account
// with declared field permissions may only see and write the attributes its
// permissions allow, while an admin bypasses the mechanism entirely.
func TestHTTPFieldPermissions(t *testing.T) {
	root, rootToken := account("01KXADMIN00000000000000000", "root", "acme",
		[]serviceaccount.Scope{serviceaccount.ScopeAdmin}, nil)
	// "name" is readable, "cost" is invisible, "notes" is writable.
	limited, limitedToken := account("01KXLIMITED000000000000000", "limited", "acme",
		[]serviceaccount.Scope{serviceaccount.ScopeRead, serviceaccount.ScopeWrite},
		map[string]string{"name": "read", "cost": "none", "notes": "write"})
	accounts := serviceaccount.NewStore([]serviceaccount.Account{root, limited})

	Convey("Given a type whose attributes carry per-field permissions", t, func() {
		a := newAPI(t, flexitype.APIConfig{Accounts: accounts})
		admin := a.as(rootToken)
		user := a.as(limitedToken)

		typeID := admin.mustCreateType("product", "Product")
		nameID := admin.mustCreateAttr(typeID, "name", "string", nil)
		costID := admin.mustCreateAttr(typeID, "cost", "float", nil)
		notesID := admin.mustCreateAttr(typeID, "notes", "string", nil)
		admin.mustSetValue(typeID, nameID, "sku-1", "Widget")
		admin.mustSetValue(typeID, costID, "sku-1", 4.25)
		admin.mustSetValue(typeID, notesID, "sku-1", "handle with care")

		Convey("When the admin reads the entity's values", func() {
			resp := admin.get("/api/v1/entities/" + typeID + "/sku-1/values")

			Convey("Then every attribute is visible — admin ignores field permissions", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(len(resp.items(t)), ShouldEqual, 3)
			})
		})

		Convey("When the restricted account reads the entity's values", func() {
			resp := user.get("/api/v1/entities/" + typeID + "/sku-1/values")

			Convey("Then the 'none' attribute is filtered out of the response", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				visible := map[string]bool{}
				for _, it := range resp.items(t) {
					visible[it.(map[string]any)["attribute_definition_id"].(string)] = true
				}
				So(visible[nameID], ShouldBeTrue)
				So(visible[notesID], ShouldBeTrue)
				So(visible[costID], ShouldBeFalse)
			})

			Convey("And the hidden value never appears in the raw body", func() {
				So(string(resp.Body), ShouldNotContainSubstring, "4.25")
			})
		})

		Convey("When the restricted account writes a read-only attribute", func() {
			resp := user.post("/api/v1/values", map[string]any{
				"type_definition_id": typeID, "attribute_definition_id": nameID,
				"entity_id": "sku-1", "value": "Renamed",
			})

			Convey("Then it is 403 naming the attribute, and the stored value is unchanged", func() {
				So(resp.Status, ShouldEqual, http.StatusForbidden)
				So(resp.errorCode(), ShouldEqual, "FORBIDDEN")
				So(resp.errorMessage(), ShouldContainSubstring, "not permitted to write this attribute")
				So(string(resp.Body), ShouldContainSubstring, `"attribute":"name"`)

				stored := admin.get("/api/v1/values?attribute_definition_id=" + nameID)
				So(stored.items(t)[0].(map[string]any)["value"], ShouldEqual, "Widget")
			})
		})

		Convey("When the restricted account writes a writable attribute", func() {
			resp := user.post("/api/v1/values", map[string]any{
				"type_definition_id": typeID, "attribute_definition_id": notesID,
				"entity_id": "sku-1", "value": "updated note",
			})

			Convey("Then the write is accepted", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(resp.object(t)["value"], ShouldEqual, "updated note")
			})
		})

		Convey("When the restricted account exports the invisible attribute as a column", func() {
			resp := user.get("/api/v1/entities/" + typeID + "/export?attributes=name,cost")

			Convey("Then the export is refused 422 — the attribute is not in this caller's schema", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
				So(string(resp.Body), ShouldContainSubstring, `"attribute":"cost"`)
				So(string(resp.Body), ShouldNotContainSubstring, "4.25")
			})
		})

		Convey("When the restricted account exports only the attributes it may see", func() {
			resp := user.get("/api/v1/entities/" + typeID + "/export?attributes=name")

			Convey("Then the CSV is served and carries no hidden value", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(string(resp.Body), ShouldContainSubstring, "Widget")
				So(string(resp.Body), ShouldNotContainSubstring, "4.25")
			})
		})

		Convey("When the restricted account writes the invisible attribute", func() {
			resp := user.post("/api/v1/values", map[string]any{
				"type_definition_id": typeID, "attribute_definition_id": costID,
				"entity_id": "sku-1", "value": 1.0,
			})

			Convey("Then it is 403 FORBIDDEN", func() {
				So(resp.Status, ShouldEqual, http.StatusForbidden)
				So(resp.errorCode(), ShouldEqual, "FORBIDDEN")
			})
		})
	})
}

// TestHTTPRateLimiting covers the per-account token bucket in front of the
// API: an over-quota caller gets 429 with a Retry-After hint, and the limit
// is scoped to the account rather than shared across them.
func TestHTTPRateLimiting(t *testing.T) {
	first, firstToken := account("01KXFIRST00000000000000000", "first", "acme",
		[]serviceaccount.Scope{serviceaccount.ScopeRead, serviceaccount.ScopeWrite}, nil)
	second, secondToken := account("01KXSECOND0000000000000000", "second", "acme",
		[]serviceaccount.Scope{serviceaccount.ScopeRead, serviceaccount.ScopeWrite}, nil)
	accounts := serviceaccount.NewStore([]serviceaccount.Account{first, second})

	Convey("Given an API throttled to a burst of two requests", t, func() {
		a := newAPI(t, flexitype.APIConfig{
			Accounts:    accounts,
			Metrics:     metrics.New(),
			RateLimiter: ratelimit.New(0.0001, 2), // effectively no refill during the test
		})

		Convey("When one account exhausts its burst", func() {
			So(a.as(firstToken).get("/api/v1/features").Status, ShouldEqual, http.StatusOK)
			So(a.as(firstToken).get("/api/v1/features").Status, ShouldEqual, http.StatusOK)
			resp := a.as(firstToken).get("/api/v1/features")

			Convey("Then the next request is 429 with a machine-readable code", func() {
				So(resp.Status, ShouldEqual, http.StatusTooManyRequests)
				So(resp.errorCode(), ShouldEqual, "RATE_LIMITED")
				So(resp.errorMessage(), ShouldContainSubstring, "retry later")
			})

			Convey("Then a Retry-After header tells the client when to come back", func() {
				So(resp.Header.Get("Retry-After"), ShouldNotBeEmpty)
			})

			Convey("Then a different account is unaffected — buckets are per account", func() {
				So(a.as(secondToken).get("/api/v1/features").Status, ShouldEqual, http.StatusOK)
			})
		})

		Convey("When no limiter is configured", func() {
			open := newAPI(t, flexitype.APIConfig{Accounts: accounts})

			Convey("Then requests are never throttled", func() {
				for i := 0; i < 5; i++ {
					So(open.as(firstToken).get("/api/v1/features").Status, ShouldEqual, http.StatusOK)
				}
			})
		})
	})
}

// TestHTTPDisabledFeatures covers the 501 FEATURE_DISABLED responses a
// deployment gives for surfaces it did not switch on, so a client can tell
// "not configured here" apart from "broken" or "not allowed".
func TestHTTPDisabledFeatures(t *testing.T) {
	Convey("Given a service with search turned off", t, func() {
		a := newAPI(t, flexitype.APIConfig{}, flexitype.WithoutSearch())

		Convey("When the query surface is used", func() {
			run := a.get(`/api/v1/query?type=product&q=name+%3D+%22x%22`)
			validate := a.post("/api/v1/query/validate", map[string]any{"type": "product", "q": "x"})

			Convey("Then both report 501 FEATURE_DISABLED", func() {
				So(run.Status, ShouldEqual, http.StatusNotImplemented)
				So(run.errorCode(), ShouldEqual, "FEATURE_DISABLED")
				So(run.errorMessage(), ShouldContainSubstring, "search")

				So(validate.Status, ShouldEqual, http.StatusNotImplemented)
				So(validate.errorCode(), ShouldEqual, "FEATURE_DISABLED")
			})
		})

		Convey("When the feature flags are read", func() {
			resp := a.get("/api/v1/features")

			Convey("Then search is advertised as off", func() {
				So(resp.object(t)["search"], ShouldBeFalse)
			})
		})
	})

	Convey("Given a service with the activity log turned off", t, func() {
		a := newAPI(t, flexitype.APIConfig{}, flexitype.WithoutActivityLog())

		Convey("When the audit log is read", func() {
			resp := a.get("/api/v1/activity")

			Convey("Then it is 501 FEATURE_DISABLED", func() {
				So(resp.Status, ShouldEqual, http.StatusNotImplemented)
				So(resp.errorCode(), ShouldEqual, "FEATURE_DISABLED")
				So(resp.errorMessage(), ShouldContainSubstring, "activity history")
			})
		})

		Convey("When the feature flags are read", func() {
			resp := a.get("/api/v1/features")

			Convey("Then activity is advertised as off", func() {
				So(resp.object(t)["activity"], ShouldBeFalse)
			})
		})
	})

	Convey("Given a service without the search index", t, func() {
		a := newAPI(t, flexitype.APIConfig{})

		Convey("When a reindex or recompute is requested", func() {
			reindex := a.post("/api/v1/search/reindex", nil)
			recompute := a.post("/api/v1/computed/recompute", nil)

			Convey("Then both are 501 — the maintenance hooks are not wired", func() {
				So(reindex.Status, ShouldEqual, http.StatusNotImplemented)
				So(reindex.errorCode(), ShouldEqual, "FEATURE_DISABLED")
				So(reindex.errorMessage(), ShouldContainSubstring, "search index")

				So(recompute.Status, ShouldEqual, http.StatusNotImplemented)
				So(recompute.errorCode(), ShouldEqual, "FEATURE_DISABLED")
				So(recompute.errorMessage(), ShouldContainSubstring, "computed attributes")
			})
		})
	})

	Convey("Given a service without the outbox (no event delivery)", t, func() {
		a := newAPI(t, flexitype.APIConfig{})

		Convey("When any webhook-subscription route is called", func() {
			Convey("Then every one is 501 FEATURE_DISABLED", func() {
				for _, call := range []apiResponse{
					a.get("/api/v1/webhook-subscriptions"),
					a.post("/api/v1/webhook-subscriptions", map[string]any{"name": "x", "url": "https://example.com"}),
					a.get("/api/v1/webhook-subscriptions/" + missingULID),
					a.patch("/api/v1/webhook-subscriptions/"+missingULID, map[string]any{"active": false}),
					a.delete("/api/v1/webhook-subscriptions/" + missingULID),
					a.get("/api/v1/webhook-subscriptions/" + missingULID + "/deliveries"),
					a.post("/api/v1/webhook-deliveries/"+missingULID+"/redeliver", nil),
				} {
					So(call.Status, ShouldEqual, http.StatusNotImplemented)
					So(call.errorCode(), ShouldEqual, "FEATURE_DISABLED")
				}
			})
		})

		Convey("When any events-feed route is called", func() {
			Convey("Then every one is 501 FEATURE_DISABLED", func() {
				for _, call := range []apiResponse{
					a.get("/api/v1/events"),
					a.get("/api/v1/events/stream"),
					a.get("/api/v1/event-cursors/my-consumer"),
					a.put("/api/v1/event-cursors/my-consumer", map[string]any{"position": 1, "expected": 0}),
				} {
					So(call.Status, ShouldEqual, http.StatusNotImplemented)
					So(call.errorCode(), ShouldEqual, "FEATURE_DISABLED")
					So(call.errorMessage(), ShouldContainSubstring, "enable the outbox")
				}
			})
		})

		Convey("When the feature flags are read", func() {
			resp := a.get("/api/v1/features")

			Convey("Then event delivery is advertised as off", func() {
				So(resp.object(t)["event_delivery"], ShouldBeFalse)
			})
		})
	})

	Convey("Given an in-memory service (no database-backed control plane)", t, func() {
		a := newAPI(t, flexitype.APIConfig{EnableProvisioning: true})

		Convey("When the provisioning API is called by an admin", func() {
			Convey("Then every route is 501 — provisioning needs a database", func() {
				for _, call := range []apiResponse{
					a.get("/api/v1/tenants"),
					a.post("/api/v1/tenants", map[string]any{"name": "newco"}),
					a.patch("/api/v1/tenants/acme", map[string]any{"active": false}),
					a.get("/api/v1/service-accounts?tenant_name=acme"),
					a.post("/api/v1/service-accounts", map[string]any{"tenant_name": "acme", "name": "bot"}),
					a.post("/api/v1/service-accounts/"+missingULID+"/rotate", nil),
					a.delete("/api/v1/service-accounts/" + missingULID),
				} {
					So(call.Status, ShouldEqual, http.StatusNotImplemented)
					So(call.errorCode(), ShouldEqual, "FEATURE_DISABLED")
					So(call.errorMessage(), ShouldContainSubstring, "provisioning")
				}
			})
		})
	})
}
