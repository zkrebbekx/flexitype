package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appfeed "github.com/zkrebbekx/flexitype/application/feed"
	appoutbox "github.com/zkrebbekx/flexitype/application/outbox"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// runEntityCoordinates is the regression for #550.
//
// An envelope named the AGGREGATE that emitted it, which for a value event is
// the attribute VALUE. A consumer that only wanted "entity E changed, re-read
// it" could not answer that from the envelope: it had to decode the payload
// for type_definition_id and entity_id, which couples every router to the
// payload schema of every event type it routes — the thing an envelope exists
// to prevent.
//
// The marketplace example is exactly that consumer, and it decoded three
// payload types for one fact.
func runEntityCoordinates(t *testing.T, label string, setup func(func(context.Context, events.Envelope) error) *flexitype.Service) {
	t.Helper()

	Convey("Given a subscriber that reads the envelope only ("+label+")", t, func() {
		var seen []events.Envelope
		svc := setup(func(_ context.Context, env events.Envelope) error {
			seen = append(seen, env)
			return nil
		})
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		name, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "name",
			DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)

		// routed is what a payload-blind router can build: the set of
		// entities it must re-read.
		routed := func() []string {
			out := []string{}
			for _, env := range seen {
				if env.EntityID == "" {
					continue
				}
				out = append(out, env.TypeDefinitionID+"/"+env.EntityID)
			}
			return out
		}

		Convey("When a value is written", func() {
			seen = nil
			set, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: name.ID.String(), EntityID: "p-1",
				TypeDefinitionID: product.ID.String(), Value: json.RawMessage(`"Widget"`),
			})
			So(serr, ShouldBeNil)

			Convey("Then the envelope says which entity changed", func() {
				So(routed(), ShouldResemble, []string{product.ID.String() + "/p-1"})
			})

			Convey("And the aggregate id is still the VALUE, which is a different thing", func() {
				So(seen, ShouldNotBeEmpty)
				last := seen[len(seen)-1]
				So(last.AggregateID, ShouldEqual, set.ID.String())
				So(last.AggregateID, ShouldNotEqual, last.EntityID)
			})
		})

		Convey("When a value is updated and then removed", func() {
			written, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: name.ID.String(), EntityID: "p-2",
				TypeDefinitionID: product.ID.String(), Value: json.RawMessage(`"First"`),
			})
			So(serr, ShouldBeNil)
			seen = nil

			_, uerr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: name.ID.String(), EntityID: "p-2",
				TypeDefinitionID: product.ID.String(), Value: json.RawMessage(`"Second"`),
			})
			So(uerr, ShouldBeNil)
			_, rerr := svc.Interactors(ctx).Values().Remove(ctx, written.ID.String())
			So(rerr, ShouldBeNil)

			Convey("Then every one of the three value events carries them", func() {
				coordinates := routed()
				So(coordinates, ShouldHaveLength, 2)
				for _, entry := range coordinates {
					So(entry, ShouldEqual, product.ID.String()+"/p-2")
				}
			})
		})

		Convey("When a TYPE is created", func() {
			seen = nil
			_, cerr := svc.Interactors(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
				InternalName: "brand", DisplayName: "Brand",
			})
			So(cerr, ShouldBeNil)

			Convey("Then the envelope carries no entity: a schema change concerns none", func() {
				So(seen, ShouldNotBeEmpty)
				for _, env := range seen {
					So(env.EntityID, ShouldBeEmpty)
					So(env.TypeDefinitionID, ShouldBeEmpty)
				}
			})
		})

		Convey("When two entities are LINKED", func() {
			def, derr := svc.Interactors(ctx).Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
				InternalName: "related_to", DisplayName: "Related to", Kind: "directed",
				ParentTypeID: product.ID.String(), ChildTypeID: product.ID.String(),
			})
			So(derr, ShouldBeNil)
			seen = nil
			_, lerr := svc.Interactors(ctx).Relationships().Link(ctx, apprelationship.LinkInput{
				DefinitionID: def.ID.String(), ParentEntity: "p-1", ChildEntity: "p-2",
			})
			So(lerr, ShouldBeNil)

			Convey("Then the envelope carries no entity: one pair of fields cannot name two", func() {
				So(seen, ShouldNotBeEmpty)
				for _, env := range seen {
					So(env.EntityID, ShouldBeEmpty)
				}
			})
		})
	})
}

// TestEntityCoordinates runs the scenarios against the in-memory backend,
// where dispatch is direct.
func TestEntityCoordinates(t *testing.T) {
	runEntityCoordinates(t, "memory", func(h func(context.Context, events.Envelope) error) *flexitype.Service {
		return flexitype.NewInMemory(flexitype.WithHandlerFunc("test", h))
	})
}

// TestEntityCoordinatesPostgres re-runs them against Postgres WITHOUT the
// outbox, so dispatch is still direct but every store is the real one.
func TestEntityCoordinatesPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	runEntityCoordinates(t, "postgres", func(h func(context.Context, events.Envelope) error) *flexitype.Service {
		svc := flexitype.New(pool, flexitype.WithHandlerFunc("test", h))
		if err := svc.Migrate(context.Background()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		truncateAll(t, pool)
		return svc
	})
}

// TestEntityCoordinatesSurviveTheOutbox is the case that matters for a real
// consumer: the coordinates must survive being STORED.
//
// A webhook subscriber receives an envelope the relay rebuilt from a database
// row, so a field that is only computed in memory would be lost on the one
// path that has a subscriber outside the process.
func TestEntityCoordinatesSurviveTheOutbox(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	Convey("Given a service with the transactional outbox", t, func() {
		// A short relay interval, because feed_seq is stamped when an
		// envelope is DISPATCHED: an undispatched row is not on the feed.
		svc := flexitype.New(pool, flexitype.WithOutbox(appoutbox.WithInterval(20*time.Millisecond)))
		relayCtx, stopRelay := context.WithCancel(context.Background())
		relayDone := make(chan struct{})
		go func() { defer close(relayDone); svc.RunOutboxRelay(relayCtx) }()
		Reset(func() {
			stopRelay()
			<-relayDone
		})
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		So(svc.Migrate(context.Background()), ShouldBeNil)
		truncateAll(t, pool)

		ia := svc.Interactors(ctx)
		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		name, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "name",
			DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: name.ID.String(), EntityID: "p-9",
			TypeDefinitionID: product.ID.String(), Value: json.RawMessage(`"Widget"`),
		})
		So(err, ShouldBeNil)

		// Wait for the relay to stamp the rows it has.
		drained := func() []appfeed.Event {
			for range 100 {
				out, ferr := svc.Interactors(ctx).Feed().List(ctx, appfeed.ListInput{Limit: 50})
				So(ferr, ShouldBeNil)
				for _, entry := range out.Items {
					if entry.Envelope.EntityID == "p-9" {
						return out.Items
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			return nil
		}

		Convey("When the feed is read back", func() {
			// The feed rebuilds the envelope from the stored row, which is the
			// same reconstruction the webhook worker gets.
			page := drained()

			Convey("Then the value event still names the entity", func() {
				found := false
				for _, entry := range page {
					if entry.Envelope.EntityID == "p-9" {
						found = true
						So(entry.Envelope.TypeDefinitionID, ShouldEqual, product.ID.String())
					}
				}
				So(found, ShouldBeTrue)
			})
		})

		Convey("When a row written before the migration is read", func() {
			So(drained(), ShouldNotBeEmpty)
			// A pre-000038 row has both columns NULL. It must read back as an
			// envelope with empty coordinates rather than failing the scan,
			// so a consumer that falls back to the payload keeps working.
			_, uerr := pool.Exec(
				`UPDATE flexitype_event_outbox SET type_definition_id = NULL, entity_id = NULL`)
			So(uerr, ShouldBeNil)

			out, ferr := svc.Interactors(ctx).Feed().List(ctx, appfeed.ListInput{Limit: 50})

			Convey("Then it reads back with empty coordinates, not an error", func() {
				So(ferr, ShouldBeNil)
				page := out.Items
				So(page, ShouldNotBeEmpty)
				for _, entry := range page {
					So(entry.Envelope.EntityID, ShouldBeEmpty)
				}
			})
		})
	})
}
