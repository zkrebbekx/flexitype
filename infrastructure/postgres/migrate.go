package postgres

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/zkrebbekx/flexitype/pkg/db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// advisoryLockKey serializes concurrent migration runners over the schema they
// are migrating. Every replica calls Migrate at startup, so one of them applies
// and the rest wait.
//
// The key is derived from current_schema() rather than being a constant,
// because the lock protects one schema's migrations. Two flexitype schemas in
// one database — separate deployments, or the per-package schemas the test
// suites use — otherwise serialize against each other for no reason, and can
// deadlock through the objects a migration shares with them (pg_trgm lives in
// public and is created by migration 000004).
const advisoryLockKey = `hashtext(current_schema() || ':flexitype-migrate')`

// Migrate applies embedded up-migrations in order, tracking progress in
// flexitype_schema_migrations, then runs any pending data backfills. It is safe
// to call on every startup and from embedded deployments: concurrent runners
// serialize on an advisory lock. Runtime is forward-only — down-migrations
// exist for local development and reversibility testing via MigrateDown, and
// are never applied at startup.
//
// Each migration is applied in its own transaction, not all of them in one.
// The all-or-nothing form cannot coexist with the statements a large
// deployment needs: CREATE INDEX CONCURRENTLY is rejected inside a transaction
// block, and a data backfill held inside the schema transaction keeps its DDL
// locks for the whole scan — which is how a whole-table backfill came to block
// every value write in the fleet for the duration of the upgrade.
//
// A migration file may carry directives in its header:
//
//	-- +flexitype:no-transaction
//
// which runs its statements one at a time outside any transaction. Statements
// in such a file must be idempotent, because a failure part-way leaves the
// earlier ones applied and the file unrecorded.
//
// See docs/upgrades.md for the rolling-upgrade contract and the rules for
// writing a migration that is safe against a live fleet.
func Migrate(ctx context.Context, tx db.Transactor) error {
	conn, ok := tx.(db.SessionConnector)
	if !ok {
		// A transactor that cannot pin a connection cannot hold a session
		// advisory lock or run a statement outside a transaction. Fall back to
		// the single-transaction form, which is correct for every migration
		// that carries no directive.
		return migrateInOneTransaction(ctx, tx)
	}
	return conn.WithConn(ctx, func(q db.QueryExecer) error {
		// A session-scoped lock, so it spans the per-migration transactions and
		// the statements that run outside them. It is released explicitly and
		// again by the connection returning to the pool.
		if _, err := q.ExecContext(ctx, `SELECT pg_advisory_lock(`+advisoryLockKey+`)`); err != nil {
			return fmt.Errorf("acquire migration lock: %w", err)
		}
		defer func() { _, _ = q.ExecContext(ctx, `SELECT pg_advisory_unlock(`+advisoryLockKey+`)`) }()

		if err := ensureMigrationTables(ctx, q); err != nil {
			return err
		}
		if err := applyPending(ctx, q); err != nil {
			return err
		}
		return runBackfills(ctx, q)
	})
}

// migrateInOneTransaction is the fallback for a transactor with no pinned
// connection. It applies every pending migration in one transaction and
// refuses a migration that declares it must run outside one, rather than
// applying it in the form its author ruled out.
func migrateInOneTransaction(ctx context.Context, tx db.Transactor) error {
	return tx.InTransaction(ctx, func(tx db.Transactor) error {
		q := txExecer(tx)
		if _, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(`+advisoryLockKey+`)`); err != nil {
			return fmt.Errorf("acquire migration lock: %w", err)
		}
		if err := ensureMigrationTables(ctx, q); err != nil {
			return err
		}
		pending, err := pendingMigrations(ctx, q)
		if err != nil {
			return err
		}
		for _, m := range pending {
			if m.directives.NoTransaction {
				return fmt.Errorf("migration %s declares no-transaction but this transactor cannot pin a connection", m.name)
			}
			if _, err := q.ExecContext(ctx, m.body); err != nil {
				return fmt.Errorf("apply migration %s: %w", m.name, err)
			}
			if err := recordMigration(ctx, q, m.version); err != nil {
				return err
			}
		}
		return runBackfills(ctx, q)
	})
}

// ensureMigrationTables creates the two runner bookkeeping tables.
func ensureMigrationTables(ctx context.Context, q db.QueryExecer) error {
	if _, err := q.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS flexitype_schema_migrations (
		   version    INTEGER PRIMARY KEY,
		   applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		 )`); err != nil {
		return fmt.Errorf("ensure migrations table: %w", err)
	}
	// Backfills are tracked separately from schema versions: a backfill is
	// resumable and may span many runs, so "the schema is at version N" and
	// "the data behind version N has caught up" are different facts.
	if _, err := q.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS flexitype_schema_backfill (
		   name         TEXT PRIMARY KEY,
		   completed_at TIMESTAMPTZ NOT NULL DEFAULT now()
		 )`); err != nil {
		return fmt.Errorf("ensure backfill table: %w", err)
	}
	return nil
}

// migration is one embedded up-migration, parsed.
type migration struct {
	name       string
	version    int
	body       string
	directives migrationDirectives
}

// pendingMigrations returns the unapplied up-migrations in version order.
func pendingMigrations(ctx context.Context, q db.QueryExecer) ([]migration, error) {
	var versions []int
	if err := q.SelectContext(ctx, &versions, `SELECT version FROM flexitype_schema_migrations`); err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	applied := make(map[int]bool, len(versions))
	for _, v := range versions {
		applied[v] = true
	}

	names, err := upMigrations()
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return nil, err
		}
		if applied[version] {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		directives, err := parseDirectives(name, string(body))
		if err != nil {
			return nil, err
		}
		out = append(out, migration{name: name, version: version, body: string(body), directives: directives})
	}
	return out, nil
}

// applyPending applies each pending migration on the pinned connection, in its
// own transaction unless the file declares otherwise.
func applyPending(ctx context.Context, q db.QueryExecer) error {
	pending, err := pendingMigrations(ctx, q)
	if err != nil {
		return err
	}
	for _, m := range pending {
		if m.directives.NoTransaction {
			if err := applyOutsideTransaction(ctx, q, m); err != nil {
				return err
			}
			continue
		}
		if err := applyInTransaction(ctx, q, m); err != nil {
			return err
		}
	}
	return nil
}

// applyInTransaction applies one migration and records it atomically.
func applyInTransaction(ctx context.Context, q db.QueryExecer, m migration) (err error) {
	if _, err := q.ExecContext(ctx, `BEGIN`); err != nil {
		return fmt.Errorf("begin migration %s: %w", m.name, err)
	}
	defer func() {
		if err != nil {
			_, _ = q.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	if _, err = q.ExecContext(ctx, m.body); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.name, err)
	}
	if err = recordMigration(ctx, q, m.version); err != nil {
		return err
	}
	if _, err = q.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.name, err)
	}
	return nil
}

// applyOutsideTransaction runs a no-transaction migration one statement at a
// time, then records it. The record is not atomic with the statements — that
// is the price of running outside a transaction — so every statement in such a
// file must be idempotent, and the runner replays the whole file if it is
// interrupted.
func applyOutsideTransaction(ctx context.Context, q db.QueryExecer, m migration) error {
	for _, stmt := range splitStatements(m.body) {
		if statementIsEmpty(stmt) {
			continue
		}
		if _, err := q.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
	}
	return recordMigration(ctx, q, m.version)
}

// recordMigration marks a version applied.
func recordMigration(ctx context.Context, q db.QueryExecer, version int) error {
	if _, err := q.ExecContext(ctx,
		`INSERT INTO flexitype_schema_migrations (version) VALUES ($1)
		 ON CONFLICT (version) DO NOTHING`, version); err != nil {
		return fmt.Errorf("record migration %06d: %w", version, err)
	}
	return nil
}

// KnownSchemaVersion is the highest migration version this binary carries.
// Compare it against the database to detect a mixed-version fleet.
func KnownSchemaVersion() (int, error) {
	names, err := upMigrations()
	if err != nil {
		return 0, err
	}
	highest := 0
	for _, name := range names {
		v, err := migrationVersion(name)
		if err != nil {
			return 0, err
		}
		if v > highest {
			highest = v
		}
	}
	return highest, nil
}

// UnknownSchemaVersions returns the applied versions the binary does not
// carry — the schema is newer than this build. It is how a rolling deploy
// makes a mixed-version fleet visible: the previous generation keeps serving
// against a schema the next generation migrated, which flexitype supports
// (each release's migrations stay compatible with the previous binary), but an
// operator should still be able to see that it is happening.
//
// It returns nothing when the migrations table does not exist yet.
func UnknownSchemaVersions(ctx context.Context, q db.QueryExecer) ([]int, error) {
	// to_regclass resolves through search_path, so this asks whether the table
	// exists in the schema this connection actually writes to — not whether
	// some other schema in the database happens to have one.
	var exists bool
	if err := q.GetContext(ctx, &exists,
		`SELECT to_regclass('flexitype_schema_migrations') IS NOT NULL`); err != nil {
		return nil, fmt.Errorf("probe migrations table: %w", err)
	}
	if !exists {
		return nil, nil
	}
	known, err := KnownSchemaVersion()
	if err != nil {
		return nil, err
	}
	var newer []int
	if err := q.SelectContext(ctx, &newer,
		`SELECT version FROM flexitype_schema_migrations WHERE version > $1 ORDER BY version`, known); err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	return newer, nil
}

// MigrateDown reverts applied migrations whose version is greater than
// target, newest first, running each .down.sql and removing its
// schema-migrations row — all in one transaction. It is NOT called at
// startup; use it in local development and reversibility tests. target=0
// reverts everything.
//
// Rollback of a deployed release is redeploying the previous binary, not
// running this: see docs/upgrades.md.
func MigrateDown(ctx context.Context, tx db.Transactor, target int) error {
	return tx.InTransaction(ctx, func(tx db.Transactor) error {
		q := txExecer(tx)
		if _, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(`+advisoryLockKey+`)`); err != nil {
			return fmt.Errorf("acquire migration lock: %w", err)
		}

		var versions []int
		if err := q.SelectContext(ctx, &versions,
			`SELECT version FROM flexitype_schema_migrations WHERE version > $1 ORDER BY version DESC`,
			target); err != nil {
			return fmt.Errorf("read applied migrations: %w", err)
		}

		for _, version := range versions {
			name := fmt.Sprintf("%06d", version)
			down, err := downMigration(version)
			if err != nil {
				return err
			}
			sqlBytes, err := migrationsFS.ReadFile("migrations/" + down)
			if err != nil {
				return fmt.Errorf("read down migration %s: %w", down, err)
			}
			if _, err := q.ExecContext(ctx, string(sqlBytes)); err != nil {
				return fmt.Errorf("revert migration %s: %w", down, err)
			}
			if _, err := q.ExecContext(ctx,
				`DELETE FROM flexitype_schema_migrations WHERE version = $1`, version); err != nil {
				return fmt.Errorf("record reverting %s: %w", name, err)
			}
		}
		// A reverted schema must re-run its backfills if it is re-applied.
		if _, err := q.ExecContext(ctx, `DELETE FROM flexitype_schema_backfill`); err != nil {
			return fmt.Errorf("clear backfill records: %w", err)
		}
		return nil
	})
}

// upMigrations lists embedded up-migrations in version order.
func upMigrations() ([]string, error) {
	return listMigrations(".up.sql")
}

// downMigration finds the .down.sql for one version.
func downMigration(version int) (string, error) {
	downs, err := listMigrations(".down.sql")
	if err != nil {
		return "", err
	}
	for _, name := range downs {
		if v, err := migrationVersion(name); err == nil && v == version {
			return name, nil
		}
	}
	return "", fmt.Errorf("no down migration for version %d", version)
}

func listMigrations(suffix string) ([]string, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// migrationVersion parses the numeric prefix of "000001_init.up.sql".
func migrationVersion(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("malformed migration name %q", name)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("malformed migration version in %q: %w", name, err)
	}
	return version, nil
}
