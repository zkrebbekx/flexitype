package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/health"
	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
)

// TestMaintenanceRoutesRequireAdminScope covers the two tenant-wide rebuilds.
//
// Both are cheap for a client to issue and expensive for the service to
// perform, so a write-scoped token could repeatedly trigger them — a
// within-tenant denial of service that a per-request rate limiter does not
// contain, because every individual call is legitimate.
func TestMaintenanceRoutesRequireAdminScope(t *testing.T) {
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

	store := memory.NewStore()
	factory := application.NewFactory(application.FactoryConfig{
		Transactor:      store.Transactor(),
		NewRepositories: func() application.Repositories { return store.Repositories() },
		ActivityLog:     store.ActivityLog(),
	})
	noop := func(context.Context, valueobjects.TenantID) (int, error) { return 0, nil }

	srv := httptest.NewServer(buildRouter(ServerConfig{
		Factory:           factory,
		Logger:            logger.New(logger.Config{Level: "error"}),
		Health:            health.NewService("flexitype", "test"),
		Accounts:          accounts,
		Reindex:           noop,
		RecomputeComputed: noop,
	}))
	t.Cleanup(srv.Close)

	post := func(token, path string) int {
		req, err := http.NewRequest(http.MethodPost, srv.URL+path, nil)
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
		for _, path := range []string{"/api/v1/search/reindex", "/api/v1/computed/recompute"} {
			Convey("Then "+path+" refuses a write-scoped token", func() {
				So(post(writerToken, path), ShouldEqual, http.StatusForbidden)
			})

			Convey("Then "+path+" accepts an admin-scoped token", func() {
				So(post(adminToken, path), ShouldEqual, http.StatusOK)
			})
		}
	})
}
