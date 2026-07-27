package events_test

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/pkg/events"
)

// TestSecretRotationWindow pins where the rotation grace window lives.
//
// The design doc claimed the sender kept two secrets "both signing-valid",
// and a subscription stored a previous_secret to that end. It was never read
// when signing: a delivery carries exactly one signature, computed with the
// current secret. Rotating first and updating receivers afterwards therefore
// failed every delivery in between, which is the opposite of what the doc
// promised. The window is receiver-side, and this fixes the order it needs.
func TestSecretRotationWindow(t *testing.T) {
	const (
		oldSecret = "secret-v1"
		newSecret = "secret-v2"
	)
	now := time.Now().UTC()
	ts := now.Format(time.RFC3339)
	body := []byte(`{"id":"01J","type":"flexitype.value.updated"}`)
	tolerance := 5 * time.Minute

	Convey("Given a delivery signed with whichever secret the server holds", t, func() {
		signedOld := events.Sign(oldSecret, ts, body)
		signedNew := events.Sign(newSecret, ts, body)

		Convey("When the receiver accepts both secrets, as step 1 of the rotation", func() {
			accepted := []string{oldSecret, newSecret}

			Convey("Then it accepts a delivery from before the rotation", func() {
				So(events.VerifyRequest(accepted, ts, body, signedOld, tolerance, now), ShouldBeTrue)
			})

			Convey("Then it accepts a delivery from after the rotation", func() {
				So(events.VerifyRequest(accepted, ts, body, signedNew, tolerance, now), ShouldBeTrue)
			})
		})

		Convey("When the receiver still holds only the old secret", func() {
			accepted := []string{oldSecret}

			Convey("Then rotating first rejects every delivery until it catches up", func() {
				// This is the hard cutover the doc must warn about: the
				// server signs with the new secret and nothing on the wire
				// carries the old signature as well.
				So(events.VerifyRequest(accepted, ts, body, signedNew, tolerance, now), ShouldBeFalse)
			})
		})

		Convey("When the old secret is dropped, as step 3 of the rotation", func() {
			accepted := []string{newSecret}

			Convey("Then a replayed pre-rotation delivery no longer verifies", func() {
				So(events.VerifyRequest(accepted, ts, body, signedOld, tolerance, now), ShouldBeFalse)
			})
		})

		Convey("When an empty secret sits in the accepted list", func() {
			Convey("Then it never matches, so an unset variable cannot open the door", func() {
				So(events.VerifyRequest([]string{""}, ts, body, events.Sign("", ts, body),
					tolerance, now), ShouldBeFalse)
			})
		})
	})
}
