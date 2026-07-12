package config_test

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/pkg/config"
)

func TestRequireAuth(t *testing.T) {
	Convey("Given FLEXITYPE_REQUIRE_AUTH is set", t, func() {
		t.Setenv("FLEXITYPE_REQUIRE_AUTH", "true")

		Convey("When no account source is configured", func() {
			t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "")
			t.Setenv("FLEXITYPE_PROVISIONING", "false")

			Convey("Then Load refuses to boot", func() {
				_, err := config.Load()
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "FLEXITYPE_REQUIRE_AUTH")
			})
		})

		Convey("When a service-account file is configured", func() {
			t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "/etc/flexitype/accounts.json")
			t.Setenv("FLEXITYPE_PROVISIONING", "false")

			Convey("Then Load succeeds", func() {
				cfg, err := config.Load()
				So(err, ShouldBeNil)
				So(cfg.RequireAuth, ShouldBeTrue)
			})
		})

		Convey("When provisioning is enabled", func() {
			t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "")
			t.Setenv("FLEXITYPE_PROVISIONING", "true")

			Convey("Then Load succeeds", func() {
				_, err := config.Load()
				So(err, ShouldBeNil)
			})
		})
	})

	Convey("Given FLEXITYPE_REQUIRE_AUTH is not set", t, func() {
		t.Setenv("FLEXITYPE_REQUIRE_AUTH", "false")
		t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "")
		t.Setenv("FLEXITYPE_PROVISIONING", "false")

		Convey("When no account source is configured", func() {
			Convey("Then Load still succeeds (development mode)", func() {
				cfg, err := config.Load()
				So(err, ShouldBeNil)
				So(cfg.RequireAuth, ShouldBeFalse)
			})
		})
	})
}

func TestMalformedValueFailsLoud(t *testing.T) {
	Convey("Given a malformed boolean value", t, func() {
		t.Setenv("FLEXITYPE_OUTBOX", "ture") // typo

		Convey("Then Load fails loudly instead of silently defaulting", func() {
			_, err := config.Load()
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "FLEXITYPE_OUTBOX")
		})
	})
}

func TestSSLModeGuard(t *testing.T) {
	Convey("Given sslmode=disable and a non-loopback DB host", t, func() {
		t.Setenv("FLEXITYPE_DB_SSLMODE", "disable")
		t.Setenv("FLEXITYPE_DB_HOST", "db.internal")

		Convey("Then Load refuses unencrypted traffic", func() {
			_, err := config.Load()
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not allowed for non-loopback")
		})
	})

	Convey("Given sslmode=disable and a loopback DB host", t, func() {
		t.Setenv("FLEXITYPE_DB_SSLMODE", "disable")
		t.Setenv("FLEXITYPE_DB_HOST", "localhost")

		Convey("Then Load allows it (local development)", func() {
			_, err := config.Load()
			So(err, ShouldBeNil)
		})
	})
}
