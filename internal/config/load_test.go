package config_test

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/internal/config"
)

// allKeys is every environment variable Load consults. Tests clear the lot so
// a developer's ambient environment cannot change what they assert.
var allKeys = []string{
	"FLEXITYPE_PORT",
	"FLEXITYPE_SERVICE_ACCOUNTS",
	"FLEXITYPE_REQUIRE_AUTH",
	"FLEXITYPE_LOG_LEVEL",
	"FLEXITYPE_LOG_FORMAT",
	"FLEXITYPE_SHUTDOWN_TIMEOUT",
	"FLEXITYPE_MIGRATE_ON_START",
	"FLEXITYPE_FEATURE_SEARCH",
	"FLEXITYPE_FEATURE_ACTIVITY",
	"FLEXITYPE_OUTBOX",
	"FLEXITYPE_FEATURE_SEARCH_INDEX",
	"FLEXITYPE_BLOB_DIR",
	"FLEXITYPE_EVENT_RETENTION",
	"FLEXITYPE_WEBHOOK_ALLOW_PRIVATE",
	"FLEXITYPE_METRICS",
	"FLEXITYPE_PROVISIONING",
	"FLEXITYPE_AUTH_CACHE_TTL",
	"FLEXITYPE_BOOTSTRAP_ADMIN",
	"FLEXITYPE_RATE_LIMIT_RPS",
	"FLEXITYPE_RATE_LIMIT_BURST",
	"FLEXITYPE_PUBSUB_PROJECT",
	"FLEXITYPE_PUBSUB_TOPIC",
	"FLEXITYPE_PUBSUB_ORDERING",
	"FLEXITYPE_DB_HOST",
	"FLEXITYPE_DB_PORT",
	"FLEXITYPE_DB_USER",
	"FLEXITYPE_DB_PASSWORD",
	"FLEXITYPE_DB_NAME",
	"FLEXITYPE_DB_SSLMODE",
	"FLEXITYPE_DB_MAX_OPEN_CONNS",
	"FLEXITYPE_DB_MAX_IDLE_CONNS",
	"FLEXITYPE_DB_CONN_MAX_LIFETIME",
}

// clearEnv unsets every FLEXITYPE_* variable for the duration of the test.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range allKeys {
		t.Setenv(k, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	Convey("Given an environment with no FLEXITYPE_* variables set", t, func() {
		clearEnv(t)

		Convey("When configuration is loaded", func() {
			cfg, err := config.Load()
			So(err, ShouldBeNil)

			Convey("Then the service defaults are the production-safe values", func() {
				So(cfg.Port, ShouldEqual, 8080)
				So(cfg.ServiceAccountsPath, ShouldEqual, "")
				So(cfg.RequireAuth, ShouldBeFalse)
				So(cfg.LogLevel, ShouldEqual, "info")
				So(cfg.LogFormat, ShouldEqual, "json")
				So(cfg.ShutdownTimeout, ShouldEqual, 30*time.Second)
				So(cfg.MigrateOnStart, ShouldBeTrue)
				So(cfg.EnableSearch, ShouldBeTrue)
				So(cfg.EnableActivity, ShouldBeTrue)
				So(cfg.EnableOutbox, ShouldBeFalse)
				So(cfg.EnableSearchIndex, ShouldBeFalse)
				So(cfg.BlobDir, ShouldEqual, "")
				So(cfg.EventRetention, ShouldEqual, 7*24*time.Hour)
				So(cfg.WebhookAllowPrivate, ShouldBeFalse)
				So(cfg.EnableMetrics, ShouldBeTrue)
				So(cfg.EnableProvisioning, ShouldBeFalse)
				So(cfg.AuthCacheTTL, ShouldEqual, 30*time.Second)
				So(cfg.BootstrapAdmin, ShouldBeFalse)
				So(cfg.RateLimitRPS, ShouldEqual, 0)
				So(cfg.RateLimitBurst, ShouldEqual, 200)
				So(cfg.PubSubProject, ShouldEqual, "")
				So(cfg.PubSubTopic, ShouldEqual, "flexitype-events")
				So(cfg.PubSubOrdering, ShouldBeFalse)
			})

			Convey("Then the database defaults point at a local postgres", func() {
				So(cfg.Database.Host, ShouldEqual, "localhost")
				So(cfg.Database.Port, ShouldEqual, 5432)
				So(cfg.Database.User, ShouldEqual, "postgres")
				So(cfg.Database.Password, ShouldEqual, "postgres")
				So(cfg.Database.Name, ShouldEqual, "flexitype")
				So(cfg.Database.SSLMode, ShouldEqual, "disable")
				So(cfg.Database.MaxOpenConns, ShouldEqual, 25)
				So(cfg.Database.MaxIdleConns, ShouldEqual, 10)
				So(cfg.Database.ConnMaxLifetime, ShouldEqual, 30*time.Minute)
			})
		})
	})
}

func TestLoadOverrides(t *testing.T) {
	Convey("Given every setting overridden with a valid value", t, func() {
		clearEnv(t)
		t.Setenv("FLEXITYPE_PORT", "9090")
		t.Setenv("FLEXITYPE_SERVICE_ACCOUNTS", "/etc/flexitype/accounts.json")
		t.Setenv("FLEXITYPE_REQUIRE_AUTH", "true")
		t.Setenv("FLEXITYPE_LOG_LEVEL", "debug")
		t.Setenv("FLEXITYPE_LOG_FORMAT", "console")
		t.Setenv("FLEXITYPE_SHUTDOWN_TIMEOUT", "45s")
		t.Setenv("FLEXITYPE_MIGRATE_ON_START", "false")
		t.Setenv("FLEXITYPE_FEATURE_SEARCH", "false")
		t.Setenv("FLEXITYPE_FEATURE_ACTIVITY", "0")
		t.Setenv("FLEXITYPE_OUTBOX", "true")
		t.Setenv("FLEXITYPE_FEATURE_SEARCH_INDEX", "1")
		t.Setenv("FLEXITYPE_BLOB_DIR", "/var/lib/flexitype/blobs")
		t.Setenv("FLEXITYPE_EVENT_RETENTION", "48h")
		t.Setenv("FLEXITYPE_WEBHOOK_ALLOW_PRIVATE", "true")
		t.Setenv("FLEXITYPE_METRICS", "false")
		t.Setenv("FLEXITYPE_PROVISIONING", "true")
		t.Setenv("FLEXITYPE_AUTH_CACHE_TTL", "1m30s")
		t.Setenv("FLEXITYPE_BOOTSTRAP_ADMIN", "true")
		t.Setenv("FLEXITYPE_RATE_LIMIT_RPS", "12.5")
		t.Setenv("FLEXITYPE_RATE_LIMIT_BURST", "500")
		t.Setenv("FLEXITYPE_PUBSUB_PROJECT", "acme-prod")
		t.Setenv("FLEXITYPE_PUBSUB_TOPIC", "acme-events")
		t.Setenv("FLEXITYPE_PUBSUB_ORDERING", "true")
		t.Setenv("FLEXITYPE_DB_HOST", "127.0.0.1")
		t.Setenv("FLEXITYPE_DB_PORT", "6543")
		t.Setenv("FLEXITYPE_DB_USER", "flexi")
		t.Setenv("FLEXITYPE_DB_PASSWORD", "s3cret")
		t.Setenv("FLEXITYPE_DB_NAME", "flexi_prod")
		t.Setenv("FLEXITYPE_DB_SSLMODE", "require")
		t.Setenv("FLEXITYPE_DB_MAX_OPEN_CONNS", "50")
		t.Setenv("FLEXITYPE_DB_MAX_IDLE_CONNS", "20")
		t.Setenv("FLEXITYPE_DB_CONN_MAX_LIFETIME", "10m")

		Convey("When configuration is loaded", func() {
			cfg, err := config.Load()
			So(err, ShouldBeNil)

			Convey("Then every parser reads the value it was given", func() {
				So(cfg.Port, ShouldEqual, 9090)
				So(cfg.ServiceAccountsPath, ShouldEqual, "/etc/flexitype/accounts.json")
				So(cfg.RequireAuth, ShouldBeTrue)
				So(cfg.LogLevel, ShouldEqual, "debug")
				So(cfg.LogFormat, ShouldEqual, "console")
				So(cfg.ShutdownTimeout, ShouldEqual, 45*time.Second)
				So(cfg.MigrateOnStart, ShouldBeFalse)
				So(cfg.EnableSearch, ShouldBeFalse)
				So(cfg.EnableActivity, ShouldBeFalse)
				So(cfg.EnableOutbox, ShouldBeTrue)
				So(cfg.EnableSearchIndex, ShouldBeTrue)
				So(cfg.BlobDir, ShouldEqual, "/var/lib/flexitype/blobs")
				So(cfg.EventRetention, ShouldEqual, 48*time.Hour)
				So(cfg.WebhookAllowPrivate, ShouldBeTrue)
				So(cfg.EnableMetrics, ShouldBeFalse)
				So(cfg.EnableProvisioning, ShouldBeTrue)
				So(cfg.AuthCacheTTL, ShouldEqual, 90*time.Second)
				So(cfg.BootstrapAdmin, ShouldBeTrue)
				So(cfg.RateLimitRPS, ShouldEqual, 12.5)
				So(cfg.RateLimitBurst, ShouldEqual, 500)
				So(cfg.PubSubProject, ShouldEqual, "acme-prod")
				So(cfg.PubSubTopic, ShouldEqual, "acme-events")
				So(cfg.PubSubOrdering, ShouldBeTrue)
				So(cfg.Database.Host, ShouldEqual, "127.0.0.1")
				So(cfg.Database.Port, ShouldEqual, 6543)
				So(cfg.Database.User, ShouldEqual, "flexi")
				So(cfg.Database.Password, ShouldEqual, "s3cret")
				So(cfg.Database.Name, ShouldEqual, "flexi_prod")
				So(cfg.Database.SSLMode, ShouldEqual, "require")
				So(cfg.Database.MaxOpenConns, ShouldEqual, 50)
				So(cfg.Database.MaxIdleConns, ShouldEqual, 20)
				So(cfg.Database.ConnMaxLifetime, ShouldEqual, 10*time.Minute)
			})
		})
	})
}

func TestTypedEnvParsers(t *testing.T) {
	Convey("Given a malformed integer value", t, func() {
		clearEnv(t)
		t.Setenv("FLEXITYPE_DB_MAX_OPEN_CONNS", "twenty-five")

		Convey("When configuration is loaded", func() {
			cfg, err := config.Load()

			Convey("Then it fails loudly rather than silently defaulting", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "invalid configuration")
				So(err.Error(), ShouldContainSubstring, `invalid FLEXITYPE_DB_MAX_OPEN_CONNS="twenty-five"`)
				So(err.Error(), ShouldContainSubstring, "must be an integer")
				So(cfg, ShouldResemble, config.Config{})
			})
		})
	})

	Convey("Given a malformed float value", t, func() {
		clearEnv(t)
		t.Setenv("FLEXITYPE_RATE_LIMIT_RPS", "fast")

		Convey("When configuration is loaded", func() {
			_, err := config.Load()

			Convey("Then the number parse error is reported", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, `invalid FLEXITYPE_RATE_LIMIT_RPS="fast"`)
				So(err.Error(), ShouldContainSubstring, "must be a number")
			})
		})
	})

	Convey("Given a malformed duration value", t, func() {
		clearEnv(t)
		t.Setenv("FLEXITYPE_SHUTDOWN_TIMEOUT", "30 seconds")

		Convey("When configuration is loaded", func() {
			_, err := config.Load()

			Convey("Then the duration parse error is reported with a hint", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, `invalid FLEXITYPE_SHUTDOWN_TIMEOUT="30 seconds"`)
				So(err.Error(), ShouldContainSubstring, "must be a duration")
				So(err.Error(), ShouldContainSubstring, "30s, 5m")
			})
		})
	})

	Convey("Given several malformed values at once", t, func() {
		clearEnv(t)
		t.Setenv("FLEXITYPE_DB_PORT", "five-four-three-two")
		t.Setenv("FLEXITYPE_OUTBOX", "ture")
		t.Setenv("FLEXITYPE_EVENT_RETENTION", "a week")

		Convey("When configuration is loaded", func() {
			_, err := config.Load()

			Convey("Then every parse failure is collected into one error", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "FLEXITYPE_DB_PORT")
				So(err.Error(), ShouldContainSubstring, "FLEXITYPE_OUTBOX")
				So(err.Error(), ShouldContainSubstring, "must be a boolean")
				So(err.Error(), ShouldContainSubstring, "FLEXITYPE_EVENT_RETENTION")
			})
		})
	})

	Convey("Given a malformed port that would fall back to the default", t, func() {
		clearEnv(t)
		t.Setenv("FLEXITYPE_PORT", "eighty-eighty")

		Convey("When configuration is loaded", func() {
			_, err := config.Load()

			Convey("Then the fallback keeps the range check happy but the parse error still fails the boot", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "must be an integer")
				// Proof the fallback (8080) was used: the range check never
				// complained about a zero port.
				So(err.Error(), ShouldNotContainSubstring, "invalid FLEXITYPE_PORT 0")
			})
		})
	})
}

func TestPortRange(t *testing.T) {
	Convey("Given an out-of-range port", t, func() {
		clearEnv(t)

		Convey("When the port is zero", func() {
			t.Setenv("FLEXITYPE_PORT", "0")

			Convey("Then Load rejects it", func() {
				_, err := config.Load()
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldEqual, "invalid FLEXITYPE_PORT 0")
			})
		})

		Convey("When the port is negative", func() {
			t.Setenv("FLEXITYPE_PORT", "-1")

			Convey("Then Load rejects it", func() {
				_, err := config.Load()
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldEqual, "invalid FLEXITYPE_PORT -1")
			})
		})

		Convey("When the port is above the 16-bit ceiling", func() {
			t.Setenv("FLEXITYPE_PORT", "65536")

			Convey("Then Load rejects it", func() {
				_, err := config.Load()
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldEqual, "invalid FLEXITYPE_PORT 65536")
			})
		})

		Convey("When the port is the highest valid value", func() {
			t.Setenv("FLEXITYPE_PORT", "65535")

			Convey("Then Load accepts it", func() {
				cfg, err := config.Load()
				So(err, ShouldBeNil)
				So(cfg.Port, ShouldEqual, 65535)
			})
		})
	})
}

func TestLoopbackSSLGuard(t *testing.T) {
	Convey("Given sslmode=disable", t, func() {
		clearEnv(t)
		t.Setenv("FLEXITYPE_DB_SSLMODE", "disable")

		Convey("When the host is an IPv4 loopback address", func() {
			t.Setenv("FLEXITYPE_DB_HOST", "127.0.0.53")

			Convey("Then Load allows the unencrypted connection", func() {
				_, err := config.Load()
				So(err, ShouldBeNil)
			})
		})

		Convey("When the host is an IPv6 loopback address", func() {
			t.Setenv("FLEXITYPE_DB_HOST", "::1")

			Convey("Then Load allows the unencrypted connection", func() {
				_, err := config.Load()
				So(err, ShouldBeNil)
			})
		})

		Convey("When the host is a non-loopback IP address", func() {
			t.Setenv("FLEXITYPE_DB_HOST", "10.0.0.42")

			Convey("Then Load refuses to send credentials in the clear", func() {
				_, err := config.Load()
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, `non-loopback host "10.0.0.42"`)
				So(err.Error(), ShouldContainSubstring, "use require/verify-full")
			})
		})
	})

	Convey("Given a non-loopback host with TLS required", t, func() {
		clearEnv(t)
		t.Setenv("FLEXITYPE_DB_HOST", "db.internal")
		t.Setenv("FLEXITYPE_DB_SSLMODE", "verify-full")

		Convey("When configuration is loaded", func() {
			cfg, err := config.Load()

			Convey("Then it is accepted", func() {
				So(err, ShouldBeNil)
				So(cfg.Database.SSLMode, ShouldEqual, "verify-full")
			})
		})
	})
}

func TestDatabaseDSN(t *testing.T) {
	Convey("Given database settings", t, func() {
		Convey("When the DSN is rendered", func() {
			dsn := config.Database{
				Host:     "db.internal",
				Port:     6543,
				User:     "flexi",
				Password: "s3cret",
				Name:     "flexi_prod",
				SSLMode:  "verify-full",
			}.DSN()

			Convey("Then it is the lib/pq key-value connection string", func() {
				So(dsn, ShouldEqual,
					"host=db.internal port=6543 user=flexi password=s3cret dbname=flexi_prod sslmode=verify-full")
			})
		})

		Convey("When the settings come from a loaded config", func() {
			clearEnv(t)
			t.Setenv("FLEXITYPE_DB_NAME", "ft_pkgdb")
			t.Setenv("FLEXITYPE_DB_PORT", "55433")
			cfg, err := config.Load()
			So(err, ShouldBeNil)

			Convey("Then the DSN reflects the loaded values and defaults", func() {
				So(cfg.Database.DSN(), ShouldEqual,
					"host=localhost port=55433 user=postgres password=postgres dbname=ft_pkgdb sslmode=disable")
			})
		})

		Convey("When the settings are zero values", func() {
			dsn := config.Database{}.DSN()

			Convey("Then every key is still present, with empty values", func() {
				So(dsn, ShouldEqual, "host= port=0 user= password= dbname= sslmode=")
			})
		})
	})
}
