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

// Migrate applies embedded up-migrations in order, tracking progress in
// flexitype_schema_migrations. All pending migrations run in a single
// transaction, so a schema upgrade is all-or-nothing; concurrent runners
// serialize on an advisory lock, making it safe to call on every startup
// (and from embedded deployments). Runtime is forward-only —
// down-migrations exist for local development and reversibility testing
// via MigrateDown, and are never applied at startup.
func Migrate(ctx context.Context, tx db.Transactor) error {
	const advisoryLockKey = 0x666c6578 // "flex"

	return tx.InTransaction(ctx, func(tx db.Transactor) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryLockKey); err != nil {
			return fmt.Errorf("acquire migration lock: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`CREATE TABLE IF NOT EXISTS flexitype_schema_migrations (
			   version    INTEGER PRIMARY KEY,
			   applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
			 )`); err != nil {
			return fmt.Errorf("ensure migrations table: %w", err)
		}

		applied := make(map[int]bool)
		var versions []int
		if err := tx.SelectContext(ctx, &versions, `SELECT version FROM flexitype_schema_migrations`); err != nil {
			return fmt.Errorf("read applied migrations: %w", err)
		}
		for _, v := range versions {
			applied[v] = true
		}

		names, err := upMigrations()
		if err != nil {
			return err
		}
		for _, name := range names {
			version, err := migrationVersion(name)
			if err != nil {
				return err
			}
			if applied[version] {
				continue
			}

			sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
			if err != nil {
				return fmt.Errorf("read migration %s: %w", name, err)
			}
			if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
				return fmt.Errorf("apply migration %s: %w", name, err)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO flexitype_schema_migrations (version) VALUES ($1)`, version); err != nil {
				return fmt.Errorf("record migration %s: %w", name, err)
			}
		}
		return nil
	})
}

// MigrateDown reverts applied migrations whose version is greater than
// target, newest first, running each .down.sql and removing its
// schema-migrations row — all in one transaction. It is NOT called at
// startup; use it in local development and reversibility tests. target=0
// reverts everything.
func MigrateDown(ctx context.Context, tx db.Transactor, target int) error {
	const advisoryLockKey = 0x666c6578 // "flex"

	return tx.InTransaction(ctx, func(tx db.Transactor) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryLockKey); err != nil {
			return fmt.Errorf("acquire migration lock: %w", err)
		}

		var versions []int
		if err := tx.SelectContext(ctx, &versions,
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
			if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
				return fmt.Errorf("revert migration %s: %w", down, err)
			}
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM flexitype_schema_migrations WHERE version = $1`, version); err != nil {
				return fmt.Errorf("record reverting %s: %w", name, err)
			}
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
