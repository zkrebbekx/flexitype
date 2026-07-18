package serviceaccount

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// writeAccounts drops a service-account file into a temp dir and returns its
// path.
func writeAccounts(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write accounts file: %v", err)
	}
	return path
}

func TestLoadFile(t *testing.T) {
	Convey("Given a service-account file on disk", t, func() {
		Convey("When it holds two well-formed accounts", func() {
			path := writeAccounts(t, `[
			  {
			    "id": "ci",
			    "name": "CI Importer",
			    "tenant_id": "acme",
			    "scopes": ["read", "write"],
			    "field_permissions": {"salary": "none"},
			    "secret_hash": "`+HashSecret("ci-secret")+`"
			  },
			  {
			    "id": "ops",
			    "name": "Ops Console",
			    "tenant_id": "globex",
			    "scopes": ["admin"],
			    "secret_hash": "`+HashSecret("ops-secret")+`"
			  }
			]`)

			store, err := LoadFile(path)

			Convey("Then the store authenticates both accounts with their fields intact", func() {
				So(err, ShouldBeNil)
				So(store, ShouldNotBeNil)

				ci, ciErr := store.Authenticate(MintToken("ci", "ci-secret"))
				So(ciErr, ShouldBeNil)
				So(ci.Name, ShouldEqual, "CI Importer")
				So(ci.Tenant().String(), ShouldEqual, "acme")
				So(ci.Scopes, ShouldResemble, []Scope{ScopeRead, ScopeWrite})
				So(ci.FieldPermissions, ShouldResemble, map[string]string{"salary": "none"})
				So(ci.HasScope(ScopeAdmin), ShouldBeFalse)

				ops, opsErr := store.Authenticate(MintToken("ops", "ops-secret"))
				So(opsErr, ShouldBeNil)
				So(ops.Tenant().String(), ShouldEqual, "globex")
				So(ops.HasScope(ScopeWrite), ShouldBeTrue)
			})

			Convey("Then a wrong secret for a loaded account is rejected", func() {
				So(err, ShouldBeNil)
				_, authErr := store.Authenticate(MintToken("ci", "ops-secret"))
				So(authErr, ShouldNotBeNil)
				So(authErr.Error(), ShouldEqual, "invalid credentials")
			})
		})

		Convey("When it holds an empty array", func() {
			store, err := LoadFile(writeAccounts(t, `[]`))

			Convey("Then the store loads but authenticates nobody", func() {
				So(err, ShouldBeNil)
				So(store, ShouldNotBeNil)

				_, authErr := store.Authenticate(MintToken("ci", "secret"))
				So(authErr, ShouldNotBeNil)
				So(authErr.Error(), ShouldEqual, "unknown service account")
			})
		})

		Convey("When an account is missing its id", func() {
			_, err := LoadFile(writeAccounts(t,
				`[{"name": "Nameless", "secret_hash": "abc"}]`))

			Convey("Then loading fails naming the offending account", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, `service account "Nameless" missing id or secret_hash`)
			})
		})

		Convey("When an account is missing its secret hash", func() {
			_, err := LoadFile(writeAccounts(t,
				`[{"id": "ci", "name": "CI Importer"}]`))

			Convey("Then loading fails rather than provisioning an unauthenticatable account", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, `service account "CI Importer" missing id or secret_hash`)
			})
		})

		Convey("When the file is not valid JSON", func() {
			_, err := LoadFile(writeAccounts(t, `{not json`))

			Convey("Then the decode error is wrapped", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "decode service accounts")
			})
		})

		Convey("When the file holds a JSON object instead of an array", func() {
			_, err := LoadFile(writeAccounts(t, `{"id": "ci"}`))

			Convey("Then the shape mismatch is reported as a decode failure", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "decode service accounts")
			})
		})
	})

	Convey("Given no service-account file at the configured path", t, func() {
		Convey("When it is loaded", func() {
			store, err := LoadFile(filepath.Join(t.TempDir(), "absent.json"))

			Convey("Then the read error is wrapped and identifiable as not-exist", func() {
				So(store, ShouldBeNil)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "read service accounts")
				So(errors.Is(err, fs.ErrNotExist), ShouldBeTrue)
			})
		})
	})
}

func TestAuthenticateCtx(t *testing.T) {
	Convey("Given a file-backed store used through the context-aware interface", t, func() {
		secret := "super-secret-value"
		store := NewStore([]Account{{
			ID:         "ci",
			Name:       "CI Importer",
			TenantID:   "acme",
			Scopes:     []Scope{ScopeRead},
			SecretHash: HashSecret(secret),
		}})

		var authCtx AuthenticatorCtx = store
		var auth Authenticator = store
		So(authCtx, ShouldNotBeNil)
		So(auth, ShouldNotBeNil)

		Convey("When a valid token is presented with a live context", func() {
			account, err := authCtx.AuthenticateCtx(context.Background(), MintToken("ci", secret))

			Convey("Then it resolves the same account Authenticate would", func() {
				So(err, ShouldBeNil)
				So(account.ID, ShouldEqual, "ci")
				So(account.Name, ShouldEqual, "CI Importer")
			})
		})

		Convey("When the context is already cancelled", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			account, err := authCtx.AuthenticateCtx(ctx, MintToken("ci", secret))

			Convey("Then the in-memory store still resolves it — no I/O to cancel", func() {
				So(err, ShouldBeNil)
				So(account.ID, ShouldEqual, "ci")
			})
		})

		Convey("When a malformed token is presented", func() {
			_, err := authCtx.AuthenticateCtx(context.Background(), "not-a-flexitype-token")

			Convey("Then it fails as malformed", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldEqual, "malformed token")
			})
		})

		Convey("When an unknown account id is presented", func() {
			_, err := authCtx.AuthenticateCtx(context.Background(), MintToken("ghost", secret))

			Convey("Then it fails as an unknown account", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldEqual, "unknown service account")
			})
		})
	})
}

func TestTenantParsing(t *testing.T) {
	Convey("Given accounts with varying tenant ids", t, func() {
		Convey("When the tenant id is well-formed", func() {
			tenant := Account{TenantID: "acme"}.Tenant()

			Convey("Then it parses to that tenant", func() {
				So(tenant.String(), ShouldEqual, "acme")
			})
		})

		Convey("When the tenant id is empty", func() {
			tenant := Account{TenantID: ""}.Tenant()

			Convey("Then it falls back to the default tenant", func() {
				So(tenant, ShouldEqual, valueobjects.DefaultTenant)
			})
		})

		Convey("When the tenant id is unparseable", func() {
			tenant := Account{TenantID: "NOT A VALID TENANT!!"}.Tenant()

			Convey("Then it falls back to the default tenant rather than erroring", func() {
				So(tenant, ShouldEqual, valueobjects.DefaultTenant)
			})
		})
	})
}

func TestTokenHelpers(t *testing.T) {
	Convey("Given the token vocabulary", t, func() {
		Convey("When a token is minted and split back apart", func() {
			token := MintToken("01H0ABCDEF", "s3cr#t_with_underscores")
			id, secret, err := SplitToken(token)

			Convey("Then the id and the full secret survive the round trip", func() {
				So(err, ShouldBeNil)
				So(token, ShouldStartWith, TokenPrefix)
				So(id, ShouldEqual, "01H0ABCDEF")
				So(secret, ShouldEqual, "s3cr#t_with_underscores")
			})
		})

		Convey("When malformed tokens are split", func() {
			Convey("Then each is rejected as malformed", func() {
				for _, token := range []string{"", "ft_", "ft_ci", "ft__secret", "bearer ft_ci_x", "ci_secret"} {
					_, _, err := SplitToken(token)
					So(err, ShouldNotBeNil)
					So(err.Error(), ShouldEqual, "malformed token")
				}
			})
		})

		Convey("When a secret is hashed", func() {
			Convey("Then hashing is deterministic, hex-encoded and verifiable", func() {
				So(HashSecret("abc"), ShouldEqual, HashSecret("abc"))
				So(HashSecret("abc"), ShouldNotEqual, HashSecret("abd"))
				So(HashSecret("abc"), ShouldHaveLength, 64)
				So(VerifySecret("abc", HashSecret("abc")), ShouldBeNil)
			})
		})

		Convey("When a secret does not match its stored hash", func() {
			err := VerifySecret("wrong", HashSecret("right"))

			Convey("Then verification reports invalid credentials", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldEqual, "invalid credentials")
			})
		})

		Convey("When only the timing-equalising comparison is run", func() {
			err := VerifyOnlyTiming("anything")

			Convey("Then it always reports an unknown account", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldEqual, "unknown service account")
			})
		})
	})
}
