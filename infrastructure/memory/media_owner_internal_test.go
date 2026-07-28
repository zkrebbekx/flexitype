package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestMediaValueForKeyBreaksTiesByID pins the owner of an object key when two
// values carry the same creation instant.
//
// The owner of a key decides two things: which attribute's field ACL governs
// adoption, and which one governs the download. The winner used to be the
// first row with the earliest created_at, and a tie left the winner to map
// iteration order in memory and to physical row order in SQL. Two values
// written in one batch share a timestamp, so the ACL that applied could
// change between two identical calls.
func TestMediaValueForKeyBreaksTiesByID(t *testing.T) {
	ctx := context.Background()
	tenant := valueobjects.DefaultTenant
	at := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)

	mediaValue := func(key string) valueobjects.Value {
		v, err := valueobjects.ParseValue(valueobjects.DataTypeMedia,
			json.RawMessage(`{"object_key":"`+key+`","mime":"image/png","size":10}`))
		So(err, ShouldBeNil)
		return v
	}

	Convey("Given two media values of one key sharing a creation instant", t, func() {
		store := NewStore()
		repo := &valueRepo{s: store}

		// Ids chosen so the lower one is inserted second: a map iteration that
		// took the first row it saw would answer either way.
		valueID := func(s string) valueobjects.AttributeValueID {
			id, err := valueobjects.ParseAttributeValueID(s)
			So(err, ShouldBeNil)
			return id
		}
		attrID := func(s string) valueobjects.AttributeDefinitionID {
			id, err := valueobjects.ParseAttributeDefinitionID(s)
			So(err, ShouldBeNil)
			return id
		}
		hi := valueID("01ZZZZZZZZZZZZZZZZZZZZZZZZ")
		lo := valueID("01AAAAAAAAAAAAAAAAAAAAAAAA")
		for _, id := range []valueobjects.AttributeValueID{hi, lo} {
			store.values[id.String()] = domainvalue.Snapshot{
				ID:                    id,
				TenantID:              tenant,
				AttributeDefinitionID: attrID(id.String()),
				EntityID:              valueobjects.EntityID("e1"),
				Value:                 mediaValue("k1"),
				CreatedAt:             at,
				UpdatedAt:             at,
			}
		}

		Convey("When the owning value is resolved 20 times", func() {
			owners := map[string]int{}
			for i := 0; i < 20; i++ {
				snap, ok, err := repo.MediaValueForKey(ctx, tenant, "k1")
				So(err, ShouldBeNil)
				So(ok, ShouldBeTrue)
				owners[snap.ID.String()]++
			}

			Convey("Then the lowest id wins every time", func() {
				So(len(owners), ShouldEqual, 1)
				So(owners[lo.String()], ShouldEqual, 20)
			})
		})

		Convey("When an older value of the same key exists", func() {
			older := valueID("01BBBBBBBBBBBBBBBBBBBBBBBB")
			store.values[older.String()] = domainvalue.Snapshot{
				ID:                    older,
				TenantID:              tenant,
				AttributeDefinitionID: attrID(older.String()),
				EntityID:              valueobjects.EntityID("e0"),
				Value:                 mediaValue("k1"),
				CreatedAt:             at.Add(-time.Minute),
				UpdatedAt:             at,
			}
			snap, ok, err := repo.MediaValueForKey(ctx, tenant, "k1")

			Convey("Then age still decides before the id does", func() {
				So(err, ShouldBeNil)
				So(ok, ShouldBeTrue)
				So(snap.ID.String(), ShouldEqual, older.String())
			})
		})
	})
}
