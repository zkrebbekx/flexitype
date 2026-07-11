// Package uow provides the shared unit-of-work: transaction wrapping with
// the standard pre/post/rollback commit handlers, plus per-request actor
// and tenant context.
package uow

import (
	"context"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// ActorKind classifies who performed an action.
type ActorKind string

// The supported actor kinds.
const (
	ActorServiceAccount ActorKind = "service_account"
	ActorUser           ActorKind = "user"
	ActorSystem         ActorKind = "system"
)

// Actor identifies the caller for activity logging and event metadata.
type Actor struct {
	ID   string
	Name string
	Kind ActorKind
}

// String renders the actor for envelopes and logs, e.g.
// "service_account:ci-importer".
func (a Actor) String() string {
	if a.Name == "" && a.ID == "" {
		return string(ActorSystem)
	}
	name := a.Name
	if name == "" {
		name = a.ID
	}
	return string(a.Kind) + ":" + name
}

type actorKey struct{}

type tenantKey struct{}

// WithActor stamps the calling actor onto the context.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFromContext returns the calling actor, defaulting to system.
func ActorFromContext(ctx context.Context) Actor {
	if a, ok := ctx.Value(actorKey{}).(Actor); ok {
		return a
	}
	return Actor{Kind: ActorSystem}
}

// WithTenant stamps the active tenant onto the context.
func WithTenant(ctx context.Context, t valueobjects.TenantID) context.Context {
	return context.WithValue(ctx, tenantKey{}, t)
}

// TenantFromContext returns the active tenant, defaulting to
// valueobjects.DefaultTenant.
func TenantFromContext(ctx context.Context) valueobjects.TenantID {
	if t, ok := ctx.Value(tenantKey{}).(valueobjects.TenantID); ok && !t.IsZero() {
		return t
	}
	return valueobjects.DefaultTenant
}

// Perm is an attribute-level access level.
type Perm string

// The supported attribute access levels.
const (
	PermNone  Perm = "none"
	PermRead  Perm = "read"
	PermWrite Perm = "write"
)

// Access is a principal's field-level permissions. Admin grants everything;
// otherwise Attr maps an attribute internal name to its level. Attributes
// not listed are fully accessible (a permission set restricts specific
// fields rather than allow-listing all of them).
type Access struct {
	Admin bool
	Attr  map[string]Perm
}

// CanRead reports whether the principal may read the named attribute.
func (a Access) CanRead(name string) bool {
	if a.Admin {
		return true
	}
	p, ok := a.Attr[name]
	if !ok {
		return true
	}
	return p == PermRead || p == PermWrite
}

// CanWrite reports whether the principal may write the named attribute.
func (a Access) CanWrite(name string) bool {
	if a.Admin {
		return true
	}
	p, ok := a.Attr[name]
	if !ok {
		return true
	}
	return p == PermWrite
}

type accessKey struct{}

// WithAccess stamps the principal's field-level permissions onto the context.
func WithAccess(ctx context.Context, a Access) context.Context {
	return context.WithValue(ctx, accessKey{}, a)
}

// AccessFromContext returns the principal's field-level permissions,
// defaulting to full (admin) access — so unauthenticated development and
// admin accounts see everything.
func AccessFromContext(ctx context.Context) Access {
	if a, ok := ctx.Value(accessKey{}).(Access); ok {
		return a
	}
	return Access{Admin: true}
}

// EnsureTenant hides cross-tenant resources: a caller asking for another
// tenant's aggregate by ID gets NotFound — never confirmation it exists.
// Every interactor calls this after loading an aggregate by raw ID.
func EnsureTenant(ctx context.Context, owner valueobjects.TenantID, entity, id string) error {
	if owner == TenantFromContext(ctx) {
		return nil
	}
	return domainerrors.NewNotFound(entity, id)
}
