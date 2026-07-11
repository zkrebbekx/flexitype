// Package uow provides the shared unit-of-work: transaction wrapping with
// the standard pre/post/rollback commit handlers, plus per-request actor
// and tenant context.
package uow

import (
	"context"

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
