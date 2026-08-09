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
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	// The tests that use this package drive the lib/pq-backed repositories.
	"github.com/lib/pq"
)

// maxTruncateAttempts bounds the deadlock retry in TruncateAll. Each losing
// attempt costs about deadlock_timeout (1s by default), so this is seconds,
// not the sum of the backoffs.
const maxTruncateAttempts = 5

// deadlockDetected is SQLSTATE 40P01.
const deadlockDetected = "40P01"

// lockNotAvailable is SQLSTATE 55P03, what lock_timeout raises.
const lockNotAvailable = "55P03"

// truncateLockTimeout bounds how long the truncate waits for its locks. Long
// enough that a busy-but-progressing worker finishes, short enough that a
// stuck one is reported rather than hanging the package.
const truncateLockTimeout = "2s"

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

// TruncateTables empties the named tables in the caller's schema, with the
// same lock discipline TruncateAll uses: a deterministic order, a bounded wait
// and a retry when it loses a race to a background worker.
//
// It exists because most truncates in this repo were hand-written
// pool.MustExec calls, which got none of that — and MustExec PANICS, so a
// deadlock there was a panic with no explanation rather than a named test
// failure. Prefer TruncateAll; use this when a test deliberately empties a
// subset.
//
// Names are sorted here, so two callers listing the same tables in different
// orders cannot take their locks in opposite orders.
func TruncateTables(t *testing.T, pool *sqlx.DB, tables ...string) {
	t.Helper()
	truncate(t, pool, tables, false)
}

// TruncateTablesCascade is TruncateTables with CASCADE, for a table another
// one references.
//
// The two are separate because CASCADE is not a formality: it empties every
// table with a foreign key onto the named ones, which is more than a caller
// asking for one table usually means. A call site keeps whichever its SQL had.
func TruncateTablesCascade(t *testing.T, pool *sqlx.DB, tables ...string) {
	t.Helper()
	truncate(t, pool, tables, true)
}

func truncate(t *testing.T, pool *sqlx.DB, tables []string, cascade bool) {
	t.Helper()
	if len(tables) == 0 {
		return
	}
	stmt, err := truncateStatement(tables, cascade)
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	truncateWithRetry(t, pool, stmt)
}

// truncateStatement builds the statement for a table list, sorted so two
// callers naming the same tables in different orders cannot take their locks
// in opposite orders.
//
// The names reach the SQL by concatenation, because TRUNCATE takes no
// placeholders, so each is checked to be a bare identifier first.
func truncateStatement(tables []string, cascade bool) (string, error) {
	sorted := append([]string(nil), tables...)
	sort.Strings(sorted)
	for _, name := range sorted {
		if !tableName.MatchString(name) {
			return "", fmt.Errorf("refusing to truncate %q: not a plain table name", name)
		}
	}
	stmt := "TRUNCATE " + strings.Join(sorted, ", ")
	if cascade {
		stmt += " CASCADE"
	}
	return stmt, nil
}

// tableName accepts only a bare identifier, so a caller cannot smuggle SQL
// through a table list.
var tableName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// TruncateAll empties every flexitype table in the caller's schema, leaving
// the migration bookkeeping intact. It is scoped to the current schema, so it
// cannot reach another package's data.
func TruncateAll(t *testing.T, pool *sqlx.DB) {
	t.Helper()
	var stmt string
	// ORDER BY makes the lock order deterministic: TRUNCATE takes an ACCESS
	// EXCLUSIVE lock on every table it names, in the order it names them, and
	// string_agg without ORDER BY returns whatever order the scan produced.
	//
	// This is hygiene, not the fix. No test here runs in parallel and each
	// package truncates its own schema, so two concurrent truncates do not
	// arise in CI — they arise when a developer runs two test binaries at
	// once, which is the case this settles.
	err := pool.Get(&stmt, `SELECT 'TRUNCATE ' || string_agg(format('%I.%I', schemaname, tablename), ', ' ORDER BY tablename) || ' CASCADE'
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
	// The deadlock this retries is against a BACKGROUND WORKER, and the worker
	// that causes it cannot be stopped by a test: a schema change schedules a
	// recompute on a context deliberately detached from every caller
	// (flexitype.go, schemaChangeRecomputer), which starts after a settle
	// delay — often once the test that triggered it has returned, landing in
	// the next test's truncate. Service exposes no way to await it.
	//
	// So this is a stopgap for a product gap, and worth removing once Service
	// can be drained. Retrying is sound in the meantime: the loser of a
	// deadlock has already rolled back, and the worker it lost to is
	// finishing.
	//
	// Matched on SQLSTATE rather than the message, because lib/pq's Error()
	// returns the server's LOCALIZED primary message — the same match against
	// a server with a non-English lc_messages silently stops working. The
	// repo's migration runner already does it this way.
	truncateWithRetry(t, pool, stmt)
}

// truncateWithRetry runs one TRUNCATE statement under the lock discipline both
// entry points share.
func truncateWithRetry(t *testing.T, pool *sqlx.DB, stmt string) {
	t.Helper()
	for attempt := 0; ; attempt++ {
		err := truncateOnce(pool, stmt)
		if err == nil {
			return
		}
		var pqErr *pq.Error
		retryable := errors.As(err, &pqErr) &&
			(pqErr.Code == deadlockDetected || pqErr.Code == lockNotAvailable)
		if attempt == maxTruncateAttempts-1 || !retryable {
			// Detail carries "Process X waits for … blocked by process Y",
			// which names the other side. Error() alone does not, and that is
			// the field that identifies which worker leaked.
			if errors.As(err, &pqErr) && pqErr.Detail != "" {
				t.Fatalf("testdb: truncate: %v (detail: %s)", err, pqErr.Detail)
			}
			t.Fatalf("testdb: truncate: %v", err)
		}
		t.Logf("testdb: truncate could not take its locks (%s, attempt %d/%d), retrying: %s",
			pqErr.Code, attempt+1, maxTruncateAttempts, pqErr.Detail)
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
}

// truncateOnce runs the statement under a lock timeout, so a lock it cannot
// take fails instead of waiting.
//
// Without one, TRUNCATE waits indefinitely for its ACCESS EXCLUSIVE locks. A
// worker that holds a conflicting lock WITHOUT forming a cycle is not a
// deadlock, so Postgres never intervenes: the package instead dies on the CI
// -timeout with a goroutine dump, which says nothing about the cause. A
// timeout turns that into a bounded, named, retryable failure.
//
// SET LOCAL needs a transaction, which also makes the timeout scoped: the
// pooled connection goes back to the pool with its own settings.
func truncateOnce(pool *sqlx.DB, stmt string) error {
	tx, err := pool.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SET LOCAL lock_timeout = '` + truncateLockTimeout + `'`); err != nil {
		return err
	}
	if _, err := tx.Exec(stmt); err != nil {
		return err
	}
	return tx.Commit()
}
