package computed

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

func TestMaterializerSubscriberContract(t *testing.T) {
	Convey("Given the computed-attribute materializer as a projection subscriber", t, func() {
		Convey("When it is asked to identify itself", func() {
			Convey("Then it reports a stable subscriber name", func() {
				So(NewMaterializer(nil).Name(), ShouldEqual, "computed-materializer")
			})
		})

		Convey("When its event subscriptions are inspected", func() {
			types := EventTypes()

			Convey("Then it covers both value changes and definition changes", func() {
				So(types, ShouldContain, domainvalue.EventSet)
				So(types, ShouldContain, domainvalue.EventRemoved)
				So(types, ShouldContain, domainattribute.EventCreated)
				So(types, ShouldContain, domaintypedef.EventArchived)
			})
		})

		Convey("When event types are classified", func() {
			Convey("Then only attribute and type-definition events count as schema changes", func() {
				So(isDefinitionEvent(domainattribute.EventCreated), ShouldBeTrue)
				So(isDefinitionEvent(domainattribute.EventRestored), ShouldBeTrue)
				So(isDefinitionEvent(domaintypedef.EventUpdated), ShouldBeTrue)
				So(isDefinitionEvent(domainvalue.EventSet), ShouldBeFalse)
				So(isDefinitionEvent(events.Type("flexitype.something.else")), ShouldBeFalse)
			})
		})
	})
}

func TestMaterializerHasComputedCache(t *testing.T) {
	Convey("Given the per-type has-computed cache", t, func() {
		m := NewMaterializer(nil)

		Convey("When a type has not been seen", func() {
			known, has := m.cachedHasComputed("t1")

			Convey("Then it reports unknown so the caller falls back to the schema", func() {
				So(known, ShouldBeFalse)
				So(has, ShouldBeFalse)
			})
		})

		Convey("When a type's has-computed flag is recorded", func() {
			m.setCachedHasComputed("t1", true)
			m.setCachedHasComputed("t2", false)

			Convey("Then both the positive and negative answers are cached", func() {
				known, has := m.cachedHasComputed("t1")
				So(known, ShouldBeTrue)
				So(has, ShouldBeTrue)

				known, has = m.cachedHasComputed("t2")
				So(known, ShouldBeTrue)
				So(has, ShouldBeFalse) // a cached "no" is what lets a bulk import early-out
			})
		})

		Convey("When a definition event arrives", func() {
			m.setCachedHasComputed("t1", true)
			m.setCachedHasComputed("t2", false)

			err := m.Handle(context.Background(), events.Envelope{Type: domainattribute.EventCreated})

			Convey("Then the whole cache is flushed, since inheritance makes per-type invalidation unsound", func() {
				So(err, ShouldBeNil)

				known, _ := m.cachedHasComputed("t1")
				So(known, ShouldBeFalse)
				known, _ = m.cachedHasComputed("t2")
				So(known, ShouldBeFalse)
			})
		})
	})
}

func TestMaterializerHandleRejectsBadEvents(t *testing.T) {
	Convey("Given a value event the materializer cannot act on", t, func() {
		m := NewMaterializer(nil)

		Convey("When the payload is not decodable JSON", func() {
			err := m.Handle(context.Background(), events.Envelope{
				Type: domainvalue.EventSet, Payload: json.RawMessage(`{not json`),
			})

			Convey("Then the decode failure is reported, naming the payload", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "decode value event payload")
			})
		})

		Convey("When the payload carries a malformed tenant id", func() {
			err := m.Handle(context.Background(), events.Envelope{
				Type:    domainvalue.EventSet,
				Payload: json.RawMessage(`{"tenant_id":"NOT A TENANT!","type_definition_id":"t","entity_id":"e"}`),
			})

			Convey("Then the tenant parse failure surfaces", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "tenant")
			})
		})
	})
}

func TestMaterializerHandleBatchCoalescing(t *testing.T) {
	Convey("Given a batch of events from one commit", t, func() {
		m := NewMaterializer(nil)

		valueEnv := func(tenant, typeID, entityID, attrID string) events.Envelope {
			p, err := json.Marshal(valuePayload{
				TenantID: tenant, TypeDefinitionID: typeID,
				EntityID: entityID, AttributeDefID: attrID,
			})
			So(err, ShouldBeNil)
			return events.Envelope{Type: domainvalue.EventSet, Payload: p}
		}

		Convey("When the batch mixes a definition event with undecodable and malformed payloads", func() {
			m.setCachedHasComputed("t1", true)

			err := m.HandleBatch(context.Background(), []events.Envelope{
				{Type: domaintypedef.EventUpdated},
				{Type: domainvalue.EventSet, Payload: json.RawMessage(`{oops`)},
				valueEnv("BAD TENANT!", "t1", "e1", "a1"),
			})

			Convey("Then the cache is flushed and every failure is joined, not swallowed", func() {
				known, _ := m.cachedHasComputed("t1")
				So(known, ShouldBeFalse)

				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "decode value event payload")
				So(err.Error(), ShouldContainSubstring, "tenant")
			})
		})

		Convey("When several events touch the same entity", func() {
			// Ten value events on one entity must coalesce to a single recompute
			// key. A nil factory means any recompute attempt would panic, so
			// reaching the assertions at all proves the batch collapsed to the
			// one (unparseable-tenant) key that fails before the factory is used.
			envs := []events.Envelope{}
			for i := 0; i < 10; i++ {
				envs = append(envs, valueEnv("BAD TENANT!", "t1", "e1", "a1"))
			}
			err := m.HandleBatch(context.Background(), envs)

			Convey("Then the entity is processed once, yielding a single error", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldNotContainSubstring, "\n") // errors.Join separates with newlines
			})
		})

		Convey("When the batch is empty", func() {
			Convey("Then there is nothing to do and no error", func() {
				So(m.HandleBatch(context.Background(), nil), ShouldBeNil)
			})
		})
	})
}
