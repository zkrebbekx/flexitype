package ratelimit

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestLimiter(t *testing.T) {
	Convey("Given a limiter of 10 rps with a burst of 3, on a fixed clock", t, func() {
		now := time.Unix(1_700_000_000, 0)
		l := New(10, 3)
		l.now = func() time.Time { return now }

		Convey("When a key sends a burst up to the ceiling", func() {
			r1, _ := l.Allow("acct")
			r2, _ := l.Allow("acct")
			r3, _ := l.Allow("acct")

			Convey("Then the first burst is allowed", func() {
				So(r1, ShouldBeTrue)
				So(r2, ShouldBeTrue)
				So(r3, ShouldBeTrue)
			})

			Convey("And the next request is rejected with a Retry-After of at least a second", func() {
				ok, retry := l.Allow("acct")
				So(ok, ShouldBeFalse)
				So(retry, ShouldBeGreaterThanOrEqualTo, time.Second)
			})
		})

		Convey("When the bucket is drained then time passes", func() {
			for i := 0; i < 3; i++ {
				l.Allow("acct")
			}
			denied, _ := l.Allow("acct")
			So(denied, ShouldBeFalse)

			now = now.Add(time.Second) // 10 rps → 10 tokens accrue (capped at burst 3)
			allowed, _ := l.Allow("acct")

			Convey("Then requests are allowed again", func() {
				So(allowed, ShouldBeTrue)
			})
		})

		Convey("When two different keys each send a burst", func() {
			a1, _ := l.Allow("a")
			b1, _ := l.Allow("b")

			Convey("Then their buckets are independent", func() {
				So(a1, ShouldBeTrue)
				So(b1, ShouldBeTrue)
				// Drain a; b is untouched.
				l.Allow("a")
				l.Allow("a")
				adenied, _ := l.Allow("a")
				bok, _ := l.Allow("b")
				So(adenied, ShouldBeFalse)
				So(bok, ShouldBeTrue)
			})
		})
	})

	Convey("A nil limiter always allows", t, func() {
		var l *Limiter
		ok, retry := l.Allow("anything")
		So(ok, ShouldBeTrue)
		So(retry, ShouldEqual, time.Duration(0))
	})

	Convey("A non-positive rate or burst disables the limiter", t, func() {
		So(New(0, 10), ShouldBeNil)
		So(New(10, 0), ShouldBeNil)
	})
}
