// Package uow provides the shared unit-of-work: transaction wrapping with
// the standard pre/post/rollback commit handlers, plus per-request actor
// and tenant context.
package uow

import (
	"context"
	"sync/atomic"

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
// otherwise Attr maps an attribute internal name to its level.
//
// Default is the level applied to an attribute that Attr does not list. Its
// zero value means "unrestricted", so a permission set is a deny-list: it
// restricts the fields it names and leaves the rest accessible. Set Default
// to PermNone to invert that into an allow-list, where only the attributes
// Attr names are reachable. A multi-tenant host that derives permissions
// from its own roles should prefer the allow-list form, because a newly
// added attribute is then unreadable until the host grants it.
type Access struct {
	Admin   bool
	Attr    map[string]Perm
	Default Perm
}

// DenyAll is the fail-closed policy: no admin rights and an allow-list with
// nothing on it, so every attribute is unreadable and unwritable. It is what
// a context with no policy resolves to once RequireAccessPolicy is set.
func DenyAll() Access {
	return Access{Default: PermNone}
}

// SystemAccess is the unrestricted policy internal maintenance runs under:
// the outbox relay, the delivery worker, the retention pruner, the search
// reindex and the computed recompute. Those loops have no principal, so they
// stamp it explicitly rather than relying on the default — which
// RequireAccessPolicy inverts.
func SystemAccess() Access {
	return Access{Admin: true}
}

// WithSystemAccess stamps SystemAccess onto a background context.
func WithSystemAccess(ctx context.Context) context.Context {
	return WithAccess(ctx, SystemAccess())
}

// level returns the effective permission for an attribute name.
func (a Access) level(name string) Perm {
	if p, ok := a.Attr[name]; ok {
		return p
	}
	if a.Default != "" {
		return a.Default
	}
	return PermWrite
}

// CanRead reports whether the principal may read the named attribute.
func (a Access) CanRead(name string) bool {
	if a.Admin {
		return true
	}
	p := a.level(name)
	return p == PermRead || p == PermWrite
}

// CanWrite reports whether the principal may write the named attribute.
func (a Access) CanWrite(name string) bool {
	if a.Admin {
		return true
	}
	return a.level(name) == PermWrite
}

type accessKey struct{}

// WithAccess stamps the principal's field-level permissions onto the context.
func WithAccess(ctx context.Context, a Access) context.Context {
	return context.WithValue(ctx, accessKey{}, a)
}

// AccessFromContext returns the principal's field-level permissions,
// defaulting to full (admin) access — so unauthenticated development and
// admin accounts see everything.
//
// The default is permissive, which is convenient for the standalone service
// (its authentication middleware always stamps a policy) but is the wrong
// direction for an embedder that forgets to. Embedders select
// flexitype.WithFailClosedACL, which inverts the default to DenyAll for the
// whole process.
func AccessFromContext(ctx context.Context) Access {
	a, _ := AccessFromContextOK(ctx)
	return a
}

// AccessFromContextOK returns the principal's field-level permissions and
// reports whether the context actually carried a policy. A caller uses the
// second result to distinguish "no policy was stamped" from "an unrestricted
// policy was stamped".
func AccessFromContextOK(ctx context.Context) (Access, bool) {
	if a, ok := ctx.Value(accessKey{}).(Access); ok {
		return a, true
	}
	if failClosed.Load() {
		return DenyAll(), false
	}
	return Access{Admin: true}, false
}

// failClosed inverts the default of AccessFromContext for the whole process.
// It is process-wide rather than per-service because it describes a
// deployment posture, and it only ever moves toward the stricter setting, so
// two services in one process cannot weaken each other.
var failClosed atomic.Bool

// RequireAccessPolicy makes a context with no access policy deny every
// attribute instead of granting admin. Select it through
// flexitype.WithFailClosedACL.
//
// It applies to the whole process and cannot be undone. Both properties come
// from the guarantee it provides: a code path that reaches an interactor
// without stamping a policy — a background job, a scheduled task, a new
// resolver — must fail rather than run with more privilege than the host
// intended, and no later construction may relax that.
func RequireAccessPolicy() { failClosed.Store(true) }

// AccessPolicyRequired reports whether the process denies access when a
// context carries no policy.
func AccessPolicyRequired() bool { return failClosed.Load() }

// EnsureTenant hides cross-tenant resources: a caller asking for another
// tenant's aggregate by ID gets NotFound — never confirmation it exists.
// Every interactor calls this after loading an aggregate by raw ID.
func EnsureTenant(ctx context.Context, owner valueobjects.TenantID, entity, id string) error {
	if owner == TenantFromContext(ctx) {
		return nil
	}
	return domainerrors.NewNotFound(entity, id)
}
