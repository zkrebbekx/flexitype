package postgres

import (
	"regexp"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestParseDirectives(t *testing.T) {
	Convey("Given a migration file header", t, func() {
		Convey("When it carries no directive", func() {
			d, err := parseDirectives("000001_init.up.sql", "-- a comment\nCREATE TABLE t (id int);")

			Convey("Then the migration runs in a transaction, as before", func() {
				So(err, ShouldBeNil)
				So(d.NoTransaction, ShouldBeFalse)
			})
		})

		Convey("When it declares no-transaction", func() {
			d, err := parseDirectives("000018_perf.up.sql",
				"-- Performance indexes.\n--\n-- +flexitype:no-transaction\n--\nCREATE INDEX CONCURRENTLY x ON t (a);")

			Convey("Then the runner sends its statements outside a transaction", func() {
				So(err, ShouldBeNil)
				So(d.NoTransaction, ShouldBeTrue)
			})
		})

		Convey("When it carries a misspelled directive", func() {
			_, err := parseDirectives("000018_perf.up.sql", "-- +flexitype:no_transaction\n")

			Convey("Then it is an error, not a silent fall back to the blocking default", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "unknown directive")
			})
		})
	})
}

func TestSplitStatements(t *testing.T) {
	Convey("Given a migration body that must be sent one statement at a time", t, func() {
		Convey("When it holds plain statements", func() {
			got := splitStatements("CREATE INDEX a ON t (x);\nCREATE INDEX b ON t (y);\n")

			Convey("Then each statement is separate and the semicolons are gone", func() {
				So(got, ShouldHaveLength, 2)
				So(got[0], ShouldEqual, "CREATE INDEX a ON t (x)")
				So(got[1], ShouldEqual, "CREATE INDEX b ON t (y)")
			})
		})

		Convey("When a statement is a dollar-quoted DO block containing semicolons", func() {
			body := `DO $$
BEGIN
    EXECUTE 'DROP INDEX a';
    EXECUTE 'DROP INDEX b';
END $$;
CREATE INDEX CONCURRENTLY a ON t (x);`
			got := splitStatements(body)

			Convey("Then the block stays whole and the trailing statement is separate", func() {
				So(got, ShouldHaveLength, 2)
				So(got[0], ShouldStartWith, "DO $$")
				So(strings.Count(got[0], "EXECUTE"), ShouldEqual, 2)
				So(got[1], ShouldEqual, "CREATE INDEX CONCURRENTLY a ON t (x)")
			})
		})

		Convey("When a body uses a tagged dollar quote", func() {
			body := "CREATE FUNCTION f() RETURNS void AS $fn$ BEGIN PERFORM 1; END $fn$ LANGUAGE plpgsql;\nSELECT 1;"
			got := splitStatements(body)

			Convey("Then the tagged body is not split at its inner semicolon", func() {
				So(got, ShouldHaveLength, 2)
				So(got[0], ShouldContainSubstring, "PERFORM 1;")
				So(got[1], ShouldEqual, "SELECT 1")
			})
		})

		Convey("When a semicolon sits inside a string literal or a comment", func() {
			got := splitStatements("SELECT 'a;b';\n-- trailing; comment\nSELECT 2;")

			Convey("Then neither is treated as a separator", func() {
				So(got, ShouldHaveLength, 2)
				So(got[0], ShouldEqual, "SELECT 'a;b'")
				So(got[1], ShouldContainSubstring, "SELECT 2")
			})
		})

		Convey("When a trailing statement has no semicolon", func() {
			got := splitStatements("SELECT 1;\nSELECT 2")

			Convey("Then it is still returned", func() {
				So(got, ShouldHaveLength, 2)
				So(got[1], ShouldEqual, "SELECT 2")
			})
		})
	})
}

func TestStatementIsEmpty(t *testing.T) {
	Convey("Given a split statement", t, func() {
		Convey("Then one holding only comments and blank lines is empty", func() {
			So(statementIsEmpty("\n-- just a note\n\n"), ShouldBeTrue)
		})

		Convey("Then one holding SQL is not", func() {
			So(statementIsEmpty("-- a note\nSELECT 1"), ShouldBeFalse)
		})
	})
}

// TestEmbeddedMigrationDirectives pins the two properties the runner cannot
// check at run time, because by then the damage is done: a file that builds an
// index on a hot table must declare no-transaction, and a no-transaction file
// must not carry a statement whose partial application would be unrecoverable.
//
// It covers down-migrations too. A down-file dropping an index CONCURRENTLY
// needs the directive exactly as its up-file does, and MigrateDown honours it.
func TestEmbeddedMigrationDirectives(t *testing.T) {
	Convey("Given the embedded migrations", t, func() {
		ups, err := upMigrations()
		So(err, ShouldBeNil)
		downs, err := listMigrations(".down.sql")
		So(err, ShouldBeNil)
		names := append(append([]string{}, ups...), downs...)

		for _, name := range names {
			body, err := migrationsFS.ReadFile("migrations/" + name)
			So(err, ShouldBeNil)
			d, err := parseDirectives(name, string(body))
			So(err, ShouldBeNil)
			// Comments are stripped first. A file that only MENTIONS
			// CONCURRENTLY — to say which migration took its index over —
			// declares nothing to the runner, and reading prose as a statement
			// would force a directive onto a purely transactional file.
			upper := strings.ToUpper(stripComments(string(body)))

			Convey("Then "+name+" declares no-transaction if and only if it uses CONCURRENTLY", func() {
				usesConcurrently := strings.Contains(upper, "CONCURRENTLY")
				So(d.NoTransaction, ShouldEqual, usesConcurrently)
			})

			if d.NoTransaction {
				Convey("Then "+name+" creates no table, because a replay would fail on it", func() {
					// CREATE TABLE has no IF NOT EXISTS guarantee across a
					// partially applied file, and a table is the one object
					// whose duplicate creation cannot be made idempotent by the
					// runner. Schema objects belong in a transactional file.
					So(upper, ShouldNotContainSubstring, "CREATE TABLE")
				})
			}
		}
	})
}

// grandfatheredPlainIndexes are the released files that build an index on a
// table an earlier migration created, without CONCURRENTLY.
//
// They are NOT fixed, deliberately. All of them have shipped, so every
// deployment that would have been hurt has already run them; rewriting an
// applied migration changes nothing for those deployments and only risks a
// checksum mismatch. A fresh database builds these on an empty or small table,
// where the lock is brief.
//
// The list is CLOSED. It exists so the rule below binds every future
// migration, and so the exception is a stated decision rather than a gap. A new
// entry here is the wrong fix — add CONCURRENTLY and the
// +flexitype:no-transaction directive instead.
//
// The list shrank once already. The two outbox entries were removed when #595
// showed the reasoning behind grandfathering them was wrong: "every deployment
// that would be hurt has already run it" holds only for a deployment already
// PAST that version, and a deployment upgrading across it still pays the
// stall. Bookkeeping records the version and no checksum, so correcting an
// applied migration is a no-op for those who have it and a fix for those who
// have not.
//
// It shrank a second time under #611, which split the three files whose index
// builds could not be made concurrent in place. 000004 creates tables and
// 000014 alters two, so neither can be a no-transaction file; both now leave
// the build to a later one (000042, 000041). 000004 and 000021 also built the
// pg_trgm pair plainly because the build had to be conditional on an
// extension and the only conditional form in SQL is a DO block, which is a
// transaction. The condition became a runner directive
// (+flexitype:requires-extension), which let 000041 build them concurrently.
//
// The 000039 entry is the instructive one: it was added while fixing an
// unrelated review finding, in the same stretch of work that documented this
// rule. The directive check passed it, because a plain CREATE INDEX inside a
// transactional file is consistent with itself. That is the gap this test
// closes.
var grandfatheredPlainIndexes = map[string][]string{
	"000003_type_inheritance.up.sql":         {"idx_flexitype_type_definition_extends"},
	"000005_event_delivery.up.sql":           {"idx_flexitype_event_outbox_feed_seq"},
	"000008_outbox_lease.up.sql":             {"idx_flexitype_event_outbox_pending"},
	"000027_role_index.up.sql":               {"idx_flexitype_service_account_roles"},
	"000039_dependency_enforce_index.up.sql": {"idx_dependency_enforced_on_write"},
}

// The comment stripper lives in migrate_statements.go, beside the statement
// scanner it shares its rules with. A second, simpler one here read a `--`
// inside a string literal or a dollar-quoted body as the start of a comment,
// and then hid every rule below from the SQL that followed it.

// grandfathered reports whether a file is allowed to build this index plainly.
func grandfathered(file, index string) bool {
	for _, allowed := range grandfatheredPlainIndexes[file] {
		if allowed == index {
			return true
		}
	}
	return false
}

// TestIndexesOnExistingTablesAreConcurrent enforces the rule docs/upgrades.md
// states and the directive check does not: an index built on a table that
// ALREADY holds data must use CONCURRENTLY.
//
// The existing check asks only whether the no-transaction directive matches
// the SQL, so a plain CREATE INDEX in a transactional file is consistent with
// itself and passes. That is how two of them reached a released version.
//
// It matters because ACCESS EXCLUSIVE queues AHEAD of everything behind it: a
// DDL blocked on one long transaction stalls every reader and writer of that
// table until it completes, and every replica runs Migrate at startup.
func TestIndexesOnExistingTablesAreConcurrent(t *testing.T) {
	Convey("Given the embedded up-migrations in order", t, func() {
		ups, err := upMigrations()
		So(err, ShouldBeNil)

		// Tables created by an EARLIER migration. A table created in the same
		// file is empty by construction, so indexing it costs nothing.
		earlier := map[string]bool{}
		createTable := regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z0-9_]+)`)
		createIndex := regexp.MustCompile(`(?i)CREATE\s+(UNIQUE\s+)?INDEX\s+(CONCURRENTLY\s+)?(?:IF\s+NOT\s+EXISTS\s+)?([a-z0-9_]+)\s+ON\s+([a-z0-9_]+)`)

		for _, name := range ups {
			body, rerr := migrationsFS.ReadFile("migrations/" + name)
			So(rerr, ShouldBeNil)
			// Comments carry example SQL — 000021 spells out the CONCURRENTLY
			// form it cannot use — so scanning them reports statements no
			// server ever sees.
			sql := stripComments(string(body))

			here := map[string]bool{}
			for _, m := range createTable.FindAllStringSubmatch(sql, -1) {
				here[strings.ToLower(m[1])] = true
			}

			for _, m := range createIndex.FindAllStringSubmatch(sql, -1) {
				concurrent := strings.TrimSpace(m[2]) != ""
				index := strings.ToLower(m[3])
				table := strings.ToLower(m[4])
				if concurrent || here[table] || !earlier[table] {
					continue
				}
				Convey("Then "+name+" builds "+index+" on the pre-existing "+table+" concurrently", func() {
					// If this fails on a NEW migration, add CONCURRENTLY and
					// the +flexitype:no-transaction directive. Do not add it to
					// the grandfathered list — that list is closed.
					So(grandfathered(name, index), ShouldBeTrue)
				})
			}

			for table := range here {
				earlier[table] = true
			}
		}

		Convey("Then the grandfathered list names only files that still exist", func() {
			// A stale entry would silently license a future file that reused
			// the name.
			for name := range grandfatheredPlainIndexes {
				_, ferr := migrationsFS.ReadFile("migrations/" + name)
				So(ferr, ShouldBeNil)
			}
		})
	})
}

// TestStripLineComments guards the scanner's input, not the SQL.
//
// A migration explains itself in comments, and 000021 spells out the
// CONCURRENTLY form it cannot use. Scanning that text reports statements no
// server ever executes, which would either fail the build for a file that is
// correct or push someone to grandfather a phantom.
func TestStripComments(t *testing.T) {
	Convey("Given migration text that documents SQL it does not run", t, func() {
		sql := stripComments(strings.Join([]string{
			"-- CREATE INDEX CONCURRENTLY idx_example ON t (a);",
			"CREATE INDEX idx_real ON t (a);",
			"CREATE INDEX idx_trailing ON t (b); -- and a note",
		}, "\n"))

		Convey("Then only the executed statements remain", func() {
			So(sql, ShouldNotContainSubstring, "idx_example")
			So(sql, ShouldContainSubstring, "idx_real")
			So(sql, ShouldContainSubstring, "idx_trailing")
			So(sql, ShouldNotContainSubstring, "and a note")
		})
	})

	Convey("Given a `--` inside a string literal", t, func() {
		// A naive stripper cut here and hid the rest of the file from every
		// rule that scans it, including the one that requires CONCURRENTLY.
		sql := stripComments(strings.Join([]string{
			"SELECT 'a -- b';",
			"CREATE INDEX idx_hidden ON t (a);",
		}, "\n"))

		Convey("Then the literal and the SQL after it both survive", func() {
			So(sql, ShouldContainSubstring, "a -- b")
			So(sql, ShouldContainSubstring, "idx_hidden")
		})
	})

	Convey("Given a `--` inside a dollar-quoted body", t, func() {
		sql := stripComments(strings.Join([]string{
			"DO $$ BEGIN EXECUTE 'x -- y'; END $$;",
			"CREATE INDEX idx_after ON t (a);",
		}, "\n"))

		Convey("Then the body and the SQL after it both survive", func() {
			So(sql, ShouldContainSubstring, "x -- y")
			So(sql, ShouldContainSubstring, "idx_after")
		})
	})
}

// TestMigrationsDoNotQueryTheCatalogueBlind stops a namespace-blind catalogue
// guard reaching a release for a third time.
//
// A DO block that matches pg_class.relname without a pg_namespace join sees
// every schema in the database, and the unqualified DROP INDEX it then runs
// resolves through search_path. In a deployment with one schema per tenant —
// which the migration runner explicitly supports — an invalid namesake in a
// SIBLING schema makes the guard fire here: either into a missing index
// (42704, a boot loop under MigrateOnStart) or into this schema's own valid
// index, dropping it under live writes.
//
// #517 removed such a block from 000030. It came back in 000034. The runner
// already reaps invalid indexes before replaying a no-transaction file, scoped
// to current_schema(), so a migration never needs its own guard.
func TestMigrationsDoNotQueryTheCatalogueBlind(t *testing.T) {
	Convey("Given the embedded migrations", t, func() {
		ups, err := upMigrations()
		So(err, ShouldBeNil)
		downs, err := listMigrations(".down.sql")
		So(err, ShouldBeNil)

		for _, name := range append(append([]string{}, ups...), downs...) {
			body, rerr := migrationsFS.ReadFile("migrations/" + name)
			So(rerr, ShouldBeNil)
			sql := stripComments(string(body))
			if !strings.Contains(sql, "pg_class") {
				continue
			}

			Convey("Then "+name+" scopes its catalogue lookup to one schema", func() {
				// pg_namespace is how a lookup names the schema it means. Any
				// other way of getting there is welcome to relax this, but it
				// should be a deliberate change, not a copied block.
				So(strings.Contains(sql, "pg_namespace"), ShouldBeTrue)
			})
		}
	})
}
