// Package testdb gives each DB-backed test package its own Postgres schema.
//
// Go runs different packages' tests in parallel. The Postgres-backed suites
// share one FLEXITYPE_TEST_DSN and each truncates and seeds the same tables
// with the same fixture names, so one package's TRUNCATE lands inside
// another's test body. The symptom is a duplicate-key error deep inside a
// fixture helper, in a test unrelated to the change under review — which reads
// as "my change broke type-definition creation" rather than "two packages
// shared a database", and which a re-run clears.
//
// Open removes the shared state rather than serializing around it: the pool it
// returns has a search_path of one schema named after the caller's package, so
// every suite gets its own copy of the whole schema and they stay parallel.
package testdb

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/jmoiron/sqlx"
	// The tests that use this package drive the lib/pq-backed repositories.
	_ "github.com/lib/pq"
)

// schemaName accepts only what can be embedded in a connection string and a
// CREATE SCHEMA statement without quoting.
var schemaName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,45}$`)

// Open connects to FLEXITYPE_TEST_DSN with search_path set to a schema private
// to name, creating the schema if needed. It skips the test when no DSN is
// configured, and closes the pool at the end of the test.
//
// name identifies the calling package — "root", "postgres" and so on. Two
// packages must never pass the same name; that is the sharing this exists to
// prevent.
//
// The caller still migrates: each schema starts empty, so every suite applies
// the migrations once into its own copy.
func Open(t *testing.T, name string) *sqlx.DB {
	t.Helper()
	if !schemaName.MatchString(name) {
		t.Fatalf("testdb: schema name %q must be lowercase alphanumeric with underscores", name)
	}
	dsn := os.Getenv("FLEXITYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("FLEXITYPE_TEST_DSN not set; skipping database integration test")
	}

	schema := "flexitype_test_" + name

	// Create the schema over the default search_path first: the scoped pool
	// below cannot connect to a schema that does not exist yet with any
	// certainty about ordering.
	admin, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatalf("testdb: connect: %v", err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA IF NOT EXISTS ` + schema); err != nil {
		_ = admin.Close()
		t.Fatalf("testdb: create schema %s: %v", schema, err)
	}
	// Pin pg_trgm to public. An extension belongs to one schema per database,
	// and CREATE EXTENSION places it at the head of search_path — so whichever
	// suite migrated first would own gin_trgm_ops and the rest would fail to
	// resolve it. Creating it in public, which every scoped pool keeps on its
	// search_path, makes it shared. Best effort: a managed provider may refuse
	// the privilege, which migration 000004 already degrades gracefully around.
	_, _ = admin.Exec(`CREATE EXTENSION IF NOT EXISTS pg_trgm SCHEMA public`)
	if err := admin.Close(); err != nil {
		t.Fatalf("testdb: close admin connection: %v", err)
	}

	// Schema isolation is carried as a `options=-c search_path=...` STARTUP
	// parameter, and a transaction-mode pooler refuses startup parameters it
	// does not proxy ("unsupported startup parameter in options: search_path").
	//
	// That is a limit of this harness, not of flexitype: the product's SQL is
	// transaction-pooling safe, which is the whole point of the pooled CI job.
	// So through a pooler the suites share the default schema and run
	// serially (`go test -p 1`), which is what FLEXITYPE_TEST_SHARED_SCHEMA
	// asks for.
	if os.Getenv("FLEXITYPE_TEST_SHARED_SCHEMA") != "" {
		pool, err := sqlx.Connect("postgres", dsn)
		if err != nil {
			t.Fatalf("testdb: connect: %v", err)
		}
		t.Cleanup(func() { _ = pool.Close() })
		return pool
	}

	pool, err := sqlx.Connect("postgres", withSearchPath(dsn, schema))
	if err != nil {
		t.Fatalf("testdb: connect to schema %s: %v", schema, err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// withSearchPath appends the libpq `options` parameter that pins search_path
// for every connection the pool opens. Setting it per session after connecting
// would only bind one pooled connection.
//
// public comes second so extension objects installed there still resolve —
// pg_trgm's gin_trgm_ops operator class, which migration 000004 needs. Only
// the leading entry receives new tables, so the suite's schema still owns
// every flexitype table it creates.
func withSearchPath(dsn, schema string) string {
	sep := "?"
	if containsRune(dsn, '?') {
		sep = "&"
	}
	return fmt.Sprintf("%s%soptions=-c%%20search_path%%3D%s,public", dsn, sep, schema)
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

// TruncateAll empties every flexitype table in the caller's schema, leaving
// the migration bookkeeping intact. It is scoped to the current schema, so it
// cannot reach another package's data.
func TruncateAll(t *testing.T, pool *sqlx.DB) {
	t.Helper()
	var stmt string
	err := pool.Get(&stmt, `SELECT 'TRUNCATE ' || string_agg(format('%I.%I', schemaname, tablename), ', ') || ' CASCADE'
		FROM pg_tables
		WHERE schemaname = current_schema()
		  AND tablename LIKE 'flexitype_%'
		  AND tablename NOT IN ('flexitype_schema_migrations', 'flexitype_schema_backfill')`)
	if err != nil {
		t.Fatalf("testdb: build truncate: %v", err)
	}
	if stmt == "" {
		return // nothing migrated yet
	}
	if _, err := pool.Exec(stmt); err != nil {
		t.Fatalf("testdb: truncate: %v", err)
	}
}
