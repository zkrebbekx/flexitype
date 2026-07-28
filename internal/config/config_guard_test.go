package config

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestLoadGuards covers the refusals that keep an insecure deployment from
// booting.
//
// Each of these is a configuration mistake that would otherwise serve
// traffic: no account source means the whole API — including the irreversible
// admin purge — is open to anonymous callers, and unencrypted database
// traffic to a remote host sends credentials in the clear. A boot failure is
// the right outcome for both.
func TestLoadGuards(t *testing.T) {
	Convey("Given a deployment with no account source", t, func() {
		t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "")
		t.Setenv("FLEXITYPE_PROVISIONING", "false")
		t.Setenv("FLEXITYPE_DEV_INSECURE", "false")

		Convey("Then it refuses to start, naming both ways out", func() {
			_, err := Load()
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "FLEXITYPE_SERVICE_ACCOUNTS")
			So(err.Error(), ShouldContainSubstring, "FLEXITYPE_DEV_INSECURE")
		})
	})

	Convey("Given unencrypted database traffic to a remote host", t, func() {
		t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "/etc/accounts.json")
		t.Setenv("FLEXITYPE_DEV_INSECURE", "false")
		t.Setenv("FLEXITYPE_DB_HOST", "db.example.com")
		t.Setenv("FLEXITYPE_DB_SSLMODE", "disable")

		Convey("Then it refuses to start", func() {
			_, err := Load()
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "sslmode=disable")
		})

		Convey("And a URL expressing the same thing is refused too", func() {
			t.Setenv("FLEXITYPE_DB_HOST", "localhost")
			t.Setenv("FLEXITYPE_DB_SSLMODE", "require")
			t.Setenv("FLEXITYPE_DB_URL", "postgres://u:p@db.example.com:5432/n?sslmode=disable")

			_, err := Load()
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "db.example.com")
		})

		Convey("And a URL to a loopback host is allowed", func() {
			t.Setenv("FLEXITYPE_DB_URL", "postgres://u:p@localhost:5432/n?sslmode=disable")

			cfg, err := Load()
			So(err, ShouldBeNil)
			So(cfg.Database.DSN(), ShouldContainSubstring, "localhost")
		})
	})

	Convey("Given a port outside the valid range", t, func() {
		t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "/etc/accounts.json")
		t.Setenv("FLEXITYPE_PORT", "70000")

		Convey("Then it refuses to start", func() {
			_, err := Load()
			So(err, ShouldNotBeNil)
		})
	})
}
