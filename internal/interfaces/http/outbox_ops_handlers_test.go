package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application"
	appoutbox "github.com/zkrebbekx/flexitype/application/outbox"
	appwebhook "github.com/zkrebbekx/flexitype/application/webhook"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/health"
	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// opsStore is an in-test outbox.OpsStore serving a canned parked set and
// recording redrives, so the HTTP surface is testable without PostgreSQL.
type opsStore struct {
	parked     []appoutbox.ParkedEnvelope
	lastFilter appoutbox.ParkedFilter
	redriven   int
}

func (s *opsStore) ListParked(_ context.Context, f appoutbox.ParkedFilter, _ db.Page) ([]appoutbox.ParkedEnvelope, int, error) {
	s.lastFilter = f
	return s.parked, len(s.parked), nil
}

func (s *opsStore) Redrive(_ context.Context, _ db.Transactor, f appoutbox.ParkedFilter) (int, error) {
	s.lastFilter = f
	return s.redriven, nil
}

// newOutboxOpsServer builds the real router with event delivery on and the
// parked-recovery surface backed by the fake above. A nil ops leaves
// FactoryConfig.OutboxOps unset — the not-wired deployment shape.
func newOutboxOpsServer(t *testing.T, ops appoutbox.OpsStore, nudged *int, accounts *serviceaccount.Store) *httptest.Server {
	t.Helper()
	store := memory.NewStore()
	cfg := application.FactoryConfig{
		Transactor:      store.Transactor(),
		NewRepositories: func() application.Repositories { return store.Repositories() },
		ActivityLog:     store.ActivityLog(),
		Features:        application.Features{EventDelivery: true},
		Outbox:          nopSink{},
		Subscriptions:   newSubStore(),
		Deliveries:      &deliveryStore{},
		FeedStore:       &feedStore{},
		CursorStore:     newCursorStore(),
		// Loopback endpoints are the only reachable ones from a test.
		WebhookURLPolicy: appwebhook.URLPolicy{AllowPrivate: true},
	}
	if ops != nil {
		cfg.OutboxOps = ops
		cfg.OutboxNudge = func() {
			if nudged != nil {
				*nudged++
			}
		}
	}

	serverCfg := ServerConfig{
		Factory: application.NewFactory(cfg),
		Logger:  logger.New(logger.Config{Level: "error"}),
		Health:  health.NewService("flexitype", "test"),
	}
	if accounts != nil {
		// Assign only a non-nil store: a typed nil in the Authenticator
		// interface would enable auth with no accounts.
		serverCfg.Accounts = accounts
	}
	srv := httptest.NewServer(buildRouter(serverCfg))
	t.Cleanup(srv.Close)
	return srv
}

// TestOutboxOpsHandlers covers the parked-recovery HTTP surface (#478):
// before it existed, a parked envelope was invisible and undeliverable
// through every API.
func TestOutboxOpsHandlers(t *testing.T) {
	parkedID := ulid.New().String()

	Convey("Given a server with one parked envelope behind the recovery surface", t, func() {
		store := &opsStore{parked: []appoutbox.ParkedEnvelope{{
			ID:        parkedID,
			EventType: "flexitype.entity.updated",
			Attempts:  25,
			LastError: "downstream kept refusing",
			ParkedAt:  time.Now().UTC(),
		}}}
		nudged := 0
		srv := newOutboxOpsServer(t, store, &nudged, nil)
		h := &deliveryHarness{t: t, srv: srv}

		Convey("When the parked listing is requested", func() {
			resp := h.get("/api/v1/admin/outbox/parked")

			Convey("Then the parked envelope returns with its evidence", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				body := resp.object(t)
				items, ok := body["items"].([]any)
				So(ok, ShouldBeTrue)
				So(items, ShouldHaveLength, 1)
				item, ok := items[0].(map[string]any)
				So(ok, ShouldBeTrue)
				So(item["id"], ShouldEqual, parkedID)
				So(item["attempts"], ShouldEqual, 25)
				So(item["last_error"], ShouldEqual, "downstream kept refusing")
			})
		})

		Convey("When the listing is narrowed by event type", func() {
			resp := h.get("/api/v1/admin/outbox/parked?event_type=flexitype.entity.updated")

			Convey("Then the filter reaches the store", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(store.lastFilter.EventType, ShouldEqual, "flexitype.entity.updated")
			})
		})

		Convey("When the listing is narrowed by a malformed id", func() {
			resp := h.get("/api/v1/admin/outbox/parked?id=not-a-ulid")

			Convey("Then it is a validation error, not a 500", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
			})
		})

		Convey("When a redrive is posted", func() {
			store.redriven = 4
			resp := h.post("/api/v1/admin/outbox/redrive", nil)

			Convey("Then the moved count returns and the relay is nudged", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(resp.object(t)["redriven"], ShouldEqual, 4)
				So(nudged, ShouldEqual, 1)
			})
		})

		Convey("When a redrive is narrowed to one envelope", func() {
			store.redriven = 1
			resp := h.post("/api/v1/admin/outbox/redrive?id="+parkedID, nil)

			Convey("Then the id filter reaches the store", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(store.lastFilter.ID, ShouldEqual, parkedID)
			})
		})
	})

	Convey("Given a server whose factory has no ops-capable outbox store", t, func() {
		srv := newOutboxOpsServer(t, nil, nil, nil)
		h := &deliveryHarness{t: t, srv: srv}

		Convey("When either recovery endpoint is called", func() {
			list := h.get("/api/v1/admin/outbox/parked")
			redrive := h.post("/api/v1/admin/outbox/redrive", nil)

			Convey("Then both answer as a disabled feature rather than panicking", func() {
				So(list.Status, ShouldEqual, http.StatusNotImplemented)
				So(list.errorCode(), ShouldEqual, "FEATURE_DISABLED")
				So(redrive.Status, ShouldEqual, http.StatusNotImplemented)
				So(redrive.errorCode(), ShouldEqual, "FEATURE_DISABLED")
			})
		})
	})
}

// TestOutboxOpsRequireAdminScope pins the privilege gate: a redrive
// re-publishes committed events and the listing exposes cross-cutting
// delivery internals, so both are operator actions.
func TestOutboxOpsRequireAdminScope(t *testing.T) {
	writerSecret, adminSecret := "writer-secret", "admin-secret"
	writerID, adminID := "01KXWRITER1000000000000000", "01KXADMIN10000000000000000"
	accounts := serviceaccount.NewStore([]serviceaccount.Account{
		{ID: writerID, Name: "writer", TenantID: "acme",
			Scopes:     []serviceaccount.Scope{serviceaccount.ScopeRead, serviceaccount.ScopeWrite},
			SecretHash: serviceaccount.HashSecret(writerSecret)},
		{ID: adminID, Name: "root", TenantID: "acme",
			Scopes:     []serviceaccount.Scope{serviceaccount.ScopeAdmin},
			SecretHash: serviceaccount.HashSecret(adminSecret)},
	})

	srv := newOutboxOpsServer(t, &opsStore{}, nil, accounts)

	call := func(token, method, path string) int {
		req, err := http.NewRequest(method, srv.URL+path, nil)
		So(err, ShouldBeNil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := srv.Client().Do(req)
		So(err, ShouldBeNil)
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}

	writerToken := serviceaccount.MintToken(writerID, writerSecret)
	adminToken := serviceaccount.MintToken(adminID, adminSecret)

	Convey("Given a write-scoped and an admin-scoped account", t, func() {
		Convey("Then the parked listing refuses a write-scoped token", func() {
			So(call(writerToken, http.MethodGet, "/api/v1/admin/outbox/parked"), ShouldEqual, http.StatusForbidden)
		})

		Convey("Then the redrive refuses a write-scoped token", func() {
			So(call(writerToken, http.MethodPost, "/api/v1/admin/outbox/redrive"), ShouldEqual, http.StatusForbidden)
		})

		Convey("Then both accept an admin-scoped token", func() {
			So(call(adminToken, http.MethodGet, "/api/v1/admin/outbox/parked"), ShouldEqual, http.StatusOK)
			So(call(adminToken, http.MethodPost, "/api/v1/admin/outbox/redrive"), ShouldEqual, http.StatusOK)
		})
	})
}
