// Package appctx holds the request-scoped vocabulary shared between the
// composition root and the feature subpackages (search, gql, computed): the
// per-request Repositories set and the ports handed around with it. It imports
// no composition wiring, so a subpackage can depend on the shared types without
// importing the application root — which is what lets those ports keep a single
// canonical definition instead of a cycle-avoiding local shim.
package appctx

import (
	"context"

	appquery "github.com/zkrebbekx/flexitype/application/query"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// Repositories is one request-scoped set of domain repositories. A fresh
// set means fresh dataloader caches, so nothing leaks across requests or
// tenants.
type Repositories struct {
	TypeDefinitions         typedefRepository
	Attributes              attributeRepository
	Values                  valueRepository
	Dependencies            dependencyRepository
	RelationshipDefinitions relationshipDefinitionRepository
	Relationships           relationshipRepository
	Query                   appquery.Repository
	// ValueReader is the read-model port over stored attribute values (see
	// ValueReader). The same backend struct that serves Values as the aggregate
	// repository satisfies it, so a request shares one concrete value repository
	// across the write and read ports.
	ValueReader ValueReader
	// SchemaVersions reports a tenant's persisted schema version; the GraphQL
	// engine reads it to keep its per-replica schema cache correct (issue #192).
	SchemaVersions SchemaVersionReader
}

// ValueReader is the application-owned read-model port over stored attribute
// values: the paginated, projection-style queries that back list and grid views
// plus the in-transaction lookups a value write consults (existing values,
// uniqueness counts). These are read concerns — pages, totals, entity summaries,
// upsert probes — with no part in the AttributeValue aggregate's persistence
// contract, so they live here in the shared application vocabulary rather than
// on domain/value.Repository, which stays the aggregate write port. The
// PostgreSQL and in-memory backends implement it on the same struct that
// implements the aggregate repository, so satisfying it costs no extra code; it
// mirrors appquery.Repository. Callers that need the in-transaction reads bind
// the aggregate repository to a transaction and read through its ValueReader
// view, so those reads observe the unit of work's uncommitted writes.
type ValueReader interface {
	// List returns a page of values and the total count for the filter.
	List(ctx context.Context, filter domainvalue.Filter, page db.Page) ([]*domainvalue.AttributeValue, int, error)

	// ListEntities returns a page of distinct entities holding live values of
	// any of the given type definitions (a type plus, optionally, its
	// descendants), most recently changed first, plus the total distinct-entity
	// count.
	ListEntities(ctx context.Context, tenant valueobjects.TenantID, typeDefIDs []valueobjects.TypeDefinitionID, page db.Page) ([]domainvalue.EntitySummary, int, error)

	// ListByDefinition returns one page of a definition's values plus the total
	// count (pages batch across definitions).
	ListByDefinition(ctx context.Context, defID valueobjects.AttributeDefinitionID, page db.Page) ([]*domainvalue.AttributeValue, int, error)

	// ListByEntities loads every live value held by any of the given entities,
	// in one query — the grid's projection path, so rendering a page of rows
	// never fans out to one query per entity.
	ListByEntities(ctx context.Context, tenant valueobjects.TenantID, entityIDs []valueobjects.EntityID) ([]*domainvalue.AttributeValue, error)

	// ListByEntity loads every live value of one entity. Loads for different
	// entities batch into one query — the hot path for hydrating consumer
	// objects.
	ListByEntity(ctx context.Context, key domainvalue.EntityKey) ([]*domainvalue.AttributeValue, error)

	// FindByDefinitionAndEntity returns the live values one entity holds for one
	// attribute. Used inside write transactions for multi-value and upsert
	// decisions.
	FindByDefinitionAndEntity(ctx context.Context, defID valueobjects.AttributeDefinitionID, entityID valueobjects.EntityID) ([]*domainvalue.AttributeValue, error)

	// CountByDefinitionAndValue counts live values of a definition equal to v,
	// excluding entity excludeEntity. Used to enforce unique attributes inside
	// write transactions.
	CountByDefinitionAndValue(ctx context.Context, defID valueobjects.AttributeDefinitionID, scope valueobjects.Scope, v valueobjects.Value, excludeEntity valueobjects.EntityID) (int, error)
}

// SchemaVersionReader reports a tenant's persisted schema version: a counter
// bumped whenever any type, attribute or relationship definition is created,
// updated, archived, restored or deleted. The GraphQL engine reads it to decide
// whether its cached, per-replica schema is stale, so a definition change made
// on one replica is observed by every replica (issue #192). It reads the tenant
// from the context and returns 0 when the tenant has no definitions yet.
type SchemaVersionReader interface {
	SchemaVersion(ctx context.Context) (uint64, error)
}

// SearchStore is the erasure-facing slice of the search-projection port: an
// entity's document is dropped with Remove, a tenant's with PurgeTenant. This
// is the single canonical definition of that port — the value interactor holds
// it for erasure and search.DocumentStore embeds it for projection maintenance.
// Nil disables search purging (the index is off).
type SearchStore interface {
	Remove(ctx context.Context, tenant valueobjects.TenantID, entityID valueobjects.EntityID) error
	// PurgeTenant HARD-deletes every search document of a tenant — the
	// right-to-erasure primitive — returning the row count.
	PurgeTenant(ctx context.Context, tenant valueobjects.TenantID) (int, error)
}

// Narrow aliases keep the struct readable without re-importing domain
// packages at every call site.
type (
	typedefRepository                = domaintypedef.Repository
	attributeRepository              = domainattribute.Repository
	valueRepository                  = domainvalue.Repository
	dependencyRepository             = domaindependency.Repository
	relationshipDefinitionRepository = domainrelationship.DefinitionRepository
	relationshipRepository           = domainrelationship.Repository
)
