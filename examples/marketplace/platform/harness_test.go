package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/pkg/logger"
)

// The provisioning control plane — create a tenant, create a service account —
// is database-backed only: an in-memory service answers those endpoints with a
// 501. Onboarding is exactly that control plane, so this suite needs a real
// Postgres-backed flexitype.

const (
	// flexitypeSchema holds the test flexitype's own tables. A private schema
	// keeps this suite out of the repository's other Postgres suites.
	flexitypeSchema = "mkt_platform_flexitype_test"
	// appSchema holds the platform's merchant table.
	appSchema = "mkt_platform_test"
)

func quietLogger() Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// testDSN returns the configured database, or skips.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("MARKETPLACE_TEST_DSN")
	if dsn == "" {
		dsn = os.Getenv("FLEXITYPE_TEST_DSN")
	}
	if dsn == "" {
		t.Skip("FLEXITYPE_TEST_DSN not set; skipping the platform database tests")
	}
	return dsn
}

// testFlexitype is the shared, migrated flexitype every test in this package
// drives. Migrating once and giving each test its own TENANT is far cheaper
// than re-migrating per test, and tenant separation is real isolation here.
type testFlexitype struct {
	url        string
	adminToken string
}

var (
	flexitypeOnce sync.Once
	sharedService testFlexitype
	flexitypeErr  error
	tenantCounter atomic.Int64
)

// newFlexitype boots the shared Postgres-backed flexitype with provisioning on.
func newFlexitype(t *testing.T) testFlexitype {
	t.Helper()
	dsn := testDSN(t)
	flexitypeOnce.Do(func() {
		sharedService, flexitypeErr = bootFlexitype(dsn)
	})
	if flexitypeErr != nil {
		t.Fatalf("boot flexitype: %v", flexitypeErr)
	}
	return sharedService
}

func bootFlexitype(dsn string) (testFlexitype, error) {
	admin, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		return testFlexitype{}, err
	}
	if _, err := admin.Exec("DROP SCHEMA IF EXISTS " + flexitypeSchema + " CASCADE"); err != nil {
		return testFlexitype{}, err
	}
	if _, err := admin.Exec("CREATE SCHEMA " + flexitypeSchema); err != nil {
		return testFlexitype{}, err
	}
	_, _ = admin.Exec("CREATE EXTENSION IF NOT EXISTS pg_trgm SCHEMA public")
	if err := admin.Close(); err != nil {
		return testFlexitype{}, err
	}

	pool, err := sqlx.Connect("postgres", withParam(dsn, "search_path", flexitypeSchema+",public"))
	if err != nil {
		return testFlexitype{}, err
	}
	// WithOutbox because a webhook subscription is an outbox feature: without
	// it the subscription endpoints report the capability as disabled. The
	// relay is deliberately NOT started, so nothing is delivered during a test.
	svc := flexitype.New(pool, flexitype.WithOutbox(), flexitype.WithWebhookAllowPrivate())
	ctx := context.Background()
	if err := svc.Migrate(ctx); err != nil {
		return testFlexitype{}, err
	}
	token, err := svc.BootstrapAdmin(ctx, "default", "bootstrap-admin")
	if err != nil {
		return testFlexitype{}, err
	}
	srv := httptest.NewServer(svc.APIHandler(flexitype.APIConfig{
		Accounts:           svc.NewAccountLookup(0),
		EnableProvisioning: true,
		// Errors only: the request logger would otherwise emit a JSON line
		// per call and bury the test output.
		Logger: logger.New(logger.Config{Level: "error"}),
	}))
	return testFlexitype{url: srv.URL, adminToken: token}, nil
}

// withParam appends a libpq parameter to a URL-form DSN.
func withParam(dsn, key, value string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + key + "=" + value
}

// newTestStore returns a platform store over a freshly created private schema.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("postgres", testDSN(t))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("DROP SCHEMA IF EXISTS " + appSchema + " CASCADE"); err != nil {
		t.Fatalf("drop test schema: %v", err)
	}
	store := NewStore(db, appSchema)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}
	return store
}

// newTenant returns a tenant name no other test in this package uses.
func newTenant(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, tenantCounter.Add(1))
}

// fakeStorefront stands in for the storefront's internal API. It records what
// the platform pushed, so a test can assert on the credential handover without
// running a second service.
type fakeStorefront struct {
	server *httptest.Server

	mu            sync.Mutex
	registrations []storefrontRegistration
	backfills     []string
	// backfillErr, when set, makes every backfill fail.
	backfillErr bool
}

type storefrontRegistration struct {
	Tenant        string
	DisplayName   string `json:"display_name"`
	Token         string `json:"token"`
	WebhookSecret string `json:"webhook_secret"`
}

func newFakeStorefront(t *testing.T, internalToken string) *fakeStorefront {
	t.Helper()
	f := &fakeStorefront{}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /internal/merchants/{tenant}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != internalToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var reg storefrontRegistration
		_ = json.NewDecoder(r.Body).Decode(&reg)
		reg.Tenant = r.PathValue("tenant")
		f.mu.Lock()
		f.registrations = append(f.registrations, reg)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /internal/merchants/{tenant}/backfill", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != internalToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		failing := f.backfillErr
		f.backfills = append(f.backfills, r.PathValue("tenant"))
		f.mu.Unlock()
		if failing {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projected":0}`))
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeStorefront) lastRegistration() (storefrontRegistration, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.registrations) == 0 {
		return storefrontRegistration{}, false
	}
	return f.registrations[len(f.registrations)-1], true
}

func (f *fakeStorefront) registrationCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.registrations)
}

func (f *fakeStorefront) backfillCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.backfills)
}

func (f *fakeStorefront) failBackfills(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.backfillErr = fail
}
