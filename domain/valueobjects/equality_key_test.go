package valueobjects

import (
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// TestEqualityKeyAgreesWithEqual pins the property the key exists for: two
// values are Equal if and only if their EqualityKeys match. A grouping keyed
// on it therefore counts exactly the duplicates Equal would find, which is
// what keeps the make-unique guard and the write path from disagreeing.
func TestEqualityKeyAgreesWithEqual(t *testing.T) {
	Convey("Given values that are equal but render differently", t, func() {
		Convey("Then a decimal's trailing zeros do not change its key", func() {
			a, err := NewDecimalValue("1.5")
			So(err, ShouldBeNil)
			b, err := NewDecimalValue("1.50")
			So(err, ShouldBeNil)
			So(a.Equal(b), ShouldBeTrue)
			So(a.EqualityKey(), ShouldEqual, b.EqualityKey())
			So(a.String(), ShouldNotEqual, b.String()) // the rendering, the old key, differed
		})

		Convey("Then two quantities with one base magnitude share a key", func() {
			a, err := NewQuantityValue("5", "kg", 5000)
			So(err, ShouldBeNil)
			b, err := NewQuantityValue("5000", "g", 5000)
			So(err, ShouldBeNil)
			So(a.Equal(b), ShouldBeTrue)
			So(a.EqualityKey(), ShouldEqual, b.EqualityKey())
			So(a.String(), ShouldNotEqual, b.String())
		})

		Convey("Then json key order does not change the key", func() {
			a, err := ParseValue(DataTypeJSON, json.RawMessage(`{"a":1,"b":2}`))
			So(err, ShouldBeNil)
			b, err := ParseValue(DataTypeJSON, json.RawMessage(`{"b":2,"a":1}`))
			So(err, ShouldBeNil)
			So(a.Equal(b), ShouldBeTrue)
			So(a.EqualityKey(), ShouldEqual, b.EqualityKey())
		})

		Convey("Then one instant carried in two zones shares a key", func() {
			utc := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
			east := utc.In(time.FixedZone("east", 5*3600))
			a := Value{dataType: DataTypeDateTime, timeVal: utc}
			b := Value{dataType: DataTypeDateTime, timeVal: east}
			So(a.Equal(b), ShouldBeTrue)
			So(a.EqualityKey(), ShouldEqual, b.EqualityKey())
		})

		Convey("Then a positive and a negative zero share a key, as Equal does", func() {
			z := 0.0
			a, b := NewFloatValue(0), NewFloatValue(-z)
			So(a.Equal(b), ShouldBeTrue)
			So(a.EqualityKey(), ShouldEqual, b.EqualityKey())
		})
	})

	Convey("Given values that are not equal", t, func() {
		Convey("Then distinct decimals keep distinct keys", func() {
			a, err := NewDecimalValue("1.5")
			So(err, ShouldBeNil)
			b, err := NewDecimalValue("2.5")
			So(err, ShouldBeNil)
			So(a.Equal(b), ShouldBeFalse)
			So(a.EqualityKey(), ShouldNotEqual, b.EqualityKey())
		})

		Convey("Then distinct integers keep distinct keys", func() {
			a, b := NewIntegerValue(1), NewIntegerValue(2)
			So(a.Equal(b), ShouldBeFalse)
			So(a.EqualityKey(), ShouldNotEqual, b.EqualityKey())
		})

		Convey("Then quantities with different base magnitudes keep distinct keys", func() {
			a, err := NewQuantityValue("5", "kg", 5000)
			So(err, ShouldBeNil)
			b, err := NewQuantityValue("6", "kg", 6000)
			So(err, ShouldBeNil)
			So(a.Equal(b), ShouldBeFalse)
			So(a.EqualityKey(), ShouldNotEqual, b.EqualityKey())
		})

		Convey("Then values of different types never share a key", func() {
			a, b := NewStringValue("1"), NewIntegerValue(1)
			So(a.Equal(b), ShouldBeFalse)
			So(a.EqualityKey(), ShouldNotEqual, b.EqualityKey())
		})
	})
}
