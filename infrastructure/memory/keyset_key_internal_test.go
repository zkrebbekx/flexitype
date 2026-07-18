package memory

import (
	"sort"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/pkg/db"
)

// TestEntityKeyAfterIsMonotonic pins the in-memory keyset comparison against a
// silent row-skipping regression.
//
// entityKey used to render its timestamp with time.RFC3339Nano, which strips
// trailing zeros. keyAfter compares those renderings as strings, so a shorter
// fraction ended at the trailing 'Z' (0x5A) where a longer one still had a
// digit (0x30-0x39) — making an OLDER row compare as newer. paginate feeds
// keyAfter to sort.Search, which REQUIRES a monotonic predicate, so the broken
// ordering sent the binary search into the wrong region and returned an empty
// page: rows vanished from the listing entirely.
func TestEntityKeyAfterIsMonotonic(t *testing.T) {
	base := time.Date(2026, 7, 18, 6, 4, 4, 0, time.UTC)
	// Exactly the values that reproduced the skip: e1's microseconds end in a
	// zero (so RFC3339Nano shortened it), the others do not.
	at := func(micros int) time.Time { return base.Add(time.Duration(micros) * time.Microsecond) }

	Convey("Given entities ordered newest-first with the in-memory entity key", t, func() {
		type row struct {
			id string
			ts time.Time
		}
		rows := []row{
			{"e4", at(365833)},
			{"e3", at(365821)},
			{"e2", at(365812)},
			{"e1", at(365800)}, // trailing-zero microseconds — the poison value
		}
		desc := []bool{true, false} // last_updated DESC, entity_id ASC
		keyOf := func(r row) []string { return entityKey(r.ts, r.id) }

		Convey("The rows are already in list order", func() {
			sorted := sort.SliceIsSorted(rows, func(i, j int) bool {
				return rows[i].ts.After(rows[j].ts)
			})
			So(sorted, ShouldBeTrue)
		})

		Convey("Then keyAfter is monotonic across the list for every cursor", func() {
			// sort.Search requires: once the predicate is true it stays true.
			for c := range rows {
				cursor := keyOf(rows[c])
				seenTrue := false
				for i := range rows {
					got := keyAfter(keyOf(rows[i]), cursor, desc)
					if got {
						seenTrue = true
					} else if seenTrue {
						t.Fatalf("keyAfter non-monotonic: cursor=%s flipped back to false at %s",
							rows[c].id, rows[i].id)
					}
				}
			}
		})

		Convey("And paging from each cursor yields exactly the rows after it — none skipped", func() {
			for c := range rows {
				cursor := db.EncodeKeyset(keyOf(rows[c])...)
				page, _ := paginate(rows, db.Page{Limit: 10, Cursor: cursor}, keyOf, desc...)

				var got []string
				for _, r := range page {
					got = append(got, r.id)
				}
				var want []string
				for _, r := range rows[c+1:] {
					want = append(want, r.id)
				}
				So(got, ShouldResemble, want)
			}
		})
	})
}
