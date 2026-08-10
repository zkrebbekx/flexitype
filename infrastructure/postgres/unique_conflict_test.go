package postgres

import (
	"context"
	"testing"
	"time"

	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/admin"
	"github.com/zkrebbekx/flexitype/application/webhook"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/internal/testdb"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestUniqueViolationsAreConflicts covers issue #615.
//
// Only the saved-view store translated SQLSTATE 23505. Every other insert
// behind a UNIQUE index wrapped it, so a caller that lost a race at the index
// got an opaque error — HTTP 500 — where the in-memory twin answers 409. The
// application layer checks first, so this is reachable only when two callers
// get past the same check concurrently. That makes it rare, not impossible,
// and the client cannot tell "your request conflicts" from "the server broke".
//
// These drive the STORES directly, which is the only deterministic way to
// reach the index: through an interactor the pre-check wins the race and the
// insert never runs. Each case writes the same logical row twice under two
// different IDs, which is what the losing side of a race sends.
func TestUniqueViolationsAreConflicts(t *testing.T) {
	pool := testdb.Open(t, "postgres_unique_conflict")
	ctx := context.Background()
	if err := Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	tenant := valueobjects.DefaultTenant

	newType := func(name string) *domaintypedef.TypeDefinition {
		td, _, err := domaintypedef.New(domaintypedef.NewInput{
			TenantID: tenant, InternalName: name, DisplayName: name,
		}, now)
		if err != nil {
			t.Fatalf("new type %s: %v", name, err)
		}
		return td
	}

	cases := []struct {
		name string
		// insert writes the row. It is called twice; the second call must be
		// refused by the index rather than by anything in Go.
		insert func() error
		// leak is the schema detail that must not reach the caller.
		leak string
	}{
		{
			name:   "type definition name",
			insert: func() error { return NewTypeDefinitionRepository(pool).Save(ctx, newType("same_type")) },
			leak:   "uq_flexitype_type_definition_name",
		},
		{
			name: "tenant name",
			insert: func() error {
				return NewAdminStore(pool).CreateTenant(ctx, admin.Tenant{
					ID: ulid.New(), Name: "same-tenant", Active: true,
					CreatedAt: now, UpdatedAt: now,
				})
			},
			leak: "flexitype_tenant_name_key",
		},
		{
			name: "service account name",
			insert: func() error {
				return NewAdminStore(pool).CreateAccount(ctx, admin.ServiceAccount{
					ID: ulid.New(), TenantID: tenant.String(), Name: "same-account",
					Scopes: []serviceaccount.Scope{serviceaccount.ScopeRead}, Active: true,
					CreatedAt: now, UpdatedAt: now,
				}, "hash")
			},
			leak: "flexitype_service_account_tenant_id_name_key",
		},
		{
			name: "webhook subscription name",
			insert: func() error {
				return NewSubscriptionStore(pool).Create(ctx, webhook.Subscription{
					ID: ulid.New(), TenantID: tenant, Name: "same-subscription",
					URL: "https://example.test/hook", Secret: "s", Active: true,
					CreatedAt: now, UpdatedAt: now,
				})
			},
			leak: "flexitype_webhook_subscription_tenant_id_name_key",
		},
	}

	for _, c := range cases {
		Convey("Given a row that already exists ("+c.name+")", t, func() {
			// goconvey replays this body once per leaf, so the first insert
			// must start from an empty table every time.
			testdb.TruncateAll(t, pool)
			So(c.insert(), ShouldBeNil)

			Convey("When a second writer inserts the same value", func() {
				err := c.insert()

				Convey("Then it is a conflict, not an opaque server error", func() {
					So(err, ShouldNotBeNil)
					So(domainerrors.IsConflict(err), ShouldBeTrue)
				})

				Convey("Then no schema detail reaches the caller", func() {
					// The index name belongs in the server log. A client that
					// read it would be reading the schema, and the SQLSTATE
					// says which backend is underneath.
					So(err.Error(), ShouldNotContainSubstring, c.leak)
					So(err.Error(), ShouldNotContainSubstring, "23505")
				})
			})
		})
	}

	// The unit family is NOT here, and that is a finding of its own: #615
	// listed it, but flexitype_unit_family (migration 000017) declares no
	// unique index on (tenant_id, name) at all. Nothing to translate, and
	// nothing diverges — both backends accept a duplicate name today.

	Convey("Given a type that owns an attribute and a relationship", t, func() {
		testdb.TruncateAll(t, pool)
		owner := newType("widget")
		So(NewTypeDefinitionRepository(pool).Save(ctx, owner), ShouldBeNil)

		saveAttribute := func() error {
			attr, _, err := domainattribute.New(domainattribute.NewInput{
				TenantID: tenant, TypeDefinitionID: owner.ID(),
				InternalName: "sku", DisplayName: "SKU",
				DataType: valueobjects.DataTypeString,
			}, now)
			So(err, ShouldBeNil)
			return NewAttributeDefinitionRepository(pool).Save(ctx, attr)
		}

		Convey("When two writers declare the same attribute name", func() {
			So(saveAttribute(), ShouldBeNil)
			err := saveAttribute()

			Convey("Then the loser gets a conflict", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
				So(err.Error(), ShouldNotContainSubstring, "uq_flexitype_attribute_definition_name")
			})
		})

		Convey("When two writers link the same pair of entities", func() {
			set, _, serr := domaintypedef.NewAttributeSet(tenant, "related_attrs", "Related attributes", now)
			So(serr, ShouldBeNil)
			So(NewTypeDefinitionRepository(pool).Save(ctx, set), ShouldBeNil)

			def, _, derr := domainrelationship.NewDefinition(domainrelationship.NewDefinitionInput{
				TenantID: tenant, InternalName: "related", DisplayName: "Related",
				ParentType: owner, ChildType: owner, AttributeSet: set,
			}, now)
			So(derr, ShouldBeNil)
			So(NewRelationshipDefinitionRepository(pool).Save(ctx, def), ShouldBeNil)

			link := func() error {
				rel, _, lerr := domainrelationship.Link(domainrelationship.LinkInput{
					Definition:   def,
					ParentEntity: valueobjects.EntityID("a"),
					ChildEntity:  valueobjects.EntityID("b"),
				}, now)
				So(lerr, ShouldBeNil)
				return NewRelationshipRepository(pool).Save(ctx, rel)
			}
			So(link(), ShouldBeNil)
			err := link()

			Convey("Then the loser gets a conflict", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
				So(err.Error(), ShouldNotContainSubstring, "uq_flexitype_relationship_pair")
			})
		})

		Convey("When a save that is NOT a duplicate fails", func() {
			// The translation must not swallow a real fault: only 23505
			// becomes a conflict.
			orphan, _, aerr := domainattribute.New(domainattribute.NewInput{
				TenantID: tenant, TypeDefinitionID: valueobjects.NewTypeDefinitionID(),
				InternalName: "loose", DisplayName: "Loose",
				DataType: valueobjects.DataTypeString,
			}, now)
			So(aerr, ShouldBeNil)
			err := NewAttributeDefinitionRepository(pool).Save(ctx, orphan)

			Convey("Then it is still reported as a fault, not a conflict", func() {
				// A foreign-key violation is 23503, not 23505.
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err), ShouldBeFalse)
			})
		})
	})
}
