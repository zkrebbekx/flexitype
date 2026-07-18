package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/admin"
	"github.com/zkrebbekx/flexitype/application/dedup"
	"github.com/zkrebbekx/flexitype/application/feed"
	"github.com/zkrebbekx/flexitype/application/outbox"
	"github.com/zkrebbekx/flexitype/application/unit"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// controlPlaneFixture opens the integration database, migrates it, and
// returns the pool plus a transactor. The control-plane tables it exercises
// (tenant, service account, activity log, match rule, unit family, event
// cursor) are truncated by each test that touches them, so this suite stays
// independent of the entity-oriented parity suites sharing the database.
func controlPlaneFixture(t *testing.T) (*sqlx.DB, db.Transactor) {
	t.Helper()
	pool := openIntegrationDB(t)
	t.Cleanup(func() { _ = pool.Close() })
	transactor := db.NewTransactor(pool)
	if err := postgres.Migrate(context.Background(), transactor); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool, transactor
}

// writeEnvelopesFor writes n envelopes for a tenant and event type through the
// outbox store, returning their ids in write order. It complements the
// fixed-tenant helper in outbox_integration_test.go.
func writeEnvelopesFor(ctx context.Context, tx db.Transactor, store outbox.Store, n int, tenant, typ string) []string {
	ids := make([]string, n)
	envs := make([]events.Envelope, n)
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		id := ulid.New().String()
		ids[i] = id
		envs[i] = events.Envelope{
			ID:            id,
			Type:          events.Type(typ),
			AggregateType: "test",
			AggregateID:   fmt.Sprintf("agg-%d", i),
			TenantID:      tenant,
			Actor:         "tester",
			OccurredAt:    now,
			RecordedAt:    now,
			SchemaVersion: events.SchemaVersion,
			Payload:       []byte(`{}`),
		}
	}
	if err := tx.InTransaction(ctx, func(t db.Transactor) error {
		return store.Write(ctx, t, envs)
	}); err != nil {
		panic(fmt.Sprintf("write envelopes: %v", err))
	}
	return ids
}

// insertSubscription creates a webhook subscription row directly, so delivery
// fixtures can satisfy the delivery table's foreign key without going through
// the subscription usecases.
func insertSubscription(pool *sqlx.DB, tenant, name string) ulid.ID {
	id := ulid.New()
	now := time.Now().UTC()
	pool.MustExec(
		`INSERT INTO flexitype_webhook_subscription
		   (id, tenant_id, name, url, secret, previous_secret, event_types, active, created_at, updated_at)
		 VALUES ($1, $2, $3, 'https://example.test/hook', 'shh', '', '{}', TRUE, $4, $4)`,
		id.String(), tenant, name, now)
	return id
}

// insertDelivery creates a delivery row in a given status for an envelope.
func insertDelivery(pool *sqlx.DB, subID ulid.ID, envelopeID, tenant, status string, feedSeq int64) ulid.ID {
	id := ulid.New()
	now := time.Now().UTC()
	pool.MustExec(
		`INSERT INTO flexitype_webhook_delivery
		   (id, subscription_id, envelope_id, tenant_id, event_type, feed_seq,
		    status, attempts, next_attempt_at, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, 'flexitype.entity.created', $5, $6, 0, $7, $7, $7)`,
		id.String(), subID.String(), envelopeID, tenant, feedSeq, status, now)
	return id
}

// --- tenants and service accounts -------------------------------------------

func TestAdminStoreTenantsIntegration(t *testing.T) {
	pool, _ := controlPlaneFixture(t)
	ctx := context.Background()
	store := postgres.NewAdminStore(pool)

	Convey("Given three tenants created out of alphabetical order", t, func() {
		// Service accounts reference tenants, so clear both.
		pool.MustExec(`TRUNCATE flexitype_service_account, flexitype_tenant CASCADE`)
		created := time.Now().UTC().Truncate(time.Millisecond)
		for _, name := range []string{"zulu", "alpha", "mike"} {
			So(store.CreateTenant(ctx, admin.Tenant{
				ID: ulid.New(), Name: name, Active: true,
				CreatedAt: created, UpdatedAt: created,
			}), ShouldBeNil)
		}

		Convey("When they are listed", func() {
			tenants, err := store.ListTenants(ctx)

			Convey("Then every tenant comes back ordered by name", func() {
				So(err, ShouldBeNil)
				So(tenants, ShouldHaveLength, 3)
				names := []string{tenants[0].Name, tenants[1].Name, tenants[2].Name}
				So(names, ShouldResemble, []string{"alpha", "mike", "zulu"})
				So(tenants[0].Active, ShouldBeTrue)
				So(tenants[0].ID.String(), ShouldNotBeBlank)
			})
		})

		Convey("When one is looked up by name", func() {
			got, err := store.GetTenantByName(ctx, "mike")

			Convey("Then that tenant is returned with its timestamps", func() {
				So(err, ShouldBeNil)
				So(got.Name, ShouldEqual, "mike")
				So(got.CreatedAt.UTC(), ShouldHappenWithin, time.Second, created)
			})
		})

		Convey("When an unknown name is looked up", func() {
			_, err := store.GetTenantByName(ctx, "nobody")

			Convey("Then a domain not-found error names the tenant", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "nobody")
			})
		})

		Convey("When a tenant is deactivated", func() {
			later := created.Add(time.Hour)
			So(store.SetTenantActive(ctx, "mike", false, later), ShouldBeNil)

			Convey("Then only that tenant flips to inactive and its updated_at advances", func() {
				got, err := store.GetTenantByName(ctx, "mike")
				So(err, ShouldBeNil)
				So(got.Active, ShouldBeFalse)
				So(got.UpdatedAt.UTC(), ShouldHappenWithin, time.Second, later)

				other, err := store.GetTenantByName(ctx, "alpha")
				So(err, ShouldBeNil)
				So(other.Active, ShouldBeTrue)
			})

			Convey("And reactivating it restores the active flag", func() {
				So(store.SetTenantActive(ctx, "mike", true, later.Add(time.Hour)), ShouldBeNil)
				got, err := store.GetTenantByName(ctx, "mike")
				So(err, ShouldBeNil)
				So(got.Active, ShouldBeTrue)
			})
		})

		Convey("When an unknown tenant is deactivated", func() {
			err := store.SetTenantActive(ctx, "ghost", false, created)

			Convey("Then the no-op update succeeds without touching other rows", func() {
				So(err, ShouldBeNil)
				tenants, listErr := store.ListTenants(ctx)
				So(listErr, ShouldBeNil)
				So(tenants, ShouldHaveLength, 3)
			})
		})
	})
}

func TestAdminStoreAccountsIntegration(t *testing.T) {
	pool, _ := controlPlaneFixture(t)
	ctx := context.Background()
	store := postgres.NewAdminStore(pool)

	Convey("Given two service accounts on one tenant and one on another", t, func() {
		pool.MustExec(`TRUNCATE flexitype_service_account, flexitype_tenant CASCADE`)
		now := time.Now().UTC().Truncate(time.Millisecond)
		for _, tenant := range []string{"acme", "other"} {
			So(store.CreateTenant(ctx, admin.Tenant{
				ID: ulid.New(), Name: tenant, Active: true, CreatedAt: now, UpdatedAt: now,
			}), ShouldBeNil)
		}

		writer := admin.ServiceAccount{
			ID: ulid.New(), TenantID: "acme", Name: "writer",
			Scopes:    []serviceaccount.Scope{"values:write", "values:read"},
			Active:    true,
			CreatedAt: now, UpdatedAt: now,
		}
		reader := admin.ServiceAccount{
			ID: ulid.New(), TenantID: "acme", Name: "auditor",
			Scopes: []serviceaccount.Scope{"values:read"}, Active: true,
			CreatedAt: now, UpdatedAt: now,
		}
		foreign := admin.ServiceAccount{
			ID: ulid.New(), TenantID: "other", Name: "outsider",
			Scopes: nil, Active: true, CreatedAt: now, UpdatedAt: now,
		}
		So(store.CreateAccount(ctx, writer, serviceaccount.HashSecret("s3cret")), ShouldBeNil)
		So(store.CreateAccount(ctx, reader, serviceaccount.HashSecret("other")), ShouldBeNil)
		So(store.CreateAccount(ctx, foreign, serviceaccount.HashSecret("nope")), ShouldBeNil)

		Convey("When one tenant's accounts are listed", func() {
			accounts, err := store.ListAccounts(ctx, "acme")

			Convey("Then only that tenant's accounts return, ordered by name, with scopes intact", func() {
				So(err, ShouldBeNil)
				So(accounts, ShouldHaveLength, 2)
				So(accounts[0].Name, ShouldEqual, "auditor")
				So(accounts[1].Name, ShouldEqual, "writer")
				So(accounts[1].Scopes, ShouldResemble,
					[]serviceaccount.Scope{"values:write", "values:read"})
			})
		})

		Convey("When an account with no scopes is listed", func() {
			accounts, err := store.ListAccounts(ctx, "other")

			Convey("Then it round-trips as an empty scope set, not a nil surprise", func() {
				So(err, ShouldBeNil)
				So(accounts, ShouldHaveLength, 1)
				So(accounts[0].Scopes, ShouldBeEmpty)
			})
		})

		Convey("When an account is fetched by id", func() {
			got, err := store.GetAccount(ctx, writer.ID)

			Convey("Then the full account is returned", func() {
				So(err, ShouldBeNil)
				So(got.Name, ShouldEqual, "writer")
				So(got.TenantID, ShouldEqual, "acme")
				So(got.Active, ShouldBeTrue)
				So(got.Scopes, ShouldHaveLength, 2)
			})
		})

		Convey("When an unknown account id is fetched", func() {
			missing := ulid.New()
			_, err := store.GetAccount(ctx, missing)

			Convey("Then a domain not-found error names the account", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, missing.String())
			})
		})

		Convey("When an account is deactivated", func() {
			later := now.Add(time.Hour)
			So(store.SetAccountActive(ctx, writer.ID, false, later), ShouldBeNil)

			Convey("Then only that account is inactive and its updated_at advances", func() {
				got, err := store.GetAccount(ctx, writer.ID)
				So(err, ShouldBeNil)
				So(got.Active, ShouldBeFalse)
				So(got.UpdatedAt.UTC(), ShouldHappenWithin, time.Second, later)

				sibling, err := store.GetAccount(ctx, reader.ID)
				So(err, ShouldBeNil)
				So(sibling.Active, ShouldBeTrue)
			})
		})

		Convey("When an account's secret is rotated", func() {
			later := now.Add(time.Hour)
			So(store.UpdateSecret(ctx, writer.ID, serviceaccount.HashSecret("rotated"), later), ShouldBeNil)

			Convey("Then the stored hash matches the new secret and not the old one", func() {
				var hash string
				So(pool.Get(&hash,
					`SELECT secret_hash FROM flexitype_service_account WHERE id = $1`,
					writer.ID.String()), ShouldBeNil)
				So(serviceaccount.VerifySecret("rotated", hash), ShouldBeNil)
				So(serviceaccount.VerifySecret("s3cret", hash), ShouldNotBeNil)

				got, err := store.GetAccount(ctx, writer.ID)
				So(err, ShouldBeNil)
				So(got.UpdatedAt.UTC(), ShouldHappenWithin, time.Second, later)
			})
		})
	})
}

func TestAccountLookupIntegration(t *testing.T) {
	pool, _ := controlPlaneFixture(t)
	ctx := context.Background()
	store := postgres.NewAdminStore(pool)
	lookup := postgres.NewAccountLookup(pool)

	Convey("Given an active service account with a known secret", t, func() {
		pool.MustExec(`TRUNCATE flexitype_service_account, flexitype_tenant CASCADE`)
		now := time.Now().UTC()
		So(store.CreateTenant(ctx, admin.Tenant{
			ID: ulid.New(), Name: "acme", Active: true, CreatedAt: now, UpdatedAt: now,
		}), ShouldBeNil)

		acct := admin.ServiceAccount{
			ID: ulid.New(), TenantID: "acme", Name: "relay",
			Scopes: []serviceaccount.Scope{"values:read"}, Active: true,
			CreatedAt: now, UpdatedAt: now,
		}
		So(store.CreateAccount(ctx, acct, serviceaccount.HashSecret("open-sesame")), ShouldBeNil)
		token := serviceaccount.MintToken(acct.ID.String(), "open-sesame")

		Convey("When the matching token is authenticated with a context", func() {
			got, err := lookup.AuthenticateCtx(ctx, token)

			Convey("Then the account resolves with its tenant and scopes", func() {
				So(err, ShouldBeNil)
				So(got.ID, ShouldEqual, acct.ID.String())
				So(got.Name, ShouldEqual, "relay")
				So(got.TenantID, ShouldEqual, "acme")
				So(got.Tenant(), ShouldEqual, valueobjects.TenantID("acme"))
				So(got.HasScope("values:read"), ShouldBeTrue)
				So(got.HasScope("values:write"), ShouldBeFalse)
			})
		})

		Convey("When the same token is authenticated without a context", func() {
			got, err := lookup.Authenticate(token)

			Convey("Then the background-context path resolves identically", func() {
				So(err, ShouldBeNil)
				So(got.ID, ShouldEqual, acct.ID.String())
				So(got.TenantID, ShouldEqual, "acme")
			})
		})

		Convey("When the token carries the wrong secret", func() {
			_, err := lookup.AuthenticateCtx(ctx, serviceaccount.MintToken(acct.ID.String(), "wrong"))

			Convey("Then authentication is refused", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When the token names an account that does not exist", func() {
			_, err := lookup.AuthenticateCtx(ctx, serviceaccount.MintToken(ulid.New().String(), "open-sesame"))

			Convey("Then authentication is refused without leaking that the id is unknown", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldNotContainSubstring, "not found")
			})
		})

		Convey("When the token is malformed", func() {
			_, err := lookup.AuthenticateCtx(ctx, "not-a-token")

			Convey("Then the split failure surfaces before any query runs", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When the account is revoked", func() {
			So(store.SetAccountActive(ctx, acct.ID, false, now), ShouldBeNil)
			_, err := lookup.AuthenticateCtx(ctx, token)

			Convey("Then the correct secret is still rejected as revoked", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "revoked")
			})
		})

		Convey("When the context is already cancelled", func() {
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			_, err := lookup.AuthenticateCtx(cancelled, token)

			Convey("Then the lookup failure is wrapped with its query context", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "look up service account")
				So(errors.Is(err, context.Canceled), ShouldBeTrue)
			})
		})
	})
}

// --- activity log -----------------------------------------------------------

func TestActivityLogIntegration(t *testing.T) {
	pool, transactor := controlPlaneFixture(t)
	ctx := context.Background()
	log := postgres.NewActivityLog(pool)

	writeEntries := func(entries ...activity.Entry) {
		So(transactor.InTransaction(ctx, func(tx db.Transactor) error {
			return log.Write(ctx, tx, entries)
		}), ShouldBeNil)
	}

	Convey("Given an audit trail for two tenants across two entities", t, func() {
		pool.MustExec(`TRUNCATE flexitype_activity_log`)
		base := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
		entry := func(tenant, actor, entity, entityID string, action activity.Action, offset time.Duration) activity.Entry {
			return activity.Entry{
				ID: ulid.New(), TenantID: valueobjects.TenantID(tenant),
				Actor: actor, Entity: entity, EntityID: entityID, Action: action,
				OccurredAt: base.Add(offset),
			}
		}
		oldest := entry("default", "alice", "entity", "e1", activity.ActionCreated, 0)
		middle := entry("default", "bob", "entity", "e1", activity.ActionUpdated, time.Minute)
		newest := entry("default", "alice", "typedef", "t1", activity.ActionArchived, 2*time.Minute)
		foreign := entry("other", "mallory", "entity", "e1", activity.ActionRemoved, 3*time.Minute)
		writeEntries(oldest, middle, newest, foreign)

		filter := activity.Filter{TenantID: "default"}

		Convey("When the tenant's log is listed with a total requested", func() {
			entries, total, err := log.List(ctx, filter, db.Page{Limit: 10, WantTotal: true})

			Convey("Then only that tenant's entries return newest-first with the full count", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 3)
				So(entries, ShouldHaveLength, 3)
				So(entries[0].ID, ShouldEqual, newest.ID)
				So(entries[1].ID, ShouldEqual, middle.ID)
				So(entries[2].ID, ShouldEqual, oldest.ID)
				for _, e := range entries {
					So(e.TenantID, ShouldEqual, valueobjects.TenantID("default"))
				}
			})
		})

		Convey("When no total is requested", func() {
			entries, total, err := log.List(ctx, filter, db.Page{Limit: 10})

			Convey("Then the rows come back but the count query is skipped", func() {
				So(err, ShouldBeNil)
				So(entries, ShouldHaveLength, 3)
				So(total, ShouldEqual, 0)
			})
		})

		Convey("When filtered by entity type", func() {
			entries, total, err := log.List(ctx,
				activity.Filter{TenantID: "default", Entity: "typedef"},
				db.Page{Limit: 10, WantTotal: true})

			Convey("Then only entries for that entity type return and the total narrows too", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 1)
				So(entries, ShouldHaveLength, 1)
				So(entries[0].Entity, ShouldEqual, "typedef")
				So(entries[0].Action, ShouldEqual, activity.ActionArchived)
			})
		})

		Convey("When filtered by entity id and actor together", func() {
			entries, total, err := log.List(ctx,
				activity.Filter{TenantID: "default", EntityID: "e1", Actor: "bob"},
				db.Page{Limit: 10, WantTotal: true})

			Convey("Then both predicates apply", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 1)
				So(entries, ShouldHaveLength, 1)
				So(entries[0].ID, ShouldEqual, middle.ID)
			})
		})

		Convey("When a caller filters by an actor with no entries", func() {
			entries, total, err := log.List(ctx,
				activity.Filter{TenantID: "default", Actor: "nobody"},
				db.Page{Limit: 10, WantTotal: true})

			Convey("Then an empty page and a zero total come back without error", func() {
				So(err, ShouldBeNil)
				So(entries, ShouldBeEmpty)
				So(total, ShouldEqual, 0)
			})
		})

		Convey("When an empty batch is written", func() {
			err := transactor.InTransaction(ctx, func(tx db.Transactor) error {
				return log.Write(ctx, tx, nil)
			})

			Convey("Then the write short-circuits and leaves the log untouched", func() {
				So(err, ShouldBeNil)
				var n int
				So(pool.Get(&n, `SELECT count(*) FROM flexitype_activity_log`), ShouldBeNil)
				So(n, ShouldEqual, 4)
			})
		})
	})

	Convey("Given an entry carrying before and after state", t, func() {
		pool.MustExec(`TRUNCATE flexitype_activity_log`)
		now := time.Now().UTC()
		withState := activity.Entry{
			ID: ulid.New(), TenantID: "default", Actor: "alice",
			Entity: "entity", EntityID: "e9", Action: activity.ActionUpdated,
			Before:     json.RawMessage(`{"name":"before"}`),
			After:      json.RawMessage(`{"name":"after"}`),
			OccurredAt: now,
		}
		bare := activity.Entry{
			ID: ulid.New(), TenantID: "default", Actor: "alice",
			Entity: "entity", EntityID: "e8", Action: activity.ActionCreated,
			OccurredAt: now.Add(-time.Minute),
		}
		writeEntries(withState, bare)

		Convey("When the log is read back", func() {
			entries, _, err := log.List(ctx, activity.Filter{TenantID: "default"}, db.Page{Limit: 10})

			Convey("Then jsonb state round-trips and absent state stays nil rather than empty JSON", func() {
				So(err, ShouldBeNil)
				So(entries, ShouldHaveLength, 2)

				So(entries[0].ID, ShouldEqual, withState.ID)
				var before, after map[string]string
				So(json.Unmarshal(entries[0].Before, &before), ShouldBeNil)
				So(json.Unmarshal(entries[0].After, &after), ShouldBeNil)
				So(before["name"], ShouldEqual, "before")
				So(after["name"], ShouldEqual, "after")

				So(entries[1].ID, ShouldEqual, bare.ID)
				So(entries[1].Before, ShouldBeNil)
				So(entries[1].After, ShouldBeNil)
			})
		})
	})

	Convey("Given more entries than fit on one page", t, func() {
		pool.MustExec(`TRUNCATE flexitype_activity_log`)
		base := time.Now().UTC().Add(-time.Hour)
		ids := make([]ulid.ID, 5)
		for i := range ids {
			ids[i] = ulid.New()
			writeEntries(activity.Entry{
				ID: ids[i], TenantID: "default", Actor: "alice",
				Entity: "entity", EntityID: "e1", Action: activity.ActionUpdated,
				// Ascending time, so the newest-first list reverses this order.
				OccurredAt: base.Add(time.Duration(i) * time.Minute),
			})
		}

		Convey("When the first page of two is fetched", func() {
			// FetchLimit is Limit+1, so a limit of 2 fetches 3 rows.
			first, _, err := log.List(ctx, activity.Filter{TenantID: "default"}, db.Page{Limit: 2})

			Convey("Then it returns the newest entries plus the lookahead row", func() {
				So(err, ShouldBeNil)
				So(first, ShouldHaveLength, 3)
				So(first[0].ID, ShouldEqual, ids[4])
				So(first[1].ID, ShouldEqual, ids[3])
				So(first[2].ID, ShouldEqual, ids[2])
			})
		})
	})
}

// --- duplicate-detection rules ----------------------------------------------

func TestMatchStoreIntegration(t *testing.T) {
	pool, _ := controlPlaneFixture(t)
	ctx := context.Background()
	store := postgres.NewMatchStore(pool)

	Convey("Given match rules on two type definitions and two tenants", t, func() {
		pool.MustExec(`TRUNCATE flexitype_match_rule, flexitype_match_dismissal CASCADE`)
		base := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
		rule := func(tenant, typeDef, attr string, strategy dedup.Strategy, offset time.Duration) dedup.Rule {
			return dedup.Rule{
				ID: ulid.New(), TenantID: valueobjects.TenantID(tenant),
				TypeDefinitionID: typeDef, AttributeDefinitionID: attr,
				Strategy: strategy, Threshold: 0.85, CreatedAt: base.Add(offset),
			}
		}
		second := rule("default", "type-a", "attr-2", dedup.Strategy("exact"), time.Minute)
		first := rule("default", "type-a", "attr-1", dedup.Strategy("exact"), 0)
		otherType := rule("default", "type-b", "attr-3", dedup.Strategy("exact"), 2*time.Minute)
		otherTenant := rule("other", "type-a", "attr-4", dedup.Strategy("exact"), 3*time.Minute)
		for _, r := range []dedup.Rule{second, first, otherType, otherTenant} {
			So(store.CreateRule(ctx, r), ShouldBeNil)
		}

		Convey("When rules are listed for one type definition", func() {
			rules, err := store.ListRules(ctx, "default", "type-a")

			Convey("Then only that tenant's rules for that type return, oldest-first", func() {
				So(err, ShouldBeNil)
				So(rules, ShouldHaveLength, 2)
				So(rules[0].ID, ShouldEqual, first.ID)
				So(rules[1].ID, ShouldEqual, second.ID)
				So(rules[0].AttributeDefinitionID, ShouldEqual, "attr-1")
				So(rules[0].Threshold, ShouldAlmostEqual, 0.85, 0.0001)
				So(rules[0].TenantID, ShouldEqual, valueobjects.TenantID("default"))
			})
		})

		Convey("When rules are listed for a type definition with none", func() {
			rules, err := store.ListRules(ctx, "default", "type-zzz")

			Convey("Then an empty, non-nil slice comes back", func() {
				So(err, ShouldBeNil)
				So(rules, ShouldBeEmpty)
				So(rules, ShouldNotBeNil)
			})
		})

		Convey("When a rule is deleted", func() {
			So(store.DeleteRule(ctx, "default", first.ID), ShouldBeNil)

			Convey("Then it is gone and its siblings survive", func() {
				_, err := store.GetRule(ctx, "default", first.ID)
				So(domainerrors.IsNotFound(err), ShouldBeTrue)

				rules, listErr := store.ListRules(ctx, "default", "type-a")
				So(listErr, ShouldBeNil)
				So(rules, ShouldHaveLength, 1)
				So(rules[0].ID, ShouldEqual, second.ID)
			})
		})

		Convey("When a delete is scoped to the wrong tenant", func() {
			So(store.DeleteRule(ctx, "other", first.ID), ShouldBeNil)

			Convey("Then the tenant predicate protects the row", func() {
				got, err := store.GetRule(ctx, "default", first.ID)
				So(err, ShouldBeNil)
				So(got.ID, ShouldEqual, first.ID)
			})
		})

		Convey("When a rule with dismissals is deleted", func() {
			So(store.Dismiss(ctx, dedup.Dismissal{
				RuleID: first.ID, TenantID: "default", EntityA: "a", EntityB: "b",
			}), ShouldBeNil)
			// A repeat dismissal must be absorbed by ON CONFLICT DO NOTHING.
			So(store.Dismiss(ctx, dedup.Dismissal{
				RuleID: first.ID, TenantID: "default", EntityA: "a", EntityB: "b",
			}), ShouldBeNil)
			before, err := store.ListDismissals(ctx, "default", first.ID)
			So(err, ShouldBeNil)
			So(before, ShouldHaveLength, 1)

			So(store.DeleteRule(ctx, "default", first.ID), ShouldBeNil)

			Convey("Then its dismissals cascade away with it", func() {
				after, listErr := store.ListDismissals(ctx, "default", first.ID)
				So(listErr, ShouldBeNil)
				So(after, ShouldBeEmpty)
			})
		})

		Convey("When the pool is closed under the store", func() {
			So(pool.Close(), ShouldBeNil)

			Convey("Then each read and write wraps the driver failure with its operation", func() {
				_, listErr := store.ListRules(ctx, "default", "type-a")
				So(listErr, ShouldNotBeNil)
				So(listErr.Error(), ShouldContainSubstring, "list match rules")

				_, getErr := store.GetRule(ctx, "default", first.ID)
				So(getErr, ShouldNotBeNil)
				So(getErr.Error(), ShouldContainSubstring, "get match rule")

				delErr := store.DeleteRule(ctx, "default", first.ID)
				So(delErr, ShouldNotBeNil)
				So(delErr.Error(), ShouldContainSubstring, "delete match rule")

				createErr := store.CreateRule(ctx, first)
				So(createErr, ShouldNotBeNil)
				So(createErr.Error(), ShouldContainSubstring, "insert match rule")

				dismissErr := store.Dismiss(ctx, dedup.Dismissal{
					RuleID: first.ID, TenantID: "default", EntityA: "a", EntityB: "b",
				})
				So(dismissErr, ShouldNotBeNil)
				So(dismissErr.Error(), ShouldContainSubstring, "insert match dismissal")

				_, dismissalsErr := store.ListDismissals(ctx, "default", first.ID)
				So(dismissalsErr, ShouldNotBeNil)
				So(dismissalsErr.Error(), ShouldContainSubstring, "list match dismissals")
			})
		})
	})
}

// --- unit families ----------------------------------------------------------

func TestUnitStoreIntegration(t *testing.T) {
	pool, _ := controlPlaneFixture(t)
	ctx := context.Background()
	store := postgres.NewUnitFamilyStore(pool)

	Convey("Given unit families on two tenants", t, func() {
		pool.MustExec(`TRUNCATE flexitype_unit_family CASCADE`)
		mass := unit.Family{
			ID: ulid.New(), TenantID: "default", Name: "mass", BaseUnit: "g",
			Units: map[string]float64{"g": 1, "kg": 1000},
		}
		length := unit.Family{
			ID: ulid.New(), TenantID: "default", Name: "length", BaseUnit: "m",
			Units: map[string]float64{"m": 1, "cm": 0.01},
		}
		foreign := unit.Family{
			ID: ulid.New(), TenantID: "other", Name: "mass", BaseUnit: "g",
			Units: map[string]float64{"g": 1},
		}
		for _, f := range []unit.Family{mass, length, foreign} {
			So(store.Create(ctx, f), ShouldBeNil)
		}

		Convey("When one tenant's families are listed", func() {
			families, err := store.List(ctx, "default")

			Convey("Then only its families return by name with conversion factors intact", func() {
				So(err, ShouldBeNil)
				So(families, ShouldHaveLength, 2)
				So(families[0].Name, ShouldEqual, "length")
				So(families[1].Name, ShouldEqual, "mass")
				So(families[1].Units["kg"], ShouldAlmostEqual, 1000.0, 0.0001)
				So(families[0].Units["cm"], ShouldAlmostEqual, 0.01, 0.000001)
			})
		})

		Convey("When a family is deleted", func() {
			So(store.Delete(ctx, "default", mass.ID), ShouldBeNil)

			Convey("Then it is gone and its sibling and the other tenant survive", func() {
				_, err := store.Get(ctx, "default", mass.ID)
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, mass.ID.String())

				remaining, listErr := store.List(ctx, "default")
				So(listErr, ShouldBeNil)
				So(remaining, ShouldHaveLength, 1)
				So(remaining[0].Name, ShouldEqual, "length")

				stillThere, getErr := store.Get(ctx, "other", foreign.ID)
				So(getErr, ShouldBeNil)
				So(stillThere.Name, ShouldEqual, "mass")
			})
		})

		Convey("When a delete names the wrong tenant", func() {
			So(store.Delete(ctx, "other", mass.ID), ShouldBeNil)

			Convey("Then the tenant predicate protects the row", func() {
				got, err := store.Get(ctx, "default", mass.ID)
				So(err, ShouldBeNil)
				So(got.Name, ShouldEqual, "mass")
			})
		})

		Convey("When a nonexistent family is deleted", func() {
			err := store.Delete(ctx, "default", ulid.New())

			Convey("Then the delete is a silent no-op", func() {
				So(err, ShouldBeNil)
				remaining, listErr := store.List(ctx, "default")
				So(listErr, ShouldBeNil)
				So(remaining, ShouldHaveLength, 2)
			})
		})

		Convey("When the pool is closed under the store", func() {
			So(pool.Close(), ShouldBeNil)

			Convey("Then each operation wraps the driver failure with its operation", func() {
				delErr := store.Delete(ctx, "default", mass.ID)
				So(delErr, ShouldNotBeNil)
				So(delErr.Error(), ShouldContainSubstring, "delete unit family")

				_, listErr := store.List(ctx, "default")
				So(listErr, ShouldNotBeNil)
				So(listErr.Error(), ShouldContainSubstring, "list unit families")

				_, getErr := store.Get(ctx, "default", mass.ID)
				So(getErr, ShouldNotBeNil)
				So(getErr.Error(), ShouldContainSubstring, "get unit family")

				createErr := store.Create(ctx, mass)
				So(createErr, ShouldNotBeNil)
				So(createErr.Error(), ShouldContainSubstring, "insert unit family")
			})
		})
	})
}

// --- feed and cursors -------------------------------------------------------

func TestFeedStoreIntegration(t *testing.T) {
	pool, transactor := controlPlaneFixture(t)
	ctx := context.Background()
	outboxStore := postgres.NewOutboxStore(transactor)
	feedStore := postgres.NewFeedStore(pool)

	// stampFeed writes envelopes and assigns feed_seq the way the relay does,
	// so the feed sees an expanded log.
	stampFeed := func(n int, tenant string, typ string) []string {
		ids := writeEnvelopesFor(ctx, transactor, outboxStore, n, tenant, typ)
		for _, id := range ids {
			pool.MustExec(
				`UPDATE flexitype_event_outbox
				   SET feed_seq = nextval('flexitype_event_feed_seq'), dispatched_at = now()
				 WHERE id = $1`, id)
		}
		return ids
	}

	Convey("Given an expanded feed for two tenants with two event types", t, func() {
		pool.MustExec(`TRUNCATE flexitype_event_outbox, flexitype_webhook_delivery,
			flexitype_webhook_subscription, flexitype_event_cursor CASCADE`)
		created := stampFeed(3, "default", "flexitype.entity.created")
		updated := stampFeed(2, "default", "flexitype.entity.updated")
		stampFeed(2, "other", "flexitype.entity.created")

		Convey("When the tenant's feed is read from the beginning", func() {
			events, err := feedStore.List(ctx, "default", 0, nil, 100)

			Convey("Then only its events return in ascending feed order", func() {
				So(err, ShouldBeNil)
				So(events, ShouldHaveLength, 5)
				for i := 1; i < len(events); i++ {
					So(events[i].Seq, ShouldBeGreaterThan, events[i-1].Seq)
				}
				for _, e := range events {
					So(e.Envelope.TenantID, ShouldEqual, "default")
				}
				So(events[0].Envelope.ID, ShouldEqual, created[0])
				So(events[4].Envelope.ID, ShouldEqual, updated[1])
			})
		})

		Convey("When the feed is read after a cursor position", func() {
			all, err := feedStore.List(ctx, "default", 0, nil, 100)
			So(err, ShouldBeNil)
			after := all[1].Seq
			rest, err := feedStore.List(ctx, "default", after, nil, 100)

			Convey("Then only strictly later events return", func() {
				So(err, ShouldBeNil)
				So(rest, ShouldHaveLength, 3)
				So(rest[0].Seq, ShouldBeGreaterThan, after)
			})
		})

		Convey("When the feed is filtered by event type", func() {
			events, err := feedStore.List(ctx, "default", 0, []string{"flexitype.entity.updated"}, 100)

			Convey("Then only matching types return", func() {
				So(err, ShouldBeNil)
				So(events, ShouldHaveLength, 2)
				for _, e := range events {
					So(string(e.Envelope.Type), ShouldEqual, "flexitype.entity.updated")
				}
			})
		})

		Convey("When the feed is filtered by several event types", func() {
			events, err := feedStore.List(ctx, "default", 0,
				[]string{"flexitype.entity.created", "flexitype.entity.updated"}, 100)

			Convey("Then the ANY() predicate matches all of them", func() {
				So(err, ShouldBeNil)
				So(events, ShouldHaveLength, 5)
			})
		})

		Convey("When the feed is read with a limit", func() {
			events, err := feedStore.List(ctx, "default", 0, nil, 2)

			Convey("Then the page is truncated to the limit", func() {
				So(err, ShouldBeNil)
				So(events, ShouldHaveLength, 2)
				So(events[0].Envelope.ID, ShouldEqual, created[0])
			})
		})

		Convey("When the envelope payload is read back", func() {
			events, err := feedStore.List(ctx, "default", 0, nil, 1)

			Convey("Then the jsonb payload round-trips as raw JSON with its metadata", func() {
				So(err, ShouldBeNil)
				So(events, ShouldHaveLength, 1)
				e := events[0].Envelope
				So(string(e.Payload), ShouldEqual, `{}`)
				So(e.AggregateType, ShouldEqual, "test")
				So(e.Actor, ShouldEqual, "tester")
				So(e.OccurredAt.IsZero(), ShouldBeFalse)
				So(e.RecordedAt.IsZero(), ShouldBeFalse)
			})
		})

		Convey("When the retention floor is asked for", func() {
			floor, err := feedStore.Floor(ctx, "default")

			Convey("Then it is the smallest retained sequence for that tenant", func() {
				So(err, ShouldBeNil)
				all, listErr := feedStore.List(ctx, "default", 0, nil, 100)
				So(listErr, ShouldBeNil)
				So(floor, ShouldEqual, all[0].Seq)
			})
		})

		Convey("When the floor is asked for a tenant with no events", func() {
			floor, err := feedStore.Floor(ctx, "empty-tenant")

			Convey("Then it coalesces to zero rather than failing", func() {
				So(err, ShouldBeNil)
				So(floor, ShouldEqual, int64(0))
			})
		})
	})

	Convey("Given a feed where one envelope still has an unsettled delivery", t, func() {
		pool.MustExec(`TRUNCATE flexitype_event_outbox, flexitype_webhook_delivery,
			flexitype_webhook_subscription, flexitype_event_cursor CASCADE`)
		ids := stampFeed(3, "default", "flexitype.entity.created")
		// Age every envelope past the cutoff.
		cutoff := time.Now().UTC().Add(time.Hour)
		pool.MustExec(`UPDATE flexitype_event_outbox SET recorded_at = now() - interval '2 days'`)
		// One envelope keeps a pending delivery, which must pin it.
		sub := insertSubscription(pool, "default", "pinner")
		insertDelivery(pool, sub, ids[0], "default", "pending", 1)

		Convey("When the feed is pruned to the cutoff", func() {
			removed, err := feedStore.Prune(ctx, cutoff)

			Convey("Then settled envelopes are deleted and the pinned one survives", func() {
				So(err, ShouldBeNil)
				So(removed, ShouldEqual, 2)

				remaining, listErr := feedStore.List(ctx, "default", 0, nil, 100)
				So(listErr, ShouldBeNil)
				So(remaining, ShouldHaveLength, 1)
				So(remaining[0].Envelope.ID, ShouldEqual, ids[0])
			})
		})

		Convey("When the feed is pruned to a cutoff before every envelope", func() {
			removed, err := feedStore.Prune(ctx, time.Now().UTC().Add(-72*time.Hour))

			Convey("Then nothing is removed", func() {
				So(err, ShouldBeNil)
				So(removed, ShouldEqual, 0)
			})
		})
	})

	Convey("Given a closed pool", t, func() {
		closed := openIntegrationDB(t)
		So(closed.Close(), ShouldBeNil)
		broken := postgres.NewFeedStore(closed)

		Convey("When each feed read runs", func() {
			_, listErr := broken.List(ctx, "default", 0, nil, 10)
			_, floorErr := broken.Floor(ctx, "default")
			_, pruneErr := broken.Prune(ctx, time.Now())

			Convey("Then each wraps the driver failure with its own operation", func() {
				So(listErr, ShouldNotBeNil)
				So(listErr.Error(), ShouldContainSubstring, "list feed events")
				So(floorErr, ShouldNotBeNil)
				So(floorErr.Error(), ShouldContainSubstring, "feed floor")
				So(pruneErr, ShouldNotBeNil)
				So(pruneErr.Error(), ShouldContainSubstring, "prune events")
			})
		})
	})
}

func TestCursorStoreIntegration(t *testing.T) {
	pool, _ := controlPlaneFixture(t)
	ctx := context.Background()
	cursors := postgres.NewCursorStore(pool)
	now := time.Now().UTC()

	Convey("Given a consumer that has never committed a cursor", t, func() {
		pool.MustExec(`TRUNCATE flexitype_event_cursor`)

		Convey("When its position is read", func() {
			pos, err := cursors.Get(ctx, "default", "reporting")

			Convey("Then it starts at zero rather than erroring", func() {
				So(err, ShouldBeNil)
				So(pos, ShouldEqual, int64(0))
			})
		})

		Convey("When it commits its first position with expected zero", func() {
			err := cursors.Commit(ctx, "default", "reporting", 12, 0, now)

			Convey("Then the row is created and reads back at the new position", func() {
				So(err, ShouldBeNil)
				pos, getErr := cursors.Get(ctx, "default", "reporting")
				So(getErr, ShouldBeNil)
				So(pos, ShouldEqual, int64(12))
			})

			Convey("And a second first-commit from a racing replica is rejected", func() {
				// The ON CONFLICT guard only updates while position = 0, so
				// a replica that also believes it is first must lose.
				conflict := cursors.Commit(ctx, "default", "reporting", 99, 0, now)
				So(errors.Is(conflict, feed.ErrCursorConflict), ShouldBeTrue)

				pos, getErr := cursors.Get(ctx, "default", "reporting")
				So(getErr, ShouldBeNil)
				So(pos, ShouldEqual, int64(12))
			})

			Convey("And advancing it with the correct expected position succeeds", func() {
				So(cursors.Commit(ctx, "default", "reporting", 40, 12, now), ShouldBeNil)
				pos, getErr := cursors.Get(ctx, "default", "reporting")
				So(getErr, ShouldBeNil)
				So(pos, ShouldEqual, int64(40))
			})

			Convey("And advancing it with a stale expected position conflicts", func() {
				err := cursors.Commit(ctx, "default", "reporting", 40, 7, now)
				So(errors.Is(err, feed.ErrCursorConflict), ShouldBeTrue)

				pos, getErr := cursors.Get(ctx, "default", "reporting")
				So(getErr, ShouldBeNil)
				So(pos, ShouldEqual, int64(12))
			})
		})
	})

	Convey("Given the same consumer name on two tenants", t, func() {
		pool.MustExec(`TRUNCATE flexitype_event_cursor`)
		So(cursors.Commit(ctx, "default", "reporting", 5, 0, now), ShouldBeNil)
		So(cursors.Commit(ctx, "other", "reporting", 9, 0, now), ShouldBeNil)

		Convey("When each tenant reads its cursor", func() {
			a, errA := cursors.Get(ctx, "default", "reporting")
			b, errB := cursors.Get(ctx, "other", "reporting")

			Convey("Then the cursors are independent per tenant", func() {
				So(errA, ShouldBeNil)
				So(errB, ShouldBeNil)
				So(a, ShouldEqual, int64(5))
				So(b, ShouldEqual, int64(9))
			})
		})
	})

	Convey("Given a closed pool", t, func() {
		closed := openIntegrationDB(t)
		So(closed.Close(), ShouldBeNil)
		broken := postgres.NewCursorStore(closed)

		Convey("When the cursor is read and committed", func() {
			_, getErr := broken.Get(ctx, "default", "reporting")
			insertErr := broken.Commit(ctx, "default", "reporting", 1, 0, now)
			updateErr := broken.Commit(ctx, "default", "reporting", 2, 1, now)

			Convey("Then each wraps the driver failure with its operation", func() {
				So(getErr, ShouldNotBeNil)
				So(getErr.Error(), ShouldContainSubstring, "get cursor")
				So(insertErr, ShouldNotBeNil)
				So(insertErr.Error(), ShouldContainSubstring, "commit cursor")
				So(updateErr, ShouldNotBeNil)
				So(updateErr.Error(), ShouldContainSubstring, "commit cursor")
			})
		})
	})
}

// --- delivery depth metrics -------------------------------------------------

func TestDeliveryStatsIntegration(t *testing.T) {
	pool, transactor := controlPlaneFixture(t)
	ctx := context.Background()
	outboxStore := postgres.NewOutboxStore(transactor)
	stats := postgres.NewDeliveryStats(pool)

	Convey("Given pending outbox envelopes and deliveries in mixed states", t, func() {
		pool.MustExec(`TRUNCATE flexitype_event_outbox, flexitype_webhook_delivery,
			flexitype_webhook_subscription CASCADE`)
		ids := writeEnvelopesFor(ctx, transactor, outboxStore, 4, "default", "flexitype.entity.created")
		// Two envelopes are already dispatched, so only two stay pending.
		pool.MustExec(`UPDATE flexitype_event_outbox SET dispatched_at = now() WHERE id = ANY($1)`,
			pq.Array(ids[:2]))
		sub := insertSubscription(pool, "default", "counter")
		var seq int64
		for _, spec := range []struct {
			status string
			n      int
		}{{"pending", 3}, {"dead", 1}, {"delivered", 2}} {
			for i := 0; i < spec.n; i++ {
				seq++
				insertDelivery(pool, sub, ids[i%len(ids)], "default", spec.status, seq)
			}
		}

		Convey("When the depth snapshot is taken", func() {
			depth, err := stats.Snapshot(ctx)

			Convey("Then undispatched envelopes and deliveries are counted per status", func() {
				So(err, ShouldBeNil)
				So(depth.OutboxPending, ShouldEqual, int64(2))
				So(depth.DeliveriesByStatus["pending"], ShouldEqual, int64(3))
				So(depth.DeliveriesByStatus["dead"], ShouldEqual, int64(1))
				So(depth.DeliveriesByStatus["delivered"], ShouldEqual, int64(2))
				So(depth.DeliveriesByStatus, ShouldNotContainKey, "inflight")
			})
		})
	})

	Convey("Given an empty outbox", t, func() {
		pool.MustExec(`TRUNCATE flexitype_event_outbox, flexitype_webhook_delivery`)

		Convey("When the depth snapshot is taken", func() {
			depth, err := stats.Snapshot(ctx)

			Convey("Then it reports zero depth with an initialized status map", func() {
				So(err, ShouldBeNil)
				So(depth.OutboxPending, ShouldEqual, int64(0))
				So(depth.DeliveriesByStatus, ShouldNotBeNil)
				So(depth.DeliveriesByStatus, ShouldBeEmpty)
			})
		})
	})

	Convey("Given a closed pool", t, func() {
		closed := openIntegrationDB(t)
		So(closed.Close(), ShouldBeNil)
		broken := postgres.NewDeliveryStats(closed)

		Convey("When the snapshot is taken", func() {
			_, err := broken.Snapshot(ctx)

			Convey("Then the outbox count failure is wrapped and surfaced first", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "count pending outbox")
			})
		})
	})
}
