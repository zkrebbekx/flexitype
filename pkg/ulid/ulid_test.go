package ulid

import (
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestULID(t *testing.T) {
	Convey("Given the ULID generator", t, func() {
		Convey("When generating IDs", func() {
			a := New()
			b := New()

			Convey("Then they are unique, non-zero and canonical length", func() {
				So(a.String(), ShouldNotEqual, b.String())
				So(a.IsZero(), ShouldBeFalse)
				So(len(a.String()), ShouldEqual, 26)
			})

			Convey("Then generation is monotonic within the run", func() {
				So(a.Compare(b), ShouldEqual, -1)
			})
		})

		Convey("When parsing", func() {
			id := New()
			parsed, err := Parse(id.String())

			Convey("Then valid strings round-trip and invalid ones fail", func() {
				So(err, ShouldBeNil)
				So(parsed.String(), ShouldEqual, id.String())

				_, err := Parse("definitely-not-a-ulid")
				So(err, ShouldNotBeNil)
				So(func() { MustParse("nope") }, ShouldPanic)
			})
		})

		Convey("When extracting the timestamp", func() {
			before := time.Now().Add(-time.Second)
			id := New()
			after := time.Now().Add(time.Second)

			Convey("Then it falls within the generation window", func() {
				So(id.Time().After(before), ShouldBeTrue)
				So(id.Time().Before(after), ShouldBeTrue)
			})
		})

		Convey("When travelling through SQL", func() {
			id := New()
			v, err := id.Value()
			So(err, ShouldBeNil)
			So(v, ShouldEqual, id.String())

			var scanned ID
			So(scanned.Scan(id.String()), ShouldBeNil)
			So(scanned.String(), ShouldEqual, id.String())

			Convey("Then zero maps to NULL in both directions", func() {
				var zero ID
				nullV, err := zero.Value()
				So(err, ShouldBeNil)
				So(nullV, ShouldBeNil)

				var fromNull ID
				So(fromNull.Scan(nil), ShouldBeNil)
				So(fromNull.IsZero(), ShouldBeTrue)
			})
		})

		Convey("When travelling through JSON", func() {
			id := New()
			encoded, err := json.Marshal(id)
			So(err, ShouldBeNil)
			So(string(encoded), ShouldEqual, `"`+id.String()+`"`)

			var decoded ID
			Convey("Then it round-trips", func() {
				So(json.Unmarshal(encoded, &decoded), ShouldBeNil)
				So(decoded.String(), ShouldEqual, id.String())
			})
		})
	})
}
