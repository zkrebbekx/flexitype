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
	// SchemaVersions reports a tenant's persisted schema version; the GraphQL
	// engine reads it to keep its per-replica schema cache correct (issue #192).
	SchemaVersions SchemaVersionReader
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
