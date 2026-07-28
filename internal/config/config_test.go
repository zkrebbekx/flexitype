package config_test

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/internal/config"
)

func TestRequireAuth(t *testing.T) {
	Convey("Given the default (authentication required)", t, func() {

		Convey("When no account source is configured", func() {
			t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "")
			t.Setenv("FLEXITYPE_PROVISIONING", "false")

			Convey("Then Load refuses to boot", func() {
				_, err := config.Load()
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "no account source is configured")
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

	// FLEXITYPE_REQUIRE_AUTH=false used to be a second, undocumented way to
	// boot unauthenticated: Load skipped the account-source check entirely, so
	// a manifest carried over from before the fail-closed default kept booting
	// open while the warning named FLEXITYPE_DEV_INSECURE, which nobody had
	// set. This test pinned that behaviour; it now pins the refusal.
	Convey("Given FLEXITYPE_REQUIRE_AUTH=false and no account source", t, func() {
		t.Setenv("FLEXITYPE_REQUIRE_AUTH", "false")
		t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "")
		t.Setenv("FLEXITYPE_PROVISIONING", "false")

		Convey("When the configuration is loaded", func() {
			_, err := config.Load()

			Convey("Then it is refused, and told which variable actually opts out", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "FLEXITYPE_REQUIRE_AUTH=false does not disable authentication")
				So(err.Error(), ShouldContainSubstring, "FLEXITYPE_DEV_INSECURE=true")
			})
		})
	})

	Convey("Given FLEXITYPE_REQUIRE_AUTH=false with an account source", t, func() {
		t.Setenv("FLEXITYPE_REQUIRE_AUTH", "false")
		t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "/etc/flexitype/accounts.json")
		t.Setenv("FLEXITYPE_PROVISIONING", "false")

		Convey("When the configuration is loaded", func() {
			cfg, err := config.Load()

			Convey("Then it succeeds and authentication stays on", func() {
				So(err, ShouldBeNil)
				So(cfg.RequireAuth, ShouldBeTrue)
			})
		})
	})
}

func TestMalformedValueFailsLoud(t *testing.T) {
	Convey("Given a malformed boolean value", t, func() {
		t.Setenv("FLEXITYPE_OUTBOX", "ture") // typo
		t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "/etc/flexitype/accounts.json")

		Convey("Then Load fails loudly instead of silently defaulting", func() {
			_, err := config.Load()
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "FLEXITYPE_OUTBOX")
		})
	})
}

func TestSSLModeGuard(t *testing.T) {
	Convey("Given sslmode=disable and a non-loopback DB host", t, func() {
		t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "/etc/flexitype/accounts.json")
		t.Setenv("FLEXITYPE_DB_SSLMODE", "disable")
		t.Setenv("FLEXITYPE_DB_HOST", "db.internal")

		Convey("Then Load refuses unencrypted traffic", func() {
			_, err := config.Load()
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not allowed for non-loopback")
		})
	})

	Convey("Given sslmode=disable and a loopback DB host", t, func() {
		t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "/etc/flexitype/accounts.json")
		t.Setenv("FLEXITYPE_DB_SSLMODE", "disable")
		t.Setenv("FLEXITYPE_DB_HOST", "localhost")

		Convey("Then Load allows it (local development)", func() {
			_, err := config.Load()
			So(err, ShouldBeNil)
		})
	})
}

// TestDBParamsCannotBypassTheTLSGuard covers the escape hatch that turned the
// guard off.
//
// FLEXITYPE_DB_PARAMS is appended verbatim to the rendered connection string,
// and libpq resolves duplicate keywords LAST-WINS — so a value there silently
// disabled TLS, or redirected the connection with the configured credentials
// to another server, while the guard evaluated the settings it had been told
// about and passed.
func TestDBParamsCannotBypassTheTLSGuard(t *testing.T) {
	base := func(t *testing.T) {
		t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "/etc/flexitype/accounts.json")
		t.Setenv("FLEXITYPE_DEV_INSECURE", "")
	}

	Convey("Given sslmode=require and a non-loopback host", t, func() {
		base(t)
		t.Setenv("FLEXITYPE_DB_HOST", "db.example.com")
		t.Setenv("FLEXITYPE_DB_SSLMODE", "require")

		Convey("When DB_PARAMS appends sslmode=disable", func() {
			t.Setenv("FLEXITYPE_DB_PARAMS", "application_name=flexitype sslmode=disable")
			_, err := config.Load()

			Convey("Then it is refused: last-wins means TLS would have been off", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "sslmode=disable is not allowed")
			})
		})

		Convey("When DB_PARAMS carries only a harmless parameter", func() {
			t.Setenv("FLEXITYPE_DB_PARAMS", "application_name=flexitype")
			_, err := config.Load()

			Convey("Then it is accepted: the hatch still works for its purpose", func() {
				So(err, ShouldBeNil)
			})
		})
	})

	Convey("Given a loopback host with TLS off", t, func() {
		base(t)
		t.Setenv("FLEXITYPE_DB_HOST", "localhost")
		t.Setenv("FLEXITYPE_DB_SSLMODE", "disable")

		Convey("When DB_PARAMS redirects the connection with host=", func() {
			t.Setenv("FLEXITYPE_DB_PARAMS", "host=attacker.example.com")
			_, err := config.Load()

			Convey("Then it is refused: the guard evaluates where it really connects", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "attacker.example.com")
			})
		})

		Convey("When DB_PARAMS redirects it with hostaddr=", func() {
			// hostaddr bypasses name resolution entirely, so it is where the
			// connection goes even when host= is also present.
			t.Setenv("FLEXITYPE_DB_PARAMS", "hostaddr=203.0.113.10")
			_, err := config.Load()

			Convey("Then it is refused too", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "203.0.113.10")
			})
		})

		Convey("When no params are set", func() {
			t.Setenv("FLEXITYPE_DB_PARAMS", "")
			_, err := config.Load()

			Convey("Then a loopback development stack still boots", func() {
				So(err, ShouldBeNil)
			})
		})
	})

	Convey("Given a URL whose query disables TLS to a remote host", t, func() {
		base(t)
		t.Setenv("FLEXITYPE_DB_URL", "postgres://u:p@db.example.com:5432/flexitype?sslmode=disable")

		Convey("When the configuration is loaded", func() {
			_, err := config.Load()

			Convey("Then the URL form goes through the same guard", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "db.example.com")
			})
		})
	})
}

func TestParseBoolOrTrue(t *testing.T) {
	Convey("Given an environment value being read as an opt-out", t, func() {
		Convey("When it parses", func() {
			Convey("Then its value is used", func() {
				So(config.ParseBoolOrTrueForTest("false"), ShouldBeFalse)
				So(config.ParseBoolOrTrueForTest("0"), ShouldBeFalse)
				So(config.ParseBoolOrTrueForTest("true"), ShouldBeTrue)
			})
		})

		Convey("When it does not parse", func() {
			Convey("Then it reads as true, so an unreadable setting never opts out", func() {
				So(config.ParseBoolOrTrueForTest("ture"), ShouldBeTrue)
				So(config.ParseBoolOrTrueForTest(""), ShouldBeTrue)
			})
		})
	})
}
