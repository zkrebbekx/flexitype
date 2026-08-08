package flexitype_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application/admin"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
)

// auditRow is one activity-log row, read straight from the table so the test
// sees exactly what was persisted — including the serialized descriptors.
//
// before_state and after_state are cast to text in SQL. The pooled CI job
// connects with binary_parameters=yes, so a []byte parameter would be sent as
// a binary bytea and Postgres would read its first byte as a jsonb version.
// Text out, string in, no jsonb parameters anywhere in this file.
type auditRow struct {
	TenantID string `db:"tenant_id"`
	Actor    string `db:"actor"`
	Entity   string `db:"entity"`
	EntityID string `db:"entity_id"`
	Action   string `db:"action"`
	Before   string `db:"before_state"`
	After    string `db:"after_state"`
}

// payload is the whole serialized entry, for substring assertions.
func (r auditRow) payload() string { return r.Before + " " + r.After }

func readAuditRows(t *testing.T, pool *sqlx.DB) []auditRow {
	t.Helper()
	var rows []auditRow
	err := pool.Select(&rows, `SELECT tenant_id, actor, entity, entity_id, action,
	          COALESCE(before_state::text, '') AS before_state,
	          COALESCE(after_state::text, '')  AS after_state
	     FROM flexitype_activity_log
	    ORDER BY occurred_at, id`)
	if err != nil {
		t.Fatalf("read activity log: %v", err)
	}
	return rows
}

// TestAdminControlPlaneAuditPostgres is the regression for #507.
//
// The admin control plane took no unit of work and never recorded a change, so
// a credential or a role change left no audit trail at all: "who created this
// account, and who rotated it since?" had no answer. Each mutating usecase now
// runs in one transaction and writes exactly one activity entry in it, stamped
// with the AFFECTED tenant, and carrying no secret, no secret hash and no
// minted token.
func TestAdminControlPlaneAuditPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a database-backed control plane and an empty activity log", t, func() {
		truncateAll(t, pool)
		a := svc.AdminInteractor()
		ctx := context.Background() // an admin request carries NO tenant

		// only asserts that op adds exactly ONE activity entry, and returns
		// it. The log is cleared first, so the count is the assertion.
		only := func(op func() error) auditRow {
			_, derr := pool.Exec(`DELETE FROM flexitype_activity_log`)
			So(derr, ShouldBeNil)
			So(op(), ShouldBeNil)
			rows := readAuditRows(t, pool)
			So(len(rows), ShouldEqual, 1)
			So(rows[0].Actor, ShouldNotBeBlank)
			return rows[0]
		}

		Convey("When an operator provisions a tenant, a role and an account", func() {
			var tenantEntry, roleEntry, accountEntry auditRow
			var tenant *admin.Tenant
			var role *admin.Role
			var created *admin.AccountWithToken

			tenantEntry = only(func() error {
				var err error
				tenant, err = a.CreateTenant(ctx, "acme")
				return err
			})
			roleEntry = only(func() error {
				var err error
				role, err = a.UpsertRole(ctx, admin.UpsertRoleInput{
					TenantName:       "acme",
					Name:             "reviewer",
					Scopes:           []string{"read"},
					FieldPermissions: map[string]string{"salary": "none"},
				})
				return err
			})
			accountEntry = only(func() error {
				var err error
				created, err = a.CreateAccount(ctx, admin.CreateAccountInput{
					TenantName: "acme",
					Name:       "reviewer-bot",
					Scopes:     []string{"read"},
					Roles:      []string{"reviewer"},
				})
				return err
			})

			Convey("Then each change left one entry with the right tenant, entity and action", func() {
				So(tenantEntry.TenantID, ShouldEqual, "acme")
				So(tenantEntry.Entity, ShouldEqual, admin.EntityTenant)
				So(tenantEntry.EntityID, ShouldEqual, tenant.ID.String())
				So(tenantEntry.Action, ShouldEqual, "created")

				So(roleEntry.TenantID, ShouldEqual, "acme")
				So(roleEntry.Entity, ShouldEqual, admin.EntityRole)
				So(roleEntry.EntityID, ShouldEqual, role.ID.String())
				So(roleEntry.Action, ShouldEqual, "created")

				So(accountEntry.TenantID, ShouldEqual, "acme")
				So(accountEntry.Entity, ShouldEqual, admin.EntityServiceAccount)
				So(accountEntry.EntityID, ShouldEqual, created.Account.ID.String())
				So(accountEntry.Action, ShouldEqual, "created")
			})

			Convey("Then the entry is stamped with the AFFECTED tenant, never the default", func() {
				So(tenantEntry.TenantID, ShouldNotEqual, "default")
				So(accountEntry.TenantID, ShouldNotEqual, "default")
			})

			Convey("Then the account entry carries neither the token nor the stored hash", func() {
				_, secret, serr := serviceaccount.SplitToken(created.Token)
				So(serr, ShouldBeNil)
				var hash string
				So(pool.Get(&hash,
					`SELECT secret_hash FROM flexitype_service_account WHERE id = $1`,
					created.Account.ID.String()), ShouldBeNil)
				So(hash, ShouldNotBeBlank)

				So(accountEntry.payload(), ShouldNotContainSubstring, created.Token)
				So(accountEntry.payload(), ShouldNotContainSubstring, secret)
				So(accountEntry.payload(), ShouldNotContainSubstring, hash)
				So(accountEntry.payload(), ShouldNotContainSubstring, "secret_hash")
			})

			Convey("When the tenant is suspended", func() {
				entry := only(func() error { return a.SetTenantActive(ctx, "acme", false) })

				Convey("Then one updated entry records the active flip", func() {
					So(entry.TenantID, ShouldEqual, "acme")
					So(entry.Entity, ShouldEqual, admin.EntityTenant)
					So(entry.Action, ShouldEqual, "updated")
					So(entry.Before, ShouldContainSubstring, `"active": true`)
					So(entry.After, ShouldContainSubstring, `"active": false`)
				})
			})

			Convey("When the account's secret is rotated", func() {
				var rotated *admin.AccountWithToken
				entry := only(func() error {
					var err error
					rotated, err = a.RotateSecret(ctx, created.Account.ID.String())
					return err
				})

				Convey("Then one updated entry records THAT the secret changed, and nothing more", func() {
					So(entry.TenantID, ShouldEqual, "acme")
					So(entry.Entity, ShouldEqual, admin.EntityServiceAccount)
					So(entry.EntityID, ShouldEqual, created.Account.ID.String())
					So(entry.Action, ShouldEqual, "updated")
					So(entry.After, ShouldContainSubstring, `"secret_rotated": true`)
				})

				Convey("Then the new token is returned once and never lands in the log", func() {
					So(rotated.Token, ShouldNotBeBlank)
					So(rotated.Token, ShouldNotEqual, created.Token)

					_, newSecret, serr := serviceaccount.SplitToken(rotated.Token)
					So(serr, ShouldBeNil)
					_, oldSecret, serr := serviceaccount.SplitToken(created.Token)
					So(serr, ShouldBeNil)

					var hash string
					So(pool.Get(&hash,
						`SELECT secret_hash FROM flexitype_service_account WHERE id = $1`,
						created.Account.ID.String()), ShouldBeNil)

					// The rotation committed: the new secret verifies against
					// the stored hash and the old one no longer does.
					So(serviceaccount.VerifySecret(newSecret, hash), ShouldBeNil)
					So(serviceaccount.VerifySecret(oldSecret, hash), ShouldNotBeNil)

					// And the token is unrecoverable from the audit trail.
					for _, r := range readAuditRows(t, pool) {
						So(r.payload(), ShouldNotContainSubstring, rotated.Token)
						So(r.payload(), ShouldNotContainSubstring, newSecret)
						So(r.payload(), ShouldNotContainSubstring, hash)
					}
				})
			})

			Convey("When the account is revoked", func() {
				entry := only(func() error { return a.Revoke(ctx, created.Account.ID.String()) })

				Convey("Then one updated entry records the active flip", func() {
					So(entry.TenantID, ShouldEqual, "acme")
					So(entry.Entity, ShouldEqual, admin.EntityServiceAccount)
					So(entry.Action, ShouldEqual, "updated")
					So(entry.Before, ShouldContainSubstring, `"active": true`)
					So(entry.After, ShouldContainSubstring, `"active": false`)
				})
			})

			Convey("When the role is replaced", func() {
				entry := only(func() error {
					_, err := a.UpsertRole(ctx, admin.UpsertRoleInput{
						TenantName: "acme", Name: "reviewer",
						Description: "read-only reviewer",
						Scopes:      []string{"read"},
					})
					return err
				})

				Convey("Then one UPDATED entry records the role as STORED", func() {
					So(entry.TenantID, ShouldEqual, "acme")
					So(entry.Entity, ShouldEqual, admin.EntityRole)
					So(entry.EntityID, ShouldEqual, role.ID.String()) // the upsert keeps the id
					So(entry.Action, ShouldEqual, "updated")
					So(entry.After, ShouldContainSubstring, "read-only reviewer")
					So(entry.Before, ShouldContainSubstring, "salary") // the replaced restriction
				})
			})

			Convey("When the account's roles are reassigned", func() {
				entry := only(func() error {
					return a.AssignRoles(ctx, admin.AssignRolesInput{
						AccountID:        created.Account.ID.String(),
						Roles:            []string{},
						FieldPermissions: map[string]string{"salary": "read"},
					})
				})

				Convey("Then one updated entry records the roles before and after", func() {
					So(entry.TenantID, ShouldEqual, "acme")
					So(entry.Entity, ShouldEqual, admin.EntityServiceAccount)
					So(entry.EntityID, ShouldEqual, created.Account.ID.String())
					So(entry.Action, ShouldEqual, "updated")
					So(entry.Before, ShouldContainSubstring, "reviewer")
					So(entry.After, ShouldContainSubstring, `"salary": "read"`)
				})
			})

			Convey("When an unheld role is deleted", func() {
				So(a.AssignRoles(ctx, admin.AssignRolesInput{
					AccountID: created.Account.ID.String(), Roles: []string{},
				}), ShouldBeNil)
				entry := only(func() error { return a.DeleteRole(ctx, "acme", "reviewer") })

				Convey("Then one removed entry records the role that was deleted", func() {
					So(entry.TenantID, ShouldEqual, "acme")
					So(entry.Entity, ShouldEqual, admin.EntityRole)
					So(entry.EntityID, ShouldEqual, role.ID.String())
					So(entry.Action, ShouldEqual, "removed")
					So(entry.Before, ShouldContainSubstring, "reviewer")
				})
			})
		})

		Convey("When a usecase fails, it leaves no entry behind", func() {
			_, err := a.CreateTenant(ctx, "acme")
			So(err, ShouldBeNil)
			_, derr := pool.Exec(`DELETE FROM flexitype_activity_log`)
			So(derr, ShouldBeNil)

			_, err = a.CreateTenant(ctx, "acme") // conflict
			So(err, ShouldNotBeNil)
			_, err = a.CreateAccount(ctx, admin.CreateAccountInput{
				TenantName: "acme", Name: "typo-bot",
				Scopes: []string{"read"}, Roles: []string{"nope"},
			})
			So(err, ShouldNotBeNil)

			Convey("Then the activity log is still empty", func() {
				So(len(readAuditRows(t, pool)), ShouldEqual, 0)
			})
		})
	})
}

// TestAdminRoleAssignmentRacePostgres is the TOCTOU half of #507.
//
// DeleteRole counted the accounts holding a role and then deleted it, with no
// lock and no transaction between the two statements. An assignment naming the
// role could commit in that window, and the account was left holding a role
// that no longer existed — the unresolved-role state the count exists to
// prevent, which denies the principal every attribute.
//
// DeleteRole now takes an EXCLUSIVE row lock on the role BEFORE the count, and
// an assignment takes a SHARED lock on the roles it names before it writes the
// account. The two conflict, so exactly the racing pair is serialized. The
// test holds each lock from a raw transaction and asserts the other side
// WAITS.
func TestAdminRoleAssignmentRacePostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a tenant with a role and an account that does not hold it", t, func() {
		truncateAll(t, pool)
		a := svc.AdminInteractor()
		ctx := context.Background()

		_, err := a.CreateTenant(ctx, "acme")
		So(err, ShouldBeNil)
		_, err = a.UpsertRole(ctx, admin.UpsertRoleInput{
			TenantName: "acme", Name: "reviewer", Scopes: []string{"read"},
		})
		So(err, ShouldBeNil)
		acct, err := a.CreateAccount(ctx, admin.CreateAccountInput{
			TenantName: "acme", Name: "reviewer-bot", Scopes: []string{"read"},
		})
		So(err, ShouldBeNil)

		// lockRole holds the named lock mode on the role row from a raw
		// transaction. The returned release ALWAYS rolls back, so a failed
		// assertion cannot strand the lock and stall the package.
		lockRole := func(mode string) (release func()) {
			tx, terr := pool.Beginx()
			So(terr, ShouldBeNil)
			var name string
			terr = tx.Get(&name,
				`SELECT name FROM flexitype_role WHERE tenant_id = $1 AND name = $2 `+mode,
				"acme", "reviewer")
			if terr != nil {
				_ = tx.Rollback()
				So(terr, ShouldBeNil)
			}
			return func() { _ = tx.Rollback() }
		}

		waitsForLock := func(mode string, op func() error) {
			release := lockRole(mode)
			defer release() // never strand the lock, whatever the assertions do
			done := make(chan error, 1)
			go func() { done <- op() }()
			select {
			case err := <-done:
				So("returned before the role lock was released: "+errText(err), ShouldBeBlank)
			case <-time.After(400 * time.Millisecond):
				// Blocked on the role row lock: the serialization exists.
			}
			release()
			So(<-done, ShouldBeNil)
		}

		Convey("When an assignment holds the role's SHARED lock", func() {
			waitsForLock("FOR SHARE", func() error { return a.DeleteRole(ctx, "acme", "reviewer") })

			Convey("Then DeleteRole waited for it, and only then deleted the role", func() {
				roles, rerr := a.ListRoles(ctx, "acme")
				So(rerr, ShouldBeNil)
				So(len(roles), ShouldEqual, 0)
			})
		})

		Convey("When a delete holds the role's EXCLUSIVE lock", func() {
			waitsForLock("FOR UPDATE", func() error {
				return a.AssignRoles(ctx, admin.AssignRolesInput{
					AccountID: acct.Account.ID.String(), Roles: []string{"reviewer"},
				})
			})

			Convey("Then AssignRoles waited for it, and only then granted the role", func() {
				accounts, aerr := a.ListAccounts(ctx, "acme")
				So(aerr, ShouldBeNil)
				So(len(accounts), ShouldEqual, 1)
				So(strings.Join(accounts[0].Roles, ","), ShouldEqual, "reviewer")
			})
		})

		Convey("When a delete holds the role's EXCLUSIVE lock and an account is created with it", func() {
			waitsForLock("FOR UPDATE", func() error {
				_, cerr := a.CreateAccount(ctx, admin.CreateAccountInput{
					TenantName: "acme", Name: "second-bot", Roles: []string{"reviewer"},
				})
				return cerr
			})

			Convey("Then CreateAccount waited for it too", func() {
				accounts, aerr := a.ListAccounts(ctx, "acme")
				So(aerr, ShouldBeNil)
				So(len(accounts), ShouldEqual, 2)
			})
		})
	})
}
