package postgres

import (
	"testing"

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
