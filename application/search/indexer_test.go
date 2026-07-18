package search_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/appctx"
	"github.com/zkrebbekx/flexitype/application/search"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// recordingStore is a search.DocumentStore that keeps the projection in a map
// so tests can assert the INDEX CONTENTS after a rebuild, not merely that no
// error came back.
type recordingStore struct {
	docs    map[string]search.EntityDocument
	removed []string
	upserts int
	failOn  map[string]error
}

func newRecordingStore() *recordingStore {
	return &recordingStore{docs: map[string]search.EntityDocument{}, failOn: map[string]error{}}
}

func (s *recordingStore) key(tenant valueobjects.TenantID, entityID valueobjects.EntityID) string {
	return tenant.String() + "/" + entityID.String()
}

func (s *recordingStore) Upsert(_ context.Context, doc search.EntityDocument) error {
	if err := s.failOn[doc.EntityID.String()]; err != nil {
		return err
	}
	s.upserts++
	s.docs[s.key(doc.TenantID, doc.EntityID)] = doc
	return nil
}

func (s *recordingStore) Remove(_ context.Context, tenant valueobjects.TenantID, entityID valueobjects.EntityID) error {
	s.removed = append(s.removed, entityID.String())
	delete(s.docs, s.key(tenant, entityID))
	return nil
}

func (s *recordingStore) PurgeTenant(_ context.Context, tenant valueobjects.TenantID) (int, error) {
	n := 0
	for k, doc := range s.docs {
		if doc.TenantID == tenant {
			delete(s.docs, k)
			n++
		}
	}
	return n, nil
}

// fixture seeds an in-memory backend with one type and its attributes so the
// indexer has real values to flatten.
type fixture struct {
	store    *memory.Store
	repos    func() appctx.Repositories
	docs     *recordingStore
	indexer  *search.Indexer
	tenant   valueobjects.TenantID
	typeID   valueobjects.TypeDefinitionID
	attrs    map[string]valueobjects.AttributeDefinitionID
	attrDefs map[string]*domainattribute.Definition
	now      time.Time
}

func newFixture(tenant valueobjects.TenantID) *fixture {
	store := memory.NewStore()
	repos := func() appctx.Repositories { return store.Repositories() }
	docs := newRecordingStore()
	return &fixture{
		store:    store,
		repos:    repos,
		docs:     docs,
		indexer:  search.NewIndexer(repos, docs),
		tenant:   tenant,
		attrs:    map[string]valueobjects.AttributeDefinitionID{},
		attrDefs: map[string]*domainattribute.Definition{},
		now:      time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC),
	}
}

// addType creates and persists the fixture's entity type.
func (f *fixture) addType(name string) {
	td, _, err := domaintypedef.New(domaintypedef.NewInput{
		TenantID:     f.tenant,
		InternalName: name,
		DisplayName:  name,
	}, f.now)
	So(err, ShouldBeNil)
	So(f.repos().TypeDefinitions.Save(context.Background(), td), ShouldBeNil)
	f.typeID = td.ID()
}

// addAttr creates and persists one attribute definition on the fixture type.
func (f *fixture) addAttr(name string, dt valueobjects.DataType) {
	def, _, err := domainattribute.New(domainattribute.NewInput{
		TenantID:         f.tenant,
		TypeDefinitionID: f.typeID,
		InternalName:     name,
		DisplayName:      name,
		DataType:         dt,
		MultiValued:      true,
	}, f.now)
	So(err, ShouldBeNil)
	So(f.repos().Attributes.Save(context.Background(), def), ShouldBeNil)
	f.attrs[name] = def.ID()
	f.attrDefs[name] = def
}

// setValue stores one live value for an entity.
func (f *fixture) setValue(entityID, attrName string, v valueobjects.Value) {
	av, _, err := domainvalue.New(f.attrDefs[attrName], f.typeID,
		valueobjects.EntityID(entityID), valueobjects.Scope{}, v, f.now)
	So(err, ShouldBeNil)
	So(f.repos().Values.Save(context.Background(), av), ShouldBeNil)
}

// envelopeFor builds a value-event envelope carrying the payload slice the
// indexer decodes (tenant/type/entity).
func envelopeFor(eventType events.Type, tenant, typeID, entityID string) events.Envelope {
	payload, err := json.Marshal(map[string]string{
		"tenant_id":          tenant,
		"type_definition_id": typeID,
		"entity_id":          entityID,
	})
	So(err, ShouldBeNil)
	return events.Envelope{
		ID:            "env-" + entityID,
		Type:          eventType,
		AggregateType: "attribute_value",
		AggregateID:   entityID,
		TenantID:      tenant,
		Payload:       payload,
	}
}

func TestIndexerHandlerIdentity(t *testing.T) {
	Convey("Given a search indexer", t, func() {
		f := newFixture("acme")

		Convey("When it is inspected as an events.Handler", func() {
			var h events.Handler = f.indexer
			bh, isBatch := any(f.indexer).(events.BatchHandler)

			Convey("Then it names itself and also handles batches", func() {
				So(h.Name(), ShouldEqual, "search-indexer")
				So(isBatch, ShouldBeTrue)
				So(bh, ShouldNotBeNil)
			})
		})

		Convey("When the subscribed event types are read", func() {
			types := search.EventTypes()

			Convey("Then it covers set, updated and removed", func() {
				So(types, ShouldResemble, []events.Type{
					domainvalue.EventSet,
					domainvalue.EventUpdated,
					domainvalue.EventRemoved,
				})
			})
		})
	})
}

func TestIndexerHandle(t *testing.T) {
	Convey("Given an entity with a textual and a numeric value", t, func() {
		f := newFixture("acme")
		f.addType("product")
		f.addAttr("name", valueobjects.DataTypeString)
		f.addAttr("weight", valueobjects.DataTypeInteger)
		f.setValue("sku-1", "name", valueobjects.NewStringValue("Wrench"))
		f.setValue("sku-1", "weight", valueobjects.NewIntegerValue(42))
		ctx := context.Background()

		Convey("When the indexer handles a value-set envelope", func() {
			err := f.indexer.Handle(ctx, envelopeFor(domainvalue.EventSet,
				"acme", f.typeID.String(), "sku-1"))

			Convey("Then the document holds every value keyed by internal name", func() {
				So(err, ShouldBeNil)
				doc, ok := f.docs.docs["acme/sku-1"]
				So(ok, ShouldBeTrue)
				So(doc.TenantID, ShouldEqual, valueobjects.TenantID("acme"))
				So(doc.TypeDefinitionID.String(), ShouldEqual, f.typeID.String())
				So(doc.EntityID, ShouldEqual, valueobjects.EntityID("sku-1"))
				So(doc.Values["name"], ShouldResemble, []string{"Wrench"})
				So(doc.Values["weight"], ShouldResemble, []string{"42"})
			})

			Convey("Then only textual values join the searchable text", func() {
				So(err, ShouldBeNil)
				doc := f.docs.docs["acme/sku-1"]
				So(doc.Text, ShouldContainSubstring, "sku-1")
				So(doc.Text, ShouldContainSubstring, "Wrench")
				So(doc.Text, ShouldNotContainSubstring, "42")
			})

			Convey("Then the document carries an updated timestamp", func() {
				So(err, ShouldBeNil)
				So(f.docs.docs["acme/sku-1"].UpdatedAt.IsZero(), ShouldBeFalse)
			})
		})

		Convey("When the indexer is driven through a dispatcher registration", func() {
			d := events.NewDispatcher()
			d.Register(f.indexer, events.WithEventTypes(search.EventTypes()...))
			err := d.DispatchEnvelopes(ctx, envelopeFor(domainvalue.EventSet,
				"acme", f.typeID.String(), "sku-1"))

			Convey("Then the registered handler indexed the entity", func() {
				So(err, ShouldBeNil)
				So(f.docs.docs, ShouldContainKey, "acme/sku-1")
			})
		})

		Convey("When an unrelated event type is dispatched", func() {
			d := events.NewDispatcher()
			d.Register(f.indexer, events.WithEventTypes(search.EventTypes()...))
			other := envelopeFor("flexitype.type_definition.created", "acme", f.typeID.String(), "sku-1")
			err := d.DispatchEnvelopes(ctx, other)

			Convey("Then the indexer is not invoked", func() {
				So(err, ShouldBeNil)
				So(f.docs.docs, ShouldBeEmpty)
			})
		})

		Convey("When the envelope payload is not JSON", func() {
			env := envelopeFor(domainvalue.EventSet, "acme", f.typeID.String(), "sku-1")
			env.Payload = []byte("{not json")
			err := f.indexer.Handle(ctx, env)

			Convey("Then decoding fails and nothing is indexed", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "decode value event payload")
				So(f.docs.docs, ShouldBeEmpty)
			})
		})

		Convey("When the envelope carries an invalid tenant", func() {
			err := f.indexer.Handle(ctx, envelopeFor(domainvalue.EventSet,
				"NOT A TENANT", f.typeID.String(), "sku-1"))

			Convey("Then the tenant parse error surfaces", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "invalid tenant ID")
				So(f.docs.docs, ShouldBeEmpty)
			})
		})

		Convey("When the envelope carries an invalid type definition id", func() {
			err := f.indexer.Handle(ctx, envelopeFor(domainvalue.EventSet,
				"acme", "not-a-ulid", "sku-1"))

			Convey("Then the type parse error surfaces", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "invalid type definition ID")
			})
		})

		Convey("When the envelope carries an empty entity id", func() {
			err := f.indexer.Handle(ctx, envelopeFor(domainvalue.EventSet,
				"acme", f.typeID.String(), ""))

			Convey("Then the entity parse error surfaces", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "entity ID must not be empty")
			})
		})

		Convey("When an entity has no live values left", func() {
			err := f.indexer.Handle(ctx, envelopeFor(domainvalue.EventRemoved,
				"acme", f.typeID.String(), "sku-gone"))

			Convey("Then its document is removed rather than upserted", func() {
				So(err, ShouldBeNil)
				So(f.docs.removed, ShouldResemble, []string{"sku-gone"})
				So(f.docs.upserts, ShouldEqual, 0)
			})
		})

		Convey("When the document store rejects the upsert", func() {
			f.docs.failOn["sku-1"] = errors.New("index unavailable")
			err := f.indexer.Handle(ctx, envelopeFor(domainvalue.EventSet,
				"acme", f.typeID.String(), "sku-1"))

			Convey("Then the store error propagates to the caller", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldEqual, "index unavailable")
			})
		})
	})
}

func TestIndexerRebuildIsIdempotent(t *testing.T) {
	Convey("Given an entity holding multiple values of one multi-valued attribute", t, func() {
		f := newFixture("acme")
		f.addType("product")
		f.addAttr("tag", valueobjects.DataTypeString)
		f.setValue("sku-1", "tag", valueobjects.NewStringValue("alpha"))
		f.setValue("sku-1", "tag", valueobjects.NewStringValue("beta"))
		ctx := context.Background()

		Convey("When the same entity is rebuilt twice", func() {
			So(f.indexer.Rebuild(ctx, "acme", f.typeID, "sku-1"), ShouldBeNil)
			first := f.docs.docs["acme/sku-1"]
			So(f.indexer.Rebuild(ctx, "acme", f.typeID, "sku-1"), ShouldBeNil)
			second := f.docs.docs["acme/sku-1"]

			Convey("Then both values are collected under one key and the doc is stable", func() {
				So(len(first.Values["tag"]), ShouldEqual, 2)
				sorted := append([]string(nil), first.Values["tag"]...)
				sort.Strings(sorted)
				So(sorted, ShouldResemble, []string{"alpha", "beta"})
				So(second.Values["tag"], ShouldResemble, first.Values["tag"])
				So(second.Text, ShouldEqual, first.Text)
			})
		})
	})
}

func TestIndexerHandleBatch(t *testing.T) {
	Convey("Given three entities touched by one commit", t, func() {
		f := newFixture("acme")
		f.addType("product")
		f.addAttr("name", valueobjects.DataTypeString)
		f.setValue("sku-1", "name", valueobjects.NewStringValue("Wrench"))
		f.setValue("sku-2", "name", valueobjects.NewStringValue("Hammer"))
		ctx := context.Background()

		Convey("When a batch repeats the same entity several times", func() {
			envs := []events.Envelope{
				envelopeFor(domainvalue.EventSet, "acme", f.typeID.String(), "sku-1"),
				envelopeFor(domainvalue.EventSet, "acme", f.typeID.String(), "sku-1"),
				envelopeFor(domainvalue.EventUpdated, "acme", f.typeID.String(), "sku-1"),
				envelopeFor(domainvalue.EventSet, "acme", f.typeID.String(), "sku-2"),
			}
			err := f.indexer.HandleBatch(ctx, envs)

			Convey("Then each distinct entity is rebuilt exactly once", func() {
				So(err, ShouldBeNil)
				So(f.docs.upserts, ShouldEqual, 2)
				So(f.docs.docs["acme/sku-1"].Values["name"], ShouldResemble, []string{"Wrench"})
				So(f.docs.docs["acme/sku-2"].Values["name"], ShouldResemble, []string{"Hammer"})
			})
		})

		Convey("When one envelope in the batch is undecodable", func() {
			bad := envelopeFor(domainvalue.EventSet, "acme", f.typeID.String(), "sku-1")
			bad.Payload = []byte("}{")
			err := f.indexer.HandleBatch(ctx, []events.Envelope{
				bad,
				envelopeFor(domainvalue.EventSet, "acme", f.typeID.String(), "sku-2"),
			})

			Convey("Then the good envelope still indexes and the error is reported", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "decode value event payload")
				So(f.docs.docs, ShouldContainKey, "acme/sku-2")
				So(f.docs.docs, ShouldNotContainKey, "acme/sku-1")
			})
		})

		Convey("When the batch mixes invalid tenant, type and entity ids", func() {
			err := f.indexer.HandleBatch(ctx, []events.Envelope{
				envelopeFor(domainvalue.EventSet, "BAD TENANT", f.typeID.String(), "sku-1"),
				envelopeFor(domainvalue.EventSet, "acme", "nope", "sku-1"),
				envelopeFor(domainvalue.EventSet, "acme", f.typeID.String(), ""),
				envelopeFor(domainvalue.EventSet, "acme", f.typeID.String(), "sku-2"),
			})

			Convey("Then every parse failure is joined and the valid one still indexes", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "invalid tenant ID")
				So(err.Error(), ShouldContainSubstring, "invalid type definition ID")
				So(err.Error(), ShouldContainSubstring, "entity ID must not be empty")
				So(f.docs.docs, ShouldContainKey, "acme/sku-2")
			})
		})

		Convey("When the store fails for one entity of the batch", func() {
			boom := errors.New("index unavailable")
			f.docs.failOn["sku-1"] = boom
			err := f.indexer.HandleBatch(ctx, []events.Envelope{
				envelopeFor(domainvalue.EventSet, "acme", f.typeID.String(), "sku-1"),
				envelopeFor(domainvalue.EventSet, "acme", f.typeID.String(), "sku-2"),
			})

			Convey("Then the failure is joined but the other entity is still indexed", func() {
				So(errors.Is(err, boom), ShouldBeTrue)
				So(f.docs.docs, ShouldContainKey, "acme/sku-2")
				So(f.docs.docs, ShouldNotContainKey, "acme/sku-1")
			})
		})

		Convey("When the batch is empty", func() {
			err := f.indexer.HandleBatch(ctx, nil)

			Convey("Then nothing happens and no error is returned", func() {
				So(err, ShouldBeNil)
				So(f.docs.upserts, ShouldEqual, 0)
			})
		})
	})
}

// The decorators below wrap the in-memory repositories to inject a failure on
// exactly one read, so the indexer's repository error paths are exercised
// without hand-rolling whole repository implementations.

type failingValueReader struct {
	appctx.ValueReader
	listByEntityErr error
	listEntitiesErr error
}

func (r failingValueReader) ListByEntity(ctx context.Context, key domainvalue.EntityKey) ([]*domainvalue.AttributeValue, error) {
	if r.listByEntityErr != nil {
		return nil, r.listByEntityErr
	}
	return r.ValueReader.ListByEntity(ctx, key)
}

func (r failingValueReader) ListEntities(ctx context.Context, tenant valueobjects.TenantID, typeDefIDs []valueobjects.TypeDefinitionID, page db.Page) ([]domainvalue.EntitySummary, int, error) {
	if r.listEntitiesErr != nil {
		return nil, 0, r.listEntitiesErr
	}
	return r.ValueReader.ListEntities(ctx, tenant, typeDefIDs, page)
}

type failingAttributes struct {
	domainattribute.Repository
	err error
}

func (r failingAttributes) GetMany(ctx context.Context, ids []valueobjects.AttributeDefinitionID) ([]*domainattribute.Definition, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.Repository.GetMany(ctx, ids)
}

type failingTypeDefs struct {
	domaintypedef.Repository
	err error
}

func (r failingTypeDefs) List(ctx context.Context, filter domaintypedef.Filter, page db.Page) ([]*domaintypedef.TypeDefinition, int, error) {
	if r.err != nil {
		return nil, 0, r.err
	}
	return r.Repository.List(ctx, filter, page)
}

func TestIndexerRepositoryFailures(t *testing.T) {
	Convey("Given an indexed entity whose repositories can be made to fail", t, func() {
		f := newFixture("acme")
		f.addType("product")
		f.addAttr("name", valueobjects.DataTypeString)
		f.setValue("sku-1", "name", valueobjects.NewStringValue("Wrench"))
		ctx := context.Background()
		boom := errors.New("database unavailable")

		Convey("When loading the entity's values fails", func() {
			repos := func() appctx.Repositories {
				r := f.store.Repositories()
				r.ValueReader = failingValueReader{ValueReader: r.ValueReader, listByEntityErr: boom}
				return r
			}
			idx := search.NewIndexer(repos, f.docs)
			err := idx.Rebuild(ctx, "acme", f.typeID, "sku-1")

			Convey("Then the error is wrapped and nothing is indexed", func() {
				So(errors.Is(err, boom), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "load entity values")
				So(f.docs.docs, ShouldBeEmpty)
			})
		})

		Convey("When loading the attribute definitions fails", func() {
			repos := func() appctx.Repositories {
				r := f.store.Repositories()
				r.Attributes = failingAttributes{Repository: r.Attributes, err: boom}
				return r
			}
			idx := search.NewIndexer(repos, f.docs)
			err := idx.Rebuild(ctx, "acme", f.typeID, "sku-1")

			Convey("Then the error is wrapped and nothing is indexed", func() {
				So(errors.Is(err, boom), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "load attribute definitions")
				So(f.docs.docs, ShouldBeEmpty)
			})
		})

		Convey("When listing the tenant's types fails during reindex", func() {
			repos := func() appctx.Repositories {
				r := f.store.Repositories()
				r.TypeDefinitions = failingTypeDefs{Repository: r.TypeDefinitions, err: boom}
				return r
			}
			idx := search.NewIndexer(repos, f.docs)
			count, err := idx.Reindex(ctx, "acme")

			Convey("Then reindex aborts with a zero count", func() {
				So(errors.Is(err, boom), ShouldBeTrue)
				So(count, ShouldEqual, 0)
			})
		})

		Convey("When paging the entities fails during reindex", func() {
			repos := func() appctx.Repositories {
				r := f.store.Repositories()
				r.ValueReader = failingValueReader{ValueReader: r.ValueReader, listEntitiesErr: boom}
				return r
			}
			idx := search.NewIndexer(repos, f.docs)
			count, err := idx.Reindex(ctx, "acme")

			Convey("Then reindex aborts with the count reached so far", func() {
				So(errors.Is(err, boom), ShouldBeTrue)
				So(count, ShouldEqual, 0)
			})
		})
	})
}

func TestIndexerReindex(t *testing.T) {
	Convey("Given a tenant with entities across two types", t, func() {
		f := newFixture("acme")
		f.addType("product")
		f.addAttr("name", valueobjects.DataTypeString)
		f.setValue("sku-1", "name", valueobjects.NewStringValue("Wrench"))
		f.setValue("sku-2", "name", valueobjects.NewStringValue("Hammer"))
		f.setValue("sku-3", "name", valueobjects.NewStringValue("Anvil"))
		ctx := context.Background()

		Convey("When the whole tenant is reindexed", func() {
			count, err := f.indexer.Reindex(ctx, "acme")

			Convey("Then every entity is rebuilt and counted", func() {
				So(err, ShouldBeNil)
				So(count, ShouldEqual, 3)
				So(f.docs.docs, ShouldHaveLength, 3)
				So(f.docs.docs["acme/sku-1"].Values["name"], ShouldResemble, []string{"Wrench"})
				So(f.docs.docs["acme/sku-2"].Values["name"], ShouldResemble, []string{"Hammer"})
				So(f.docs.docs["acme/sku-3"].Values["name"], ShouldResemble, []string{"Anvil"})
			})
		})

		Convey("When another tenant is reindexed", func() {
			count, err := f.indexer.Reindex(ctx, "other")

			Convey("Then nothing of this tenant is touched", func() {
				So(err, ShouldBeNil)
				So(count, ShouldEqual, 0)
				So(f.docs.docs, ShouldBeEmpty)
			})
		})

		Convey("When the document store fails partway through the walk", func() {
			boom := errors.New("index unavailable")
			f.docs.failOn["sku-1"] = boom
			f.docs.failOn["sku-2"] = boom
			f.docs.failOn["sku-3"] = boom
			count, err := f.indexer.Reindex(ctx, "acme")

			Convey("Then the walk aborts with the wrapped rebuild error", func() {
				So(errors.Is(err, boom), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "rebuild ")
				So(count, ShouldEqual, 0)
			})
		})

		Convey("When the tenant holds more entities than one reindex page", func() {
			g := newFixture("bulk-co")
			g.addType("product")
			g.addAttr("name", valueobjects.DataTypeString)
			// The reindex page limit is 200; 201 entities forces a second page
			// driven by the keyset cursor of the previous page's last row.
			const total = 201
			for n := 0; n < total; n++ {
				g.setValue(fmt.Sprintf("sku-%04d", n), "name",
					valueobjects.NewStringValue(fmt.Sprintf("item %d", n)))
			}
			count, err := g.indexer.Reindex(ctx, "bulk-co")

			Convey("Then every entity across both pages is indexed exactly once", func() {
				So(err, ShouldBeNil)
				So(count, ShouldEqual, total)
				So(g.docs.docs, ShouldHaveLength, total)
				So(g.docs.upserts, ShouldEqual, total)
				So(g.docs.docs["bulk-co/sku-0000"].Values["name"], ShouldResemble, []string{"item 0"})
				So(g.docs.docs["bulk-co/sku-0200"].Values["name"], ShouldResemble, []string{"item 200"})
			})
		})

		Convey("When a tenant has a type but no entities", func() {
			g := newFixture("empty-co")
			g.addType("widget")
			count, err := g.indexer.Reindex(ctx, "empty-co")

			Convey("Then the walk completes with a zero count", func() {
				So(err, ShouldBeNil)
				So(count, ShouldEqual, 0)
				So(g.docs.docs, ShouldBeEmpty)
			})
		})
	})
}
