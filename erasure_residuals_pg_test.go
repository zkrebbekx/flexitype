package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestErasureRedactsResidualsPostgres proves the redaction SQL against a real
// database, including the event log — which the in-memory backend does not
// have, and which is the copy that outlives everything else: the feed serves
// those payloads until retention prunes them, and forever for rows that were
// never expanded, because pruning requires a feed_seq.
func TestErasureRedactsResidualsPostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool, flexitype.WithOutbox())
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a purged entity whose writes reached the outbox", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		patient, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "patient", DisplayName: "Patient",
		})
		So(err, ShouldBeNil)
		name, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: patient.ID.String(), InternalName: "full_name",
			DisplayName: "Full name", DataType: "string",
		})
		So(err, ShouldBeNil)

		write := func(entity, v string) {
			raw, _ := json.Marshal(v)
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: name.ID.String(), EntityID: entity,
				TypeDefinitionID: patient.ID.String(), Value: raw,
			})
			So(serr, ShouldBeNil)
		}
		write("p1", "Ada Lovelace")
		write("p2", "Grace Hopper")

		count := func(table, column, needle string) int {
			var n int
			So(pool.Get(&n, `SELECT COUNT(*) FROM `+table+` WHERE `+column+`::text LIKE '%'||$1||'%'`, needle), ShouldBeNil)
			return n
		}
		So(count("flexitype_event_outbox", "payload", "Ada Lovelace"), ShouldBeGreaterThan, 0)

		Convey("When the entity is purged", func() {
			report, perr := svc.Interactors(ctx).Erasure().PurgeEntity(ctx, patient.ID.String(), "p1")
			So(perr, ShouldBeNil)
			So(report.RecordsRedacted, ShouldBeGreaterThan, 0)

			Convey("Then the value is gone from the event log", func() {
				So(count("flexitype_event_outbox", "payload", "Ada Lovelace"), ShouldEqual, 0)
			})

			Convey("Then the value is gone from the activity log", func() {
				So(count("flexitype_activity_log", "before_state", "Ada Lovelace"), ShouldEqual, 0)
				So(count("flexitype_activity_log", "after_state", "Ada Lovelace"), ShouldEqual, 0)
			})

			Convey("Then another entity's value is untouched", func() {
				So(count("flexitype_event_outbox", "payload", "Grace Hopper"), ShouldBeGreaterThan, 0)
			})

			Convey("Then the event rows survive, so the feed stays gapless", func() {
				var rows int
				So(pool.Get(&rows, `SELECT COUNT(*) FROM flexitype_event_outbox`), ShouldBeNil)
				So(rows, ShouldBeGreaterThan, 0)

				var erased int
				So(pool.Get(&erased,
					`SELECT COUNT(*) FROM flexitype_event_outbox WHERE payload->>'erased' = 'true'`), ShouldBeNil)
				So(erased, ShouldBeGreaterThan, 0)
			})
		})

		Convey("When the whole tenant is purged", func() {
			report, perr := svc.Interactors(ctx).Erasure().PurgeTenant(ctx)
			So(perr, ShouldBeNil)
			So(report.RecordsRedacted, ShouldBeGreaterThan, 0)

			Convey("Then no entity value remains in either log", func() {
				So(count("flexitype_event_outbox", "payload", "Ada Lovelace"), ShouldEqual, 0)
				So(count("flexitype_event_outbox", "payload", "Grace Hopper"), ShouldEqual, 0)
				So(count("flexitype_activity_log", "after_state", "Grace Hopper"), ShouldEqual, 0)
			})

			Convey("Then the schema history survives: a tenant purge erases data, not definitions", func() {
				So(count("flexitype_activity_log", "after_state", "full_name"), ShouldBeGreaterThan, 0)
			})
		})
	})
}

// TestChangeSetResidualErasurePostgres covers the change-set copy against the
// real JSONB rewrite: a draft or rejected set is never pruned, so a purged
// value stayed readable there indefinitely.
func TestChangeSetResidualErasurePostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given two entities' values staged in one draft change-set", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := svc.Interactors(ctx)

		person, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "person", DisplayName: "Person"})
		So(err, ShouldBeNil)
		email, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: person.ID.String(), InternalName: "email",
			DisplayName: "Email", DataType: "string",
		})
		So(err, ShouldBeNil)
		for _, e := range []string{"p1", "p2"} {
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: email.ID.String(), EntityID: e,
				TypeDefinitionID: person.ID.String(),
				Value:            json.RawMessage(`"` + e + `@example.com"`),
			})
			So(serr, ShouldBeNil)
		}

		cs, err := svc.Interactors(ctx).ChangeSets().Create(ctx,
			appchangeset.CreateInput{Name: "draft"})
		So(err, ShouldBeNil)
		for _, e := range []string{"p1", "p2"} {
			_, aerr := svc.Interactors(ctx).ChangeSets().AddMutation(ctx, cs.ID.String(), appvalue.Mutation{
				Kind: appvalue.MutationSet, AttributeDefinitionID: email.ID.String(),
				EntityID: e, TypeDefinitionID: person.ID.String(),
				Value: json.RawMessage(`"` + e + `@example.com"`),
			})
			So(aerr, ShouldBeNil)
		}

		Convey("When one entity is erased", func() {
			_, err := svc.Interactors(ctx).Erasure().PurgeEntity(ctx, person.ID.String(), "p1")
			So(err, ShouldBeNil)
			got, gerr := svc.Interactors(ctx).ChangeSets().Get(ctx, cs.ID.String())
			So(gerr, ShouldBeNil)

			Convey("Then only that entity's staged value is redacted", func() {
				So(got.Mutations, ShouldHaveLength, 2)
				So(got.Mutations[0].EntityID, ShouldEqual, "p1")
				So(got.Mutations[0].Value, ShouldBeNil)
				So(got.Mutations[0].Erased, ShouldBeTrue)
				So(string(got.Mutations[1].Value), ShouldEqual, `"p2@example.com"`)
			})

			Convey("Then the mutation order is preserved", func() {
				So(got.Mutations[1].EntityID, ShouldEqual, "p2")
			})

			Convey("Then the raw JSONB no longer contains the erased address", func() {
				var raw string
				So(pool.Get(&raw, `SELECT mutations::text FROM flexitype_changeset`), ShouldBeNil)
				So(raw, ShouldNotContainSubstring, "p1@example.com")
				So(raw, ShouldContainSubstring, "p2@example.com")
			})
		})

		Convey("When the whole tenant is erased", func() {
			_, err := svc.Interactors(ctx).Erasure().PurgeTenant(ctx)
			So(err, ShouldBeNil)

			Convey("Then every staged value is redacted and no address survives", func() {
				var raw string
				So(pool.Get(&raw, `SELECT mutations::text FROM flexitype_changeset`), ShouldBeNil)
				So(raw, ShouldNotContainSubstring, "@example.com")
				So(raw, ShouldContainSubstring, "erased")
			})
		})
	})
}
