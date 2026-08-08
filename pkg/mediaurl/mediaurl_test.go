package mediaurl

import (
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestSigner(t *testing.T) {
	Convey("Given a signer over a deployment secret", t, func() {
		signer, err := NewSigner(strings.Repeat("k", MinSecretLength))
		So(err, ShouldBeNil)
		now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

		Convey("When a link is minted", func() {
			token, expires, serr := signer.Sign("acme", "01JBQ8Z000000000000000000", time.Minute, now)
			So(serr, ShouldBeNil)

			Convey("Then it verifies and asserts what it was signed for", func() {
				claims, verr := signer.Verify(token, now)
				So(verr, ShouldBeNil)
				So(claims.Tenant, ShouldEqual, "acme")
				So(claims.ObjectKey, ShouldEqual, "01JBQ8Z000000000000000000")
				So(claims.ExpiresAt.Equal(expires), ShouldBeTrue)
			})

			Convey("Then it stops working at its expiry", func() {
				_, verr := signer.Verify(token, expires)
				So(errors.Is(verr, ErrExpired), ShouldBeTrue)

				_, verr = signer.Verify(token, expires.Add(-time.Second))
				So(verr, ShouldBeNil)
			})

			Convey("Then the object key is not readable without decoding the token", func() {
				// The payload is encoded rather than plain, so a key does not
				// sit in a log line or a Referer header in the clear.
				So(token, ShouldNotContainSubstring, "01JBQ8Z000000000000000000")
			})
		})

		Convey("When a token is signed for one object", func() {
			token, _, serr := signer.Sign("acme", "object-a", time.Minute, now)
			So(serr, ShouldBeNil)

			Convey("Then it cannot be replayed against another object", func() {
				// The signature covers the key, so swapping it invalidates the
				// whole token rather than redirecting it.
				other, _, oerr := signer.Sign("acme", "object-b", time.Minute, now)
				So(oerr, ShouldBeNil)
				So(other, ShouldNotEqual, token)

				payload, signature, _ := strings.Cut(other, ".")
				forged := payload + "." + strings.Split(token, ".")[1]
				So(forged, ShouldNotEqual, other)
				_, verr := signer.Verify(forged, now)
				So(errors.Is(verr, ErrSignature), ShouldBeTrue)
				So(signature, ShouldNotBeEmpty)
			})

			Convey("Then it cannot be replayed against another tenant", func() {
				// The tenant is inside the signature, so a holder cannot point
				// a valid token at somebody else's object.
				claims, verr := signer.Verify(token, now)
				So(verr, ShouldBeNil)
				So(claims.Tenant, ShouldEqual, "acme")
			})

			Convey("Then another deployment's key does not verify it", func() {
				other, oerr := NewSigner(strings.Repeat("z", MinSecretLength))
				So(oerr, ShouldBeNil)
				_, verr := other.Verify(token, now)
				So(errors.Is(verr, ErrSignature), ShouldBeTrue)
			})

			Convey("Then a tampered expiry is refused", func() {
				// Lengthening the life of a link requires re-signing it.
				longer, _, lerr := signer.Sign("acme", "object-a", time.Hour, now)
				So(lerr, ShouldBeNil)
				forged := strings.Split(longer, ".")[0] + "." + strings.Split(token, ".")[1]
				_, verr := signer.Verify(forged, now)
				So(errors.Is(verr, ErrSignature), ShouldBeTrue)
			})
		})

		Convey("When a caller asks for no lifetime", func() {
			_, expires, serr := signer.Sign("acme", "object-a", 0, now)

			Convey("Then it gets the short default rather than an unbounded link", func() {
				So(serr, ShouldBeNil)
				So(expires.Sub(now), ShouldEqual, DefaultTTL)
			})
		})

		Convey("When a caller asks for a year", func() {
			_, expires, serr := signer.Sign("acme", "object-a", 365*24*time.Hour, now)

			Convey("Then it is capped rather than refused", func() {
				So(serr, ShouldBeNil)
				So(expires.Sub(now), ShouldEqual, MaxTTL)
			})
		})

		Convey("When a token is garbage", func() {
			for _, token := range []string{"", ".", "abc", "abc.def", "!!!.aa"} {
				_, verr := signer.Verify(token, now)
				So(verr, ShouldNotBeNil)
			}
		})

		Convey("When a key carries the characters a delimiter would have used", func() {
			// A minted object key ends in a file extension, so a dot is normal
			// input. The payload is JSON, so no character is special.
			token, _, serr := signer.Sign("acme", "01JBQ8Z0000.png", time.Minute, now)
			So(serr, ShouldBeNil)

			Convey("Then it round-trips exactly", func() {
				claims, verr := signer.Verify(token, now)
				So(verr, ShouldBeNil)
				So(claims.ObjectKey, ShouldEqual, "01JBQ8Z0000.png")
			})
		})
	})

	Convey("Given a short signing key", t, func() {
		Convey("When a signer is built", func() {
			_, err := NewSigner("too-short")

			Convey("Then it is refused: every link ever issued is forgeable once it falls", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})
}
