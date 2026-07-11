// Package search maintains the entity search projection: one document per
// entity, rebuilt whenever the entity's values change. The indexer is a
// dispatcher subscriber, so any delivery path (direct or outbox) keeps the
// projection fresh; rebuild-on-event makes it idempotent, which is exactly
// what at-least-once delivery needs.
package search

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/application"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// EntityDocument is the flattened search projection of one entity.
type EntityDocument struct {
	TenantID         valueobjects.TenantID
	TypeDefinitionID valueobjects.TypeDefinitionID
	EntityID         valueobjects.EntityID
	// Values maps attribute internal names to the entity's value strings.
	Values map[string][]string
	// Text is the searchable flattening (entity id + every textual value).
	Text      string
	UpdatedAt time.Time
}

// DocumentStore persists the projection.
type DocumentStore interface {
	Upsert(ctx context.Context, doc EntityDocument) error
	Remove(ctx context.Context, tenant valueobjects.TenantID, entityID valueobjects.EntityID) error
}

// Indexer rebuilds entity documents from value events.
type Indexer struct {
	newRepos func() application.Repositories
	store    DocumentStore
	now      func() time.Time
}

// NewIndexer builds the indexer. newRepos supplies fresh repositories per
// rebuild (the indexer runs outside any request scope).
func NewIndexer(newRepos func() application.Repositories, store DocumentStore) *Indexer {
	return &Indexer{newRepos: newRepos, store: store, now: time.Now}
}

// Name implements events.Handler.
func (i *Indexer) Name() string { return "search-indexer" }

// EventTypes lists the events the indexer subscribes to.
func EventTypes() []events.Type {
	return []events.Type{domainvalue.EventSet, domainvalue.EventUpdated, domainvalue.EventRemoved}
}

// valuePayload is the slice of the value-event payloads the indexer needs.
type valuePayload struct {
	TenantID         string `json:"tenant_id"`
	TypeDefinitionID string `json:"type_definition_id"`
	EntityID         string `json:"entity_id"`
}

// Handle implements events.Handler: any value change rebuilds the whole
// entity document.
func (i *Indexer) Handle(ctx context.Context, env events.Envelope) error {
	var p valuePayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return fmt.Errorf("decode value event payload: %w", err)
	}
	tenant, err := valueobjects.ParseTenantID(p.TenantID)
	if err != nil {
		return err
	}
	typeID, err := valueobjects.ParseTypeDefinitionID(p.TypeDefinitionID)
	if err != nil {
		return err
	}
	entityID, err := valueobjects.ParseEntityID(p.EntityID)
	if err != nil {
		return err
	}
	return i.Rebuild(ctx, tenant, typeID, entityID)
}

// Rebuild recomputes one entity's document from its live values, removing
// the document when none remain.
func (i *Indexer) Rebuild(ctx context.Context, tenant valueobjects.TenantID, typeID valueobjects.TypeDefinitionID, entityID valueobjects.EntityID) error {
	repos := i.newRepos()

	values, err := repos.Values.ListByEntity(ctx, domainvalue.EntityKey{
		TenantID:         tenant,
		TypeDefinitionID: typeID,
		EntityID:         entityID,
	})
	if err != nil {
		return fmt.Errorf("load entity values: %w", err)
	}
	if len(values) == 0 {
		return i.store.Remove(ctx, tenant, entityID)
	}

	attrIDs := make([]valueobjects.AttributeDefinitionID, 0, len(values))
	seen := map[string]bool{}
	for _, v := range values {
		id := v.AttributeDefinitionID()
		if !seen[id.String()] {
			seen[id.String()] = true
			attrIDs = append(attrIDs, id)
		}
	}
	attrs, err := repos.Attributes.GetMany(ctx, attrIDs)
	if err != nil {
		return fmt.Errorf("load attribute definitions: %w", err)
	}
	names := make(map[string]string, len(attrs))
	for _, a := range attrs {
		names[a.ID().String()] = a.InternalName()
	}

	doc := EntityDocument{
		TenantID:         tenant,
		TypeDefinitionID: typeID,
		EntityID:         entityID,
		Values:           make(map[string][]string, len(names)),
		UpdatedAt:        i.now(),
	}
	text := entityID.String()
	for _, v := range values {
		name := names[v.AttributeDefinitionID().String()]
		rendered := v.Value().String()
		doc.Values[name] = append(doc.Values[name], rendered)
		if v.Value().DataType().IsTextual() {
			text += " " + rendered
		}
	}
	doc.Text = text

	return i.store.Upsert(ctx, doc)
}

// Reindex rebuilds every entity document — bootstrap and disaster
// recovery. It walks every entity type and every entity page.
func (i *Indexer) Reindex(ctx context.Context, tenant valueobjects.TenantID) (int, error) {
	repos := i.newRepos()

	types, _, err := repos.TypeDefinitions.List(ctx, domaintypedef.Filter{TenantID: tenant}, db.Page{Limit: 500})
	if err != nil {
		return 0, err
	}

	count := 0
	for _, t := range types {
		offset := 0
		for {
			entities, _, err := repos.Values.ListEntities(ctx, tenant,
				[]valueobjects.TypeDefinitionID{t.ID()}, db.Page{Limit: 200, Offset: offset})
			if err != nil {
				return count, err
			}
			if len(entities) == 0 {
				break
			}
			for _, e := range entities {
				if err := i.Rebuild(ctx, tenant, e.TypeDefinitionID, e.EntityID); err != nil {
					return count, fmt.Errorf("rebuild %s: %w", e.EntityID, err)
				}
				count++
			}
			offset += len(entities)
		}
	}
	return count, nil
}
