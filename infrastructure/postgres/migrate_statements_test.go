package postgres

import (
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
			upper := strings.ToUpper(string(body))

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
