package postgres_test

import (
	"context"
	"testing"
	"time"

	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"

	"github.com/zkrebbekx/flexitype/internal/testdb"
)

// TestMediaValueForKeyTieBreakIntegration proves the owner of an object key is
// decided deterministically when two values share a creation instant.
//
// The owner decides which attribute's field ACL governs adoption and which one
// governs the download. ORDER BY created_at alone left a tie to the physical
// row order, so the ACL that applied could change after any UPDATE or VACUUM
// moved a row. Two values written in one batch share a timestamp, so the tie
// is an ordinary state, not a corner case.
func TestMediaValueForKeyTieBreakIntegration(t *testing.T) {
	pool := openIntegrationDB(t)
	defer func() { _ = pool.Close() }()
	ctx := context.Background()

	if err := postgres.Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	typeID := valueobjects.NewTypeDefinitionID()
	attrID := ulid.New().String()
	repo := postgres.NewAttributeValueRepository(pool)
	at := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)

	insertMedia := func(id, entity string, created time.Time) {
		pool.MustExec(`
			INSERT INTO flexitype_attribute_value
			    (id, tenant_id, type_definition_id, attribute_definition_id, entity_id, data_type,
			     value_json, definition_version, created_at, updated_at)
			VALUES ($1, 'default', $2, $3, $4, 'media',
			        '{"object_key":"k1","mime":"image/png","size":10}'::jsonb, 1, $5, $5)`,
			id, typeID.String(), attrID, entity, created)
	}

	Convey("Given two media values of one object key sharing a creation instant", t, func() {
		testdb.TruncateTablesCascade(t, pool, "flexitype_attribute_value", "flexitype_entity_summary", "flexitype_attribute_definition", "flexitype_type_definition")
		seedSummarySchema(t, pool, typeID.String(), attrID)
		hi := "01ZZZZZZZZZZZZZZZZZZZZZZZZ"
		lo := "01AAAAAAAAAAAAAAAAAAAAAAAA"
		insertMedia(hi, "e1", at)
		insertMedia(lo, "e2", at)

		Convey("When the owning value is resolved after the higher id is updated", func() {
			// An UPDATE moves the row, so a physical-order answer changes here.
			pool.MustExec(`UPDATE flexitype_attribute_value SET updated_at = now() WHERE id = $1`, hi)
			snap, ok, err := repo.MediaValueForKey(ctx, valueobjects.DefaultTenant, "k1")

			Convey("Then the lowest id owns the key", func() {
				So(err, ShouldBeNil)
				So(ok, ShouldBeTrue)
				So(snap.ID.String(), ShouldEqual, lo)
			})
		})

		Convey("When an older value of the same key exists", func() {
			older := "01BBBBBBBBBBBBBBBBBBBBBBBB"
			insertMedia(older, "e0", at.Add(-time.Minute))
			snap, ok, err := repo.MediaValueForKey(ctx, valueobjects.DefaultTenant, "k1")

			Convey("Then age decides before the id does", func() {
				So(err, ShouldBeNil)
				So(ok, ShouldBeTrue)
				So(snap.ID.String(), ShouldEqual, older)
			})
		})
	})
}
