package postgres

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"
)

// TestMigrationsPaired keeps every up-migration matched by a down-migration
// so MigrateDown can revert the full chain. It needs no database — it
// guards the embedded file set that reversibility depends on.
func TestMigrationsPaired(t *testing.T) {
	Convey("Given the embedded migrations", t, func() {
		ups, err := listMigrations(".up.sql")
		So(err, ShouldBeNil)
		So(len(ups), ShouldBeGreaterThan, 0)

		Convey("Then every up-migration has a matching down-migration", func() {
			for _, up := range ups {
				version, err := migrationVersion(up)
				So(err, ShouldBeNil)
				down, err := downMigration(version)
				So(err, ShouldBeNil)
				So(down, ShouldEndWith, ".down.sql")
			}
		})
	})
}

// TestIsConcurrentCreate covers the errors a concurrent CREATE TABLE IF NOT
// EXISTS produces.
//
// The statement is not atomic against another session running the same one:
// both pass the existence check and the loser fails in a catalogue index.
// That is what broke six replicas starting together, before any lock could be
// taken — it is the step that creates the table the lock lives in.
func TestIsConcurrentCreate(t *testing.T) {
	Convey("Given an error from CREATE TABLE IF NOT EXISTS", t, func() {
		Convey("When another session created the object first", func() {
			Convey("Then it is recognised, whichever way Postgres reports it", func() {
				So(isConcurrentCreate(&pq.Error{Code: "23505"}), ShouldBeTrue) // catalogue index
				So(isConcurrentCreate(&pq.Error{Code: "42P07"}), ShouldBeTrue) // duplicate_table
				So(isConcurrentCreate(&pq.Error{Code: "42710"}), ShouldBeTrue) // duplicate_object
			})

			Convey("Then it is recognised through a wrapper", func() {
				So(isConcurrentCreate(fmt.Errorf("ensure: %w", &pq.Error{Code: "42P07"})), ShouldBeTrue)
			})
		})

		Convey("When it is a real failure", func() {
			Convey("Then it is NOT swallowed: only a concurrent create passes", func() {
				So(isConcurrentCreate(&pq.Error{Code: "42501"}), ShouldBeFalse) // insufficient_privilege
				So(isConcurrentCreate(&pq.Error{Code: "53300"}), ShouldBeFalse) // too_many_connections
				So(isConcurrentCreate(errors.New("connection refused")), ShouldBeFalse)
				So(isConcurrentCreate(nil), ShouldBeFalse)
			})
		})
	})
}

func TestLeaseHolderID(t *testing.T) {
	Convey("Given a runner naming itself in the lock row", t, func() {
		Convey("When two ids are minted", func() {
			a, b := leaseHolderID(), leaseHolderID()

			Convey("Then each is unique, so one runner cannot renew another's lease", func() {
				So(a, ShouldNotEqual, b)
			})

			Convey("Then each names a host, so a held lease says who has it", func() {
				So(strings.Contains(a, "/"), ShouldBeTrue)
				So(strings.Split(a, "/")[0], ShouldNotEqual, "")
			})
		})
	})
}
