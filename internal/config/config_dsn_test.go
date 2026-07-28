package config

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestDSNExtensibility covers the connection parameters that were unreachable.
//
// DSN rendered a fixed six-parameter string and no setting could add to it,
// so sslrootcert (a private CA), application_name (identifying the connection
// in pg_stat_activity), connect_timeout (so a pod does not hang on an
// unreachable host) and target_session_attrs could not be set through
// documented configuration at all.
func TestDSNExtensibility(t *testing.T) {
	Convey("Given the six-field database configuration", t, func() {
		d := Database{Host: "db", Port: 5432, User: "u", Password: "p", Name: "n", SSLMode: "require"}

		Convey("Then it renders the familiar form", func() {
			So(d.DSN(), ShouldEqual, "host=db port=5432 user=u password=p dbname=n sslmode=require")
		})

		Convey("When extra parameters are supplied", func() {
			d.Params = "application_name=flexitype connect_timeout=5"

			Convey("Then they are appended rather than replacing the form", func() {
				So(d.DSN(), ShouldContainSubstring, "host=db")
				So(d.DSN(), ShouldContainSubstring, "application_name=flexitype")
				So(d.DSN(), ShouldContainSubstring, "connect_timeout=5")
			})
		})

		Convey("When a complete URL is supplied", func() {
			d.URL = "postgres://u:p@db:5432/n?sslmode=verify-full&sslrootcert=/ca.pem"

			Convey("Then it replaces the rendered form entirely", func() {
				So(d.DSN(), ShouldEqual, d.URL)
			})
		})
	})
}

// TestSSLGuardCoversTheURL covers the escape hatch that must not become a way
// to turn a security guard off.
//
// Unencrypted database traffic is refused to a non-loopback host because it
// would send credentials and data in the clear. A supplied URL goes through
// the same check: which setting expressed the connection does not change
// what leaves the host.
func TestSSLGuardCoversTheURL(t *testing.T) {
	Convey("Given connection strings in both libpq forms", t, func() {
		for _, tc := range []struct {
			name, dsn, wantMode, wantHost string
		}{
			{"URL with sslmode", "postgres://u:p@db.example:5432/n?sslmode=require", "require", "db.example"},
			{"URL without sslmode", "postgres://u:p@db.example:5432/n", "disable", "db.example"},
			{"keyword form", "host=db.example port=5432 sslmode=verify-full", "verify-full", "db.example"},
			{"keyword form, no sslmode", "host=db.example port=5432", "disable", "db.example"},
			{"garbage", "not a dsn", "disable", ""},
		} {
			mode, host := sslModeAndHostOf(tc.dsn)

			Convey("Then "+tc.name+" reads as "+tc.wantMode, func() {
				So(mode, ShouldEqual, tc.wantMode)
				So(host, ShouldEqual, tc.wantHost)
			})
		}

		Convey("Then an unreadable connection string defaults to disable", func() {
			// The safe direction: the guard then demands a loopback host
			// rather than assuming the connection is encrypted.
			mode, _ := sslModeAndHostOf("")
			So(mode, ShouldEqual, "disable")
		})
	})
}
