// Package uow provides the shared unit-of-work: transaction wrapping with
// the standard pre/post/rollback commit handlers, plus per-request actor
// and tenant context.
package uow

import (
	"context"
	"sync/atomic"
	"time"

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

// AccessFromPermissions derives a principal's field-level access from a
// RESOLVED permission set: whether it holds admin, how many of its roles
// could not be resolved, and its merged per-attribute levels.
//
// It is the one place the rule lives, so a view that answers "what may this
// account actually do" cannot report something the request path does not
// enforce. The effective-permissions view read the merged map directly, and
// enforcement ignores that map for an admin and ignores it entirely for an
// unresolved role — so the view was wrong for the highest-privilege case and
// contradicted itself for a deleted role.
//
// An account naming a role that no longer exists is denied every attribute. A
// missing role contributes no permissions, and an empty map otherwise reads
// as "unrestricted", so deleting a role would have converted every account
// restricted only by it into one with full field access.
func AccessFromPermissions(admin bool, unresolvedRoles int, fieldPermissions map[string]string) Access {
	if unresolvedRoles > 0 {
		return DenyAll()
	}
	if admin || len(fieldPermissions) == 0 {
		return Access{Admin: true}
	}
	attr := make(map[string]Perm, len(fieldPermissions))
	for name, level := range fieldPermissions {
		attr[name] = Perm(level)
	}
	return Access{Attr: attr}
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

// --- tenant time zone --------------------------------------------------------

type timeZoneKey struct{}

// WithTimeZone stamps the tenant's time zone onto the context.
//
// It exists because `today` and `now` in a dependency condition or a dynamic
// default resolved against UTC, and there was no tenant time zone anywhere in
// the domain. For a tenant operating outside UTC, a date-boundary rule was
// wrong for part of every day: a condition on "expires before today" flipped
// at the wrong hour, and a `today` default recorded yesterday's date for
// anything created after the UTC midnight.
//
// Storage is unaffected. A date value is a CALENDAR DATE stored as midnight
// UTC, so only which day "today" names changes — not how it is stored or
// compared.
func WithTimeZone(ctx context.Context, loc *time.Location) context.Context {
	if loc == nil {
		return ctx
	}
	return context.WithValue(ctx, timeZoneKey{}, loc)
}

// TimeZoneFromContext returns the tenant's time zone, or UTC when none is
// stamped. UTC is the right default: it is what the system did before, and a
// deployment that has not said otherwise should not silently move its day
// boundary.
func TimeZoneFromContext(ctx context.Context) *time.Location {
	if loc, ok := ctx.Value(timeZoneKey{}).(*time.Location); ok && loc != nil {
		return loc
	}
	return time.UTC
}

// HasTimeZone reports whether a time zone was stamped on the context.
//
// It exists so a deployment default does not overwrite a per-request choice:
// TimeZoneFromContext answers UTC for "unset" and for "explicitly UTC", and
// those must be distinguishable at the point a default is applied.
func HasTimeZone(ctx context.Context) bool {
	loc, ok := ctx.Value(timeZoneKey{}).(*time.Location)
	return ok && loc != nil
}

// LocalNow returns the current instant in the tenant's time zone.
//
// Use it wherever a CALENDAR day is derived — `today`, a date default, a
// day-boundary comparison. Use UTCNow for a timestamp that records when
// something happened, which is an instant and has no time zone.
func LocalNow(ctx context.Context) time.Time { return UTCNow().In(TimeZoneFromContext(ctx)) }

// --- caller-supplied context values ------------------------------------------

type contextValuesKey struct{}

// WithContextValues stamps the caller's own facts onto the context, for
// dependency conditions that name one.
//
// An embedder anchors flexitype values to its own entities by an opaque
// entity_id, and those entities' primary fields live in host tables — a
// customer's tier, an order's channel, a document's workflow state. No
// condition could reference them, so a rule that depended on one had to be
// expressed by copying that field into flexitype and keeping the copy in
// step, which is the duplication soft typing exists to avoid.
//
// These values are read at evaluation time and never stored. Supply them per
// request, from the host's own records.
func WithContextValues(ctx context.Context, values map[string]valueobjects.Value) context.Context {
	if len(values) == 0 {
		return ctx
	}
	// Copy: the caller must not be able to mutate what a later evaluation
	// reads, and a request-scoped map is cheap.
	out := make(map[string]valueobjects.Value, len(values))
	for k, v := range values {
		out[k] = v
	}
	return context.WithValue(ctx, contextValuesKey{}, out)
}

// ContextValuesFromContext returns the caller-supplied facts, or nil.
//
// A condition naming a key that is absent does NOT match: a rule must not
// fire on a fact the caller did not supply, because "not supplied" and
// "supplied as the zero value" mean different things and only the caller
// knows which happened.
func ContextValuesFromContext(ctx context.Context) map[string]valueobjects.Value {
	values, _ := ctx.Value(contextValuesKey{}).(map[string]valueobjects.Value)
	return values
}
