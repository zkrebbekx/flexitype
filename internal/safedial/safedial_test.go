package safedial

import (
	"errors"
	"net"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestIsPublic(t *testing.T) {
	Convey("Given the address classifier", t, func() {
		Convey("Then public addresses pass", func() {
			for _, s := range []string{"8.8.8.8", "1.1.1.1", "203.0.114.1", "2606:4700:4700::1111"} {
				So(isPublic(net.ParseIP(s)), ShouldBeTrue)
			}
		})

		Convey("Then non-public addresses are blocked", func() {
			for _, s := range []string{
				"127.0.0.1",        // loopback
				"10.0.0.5",         // private
				"192.168.1.1",      // private
				"172.16.0.1",       // private
				"169.254.169.254",  // link-local / cloud metadata
				"100.64.0.1",       // CGNAT
				"0.0.0.0",          // unspecified
				"::1",              // IPv6 loopback
				"fd00::1",          // IPv6 ULA (private)
				"fe80::1",          // IPv6 link-local
				"::ffff:127.0.0.1", // IPv4-mapped loopback
			} {
				So(isPublic(net.ParseIP(s)), ShouldBeFalse)
			}
		})
	})
}

func TestGuardAddress(t *testing.T) {
	Convey("Given the connect-time guard (Control hook)", t, func() {
		Convey("When the resolved address is a private/metadata IP", func() {
			err := guardAddress("169.254.169.254:80")

			Convey("Then it is blocked with a typed error", func() {
				var blocked *ErrBlockedAddress
				So(errors.As(err, &blocked), ShouldBeTrue)
				So(blocked.IP, ShouldEqual, "169.254.169.254")
			})
		})

		Convey("When the resolved address is a public IP", func() {
			So(guardAddress("8.8.8.8:443"), ShouldBeNil)
		})

		Convey("When a rebinding attack surfaces loopback at connect time", func() {
			// Control sees the ACTUAL dialed IP, so a name that resolved to a
			// public IP for a prior check but to loopback for the connection is
			// still blocked here.
			So(guardAddress("127.0.0.1:80"), ShouldNotBeNil)
			So(guardAddress("[::1]:80"), ShouldNotBeNil)
		})
	})
}
