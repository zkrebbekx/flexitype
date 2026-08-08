// Package admin implements runtime provisioning of tenants and service
// accounts — the hosted-tier control plane. These usecases operate across
// tenants (not scoped to the caller's tenant like the rest of the API) and
// are reached only by admin-scoped callers; the HTTP layer enforces that.
// Secrets are generated here, hashed at rest, and returned in plaintext
// exactly once (on create and rotate).
package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"regexp"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)

// The audit entities the control plane records against. An operator answers
// "who created this account, and who rotated it since?" by filtering the
// activity log on these.
const (
	EntityTenant         = "tenant"
	EntityServiceAccount = "service_account"
	EntityRole           = "role"
)

// Tenant is one provisioned tenant.
type Tenant struct {
	ID        ulid.ID   `json:"id"`
	Name      string    `json:"name"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ServiceAccount is one provisioned machine identity (secrets never
// serialized).
type ServiceAccount struct {
	ID       ulid.ID                `json:"id"`
	TenantID string                 `json:"tenant_id"`
	Name     string                 `json:"name"`
	Scopes   []serviceaccount.Scope `json:"scopes"`
	// Roles the account holds, by name within its tenant. Their scopes and
	// field permissions are merged in at authentication, so editing a role
	// changes every account holding it rather than requiring each to be
	// rewritten.
	Roles []string `json:"roles,omitempty"`
	// FieldPermissions are this account's own per-attribute overrides. They
	// win over the roles', so one person can be given an exception without
	// inventing a role for them.
	FieldPermissions map[string]string `json:"field_permissions,omitempty"`
	Active           bool              `json:"active"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// Role names a permission set once, so it can be assigned rather than
// duplicated.
//
// Field-level permissions were stored per account with no indirection, so a
// deployment fronting many human users created one account per person and
// duplicated the whole per-attribute set onto each: a permission change meant
// rewriting every affected account, and "what can a reviewer see" could only
// be answered by reading all of them.
type Role struct {
	ID          ulid.ID                `json:"id"`
	TenantID    string                 `json:"tenant_id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Scopes      []serviceaccount.Scope `json:"scopes"`
	// FieldPermissions maps an attribute internal name to
	// none | read | write. Unlisted attributes stay fully accessible.
	FieldPermissions map[string]string `json:"field_permissions,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// Store persists tenants and service accounts.
type Store interface {
	// WithTx binds the store to an open transaction, so a usecase's reads,
	// its writes, its row locks and the activity entry the unit of work
	// writes all commit or roll back together.
	WithTx(tx db.Tx) Store

	CreateTenant(ctx context.Context, t Tenant) error
	ListTenants(ctx context.Context) ([]Tenant, error)
	GetTenantByName(ctx context.Context, name string) (Tenant, error)
	SetTenantActive(ctx context.Context, name string, active bool, now time.Time) error

	CreateAccount(ctx context.Context, a ServiceAccount, secretHash string) error
	ListAccounts(ctx context.Context, tenant string) ([]ServiceAccount, error)
	GetAccount(ctx context.Context, id ulid.ID) (ServiceAccount, error)
	UpdateSecret(ctx context.Context, id ulid.ID, secretHash string, now time.Time) error
	SetAccountActive(ctx context.Context, id ulid.ID, active bool, now time.Time) error
	SetAccountRoles(ctx context.Context, id ulid.ID, roles []string, perms map[string]string, now time.Time) error

	// UpsertRole returns the PERSISTED row. An update keeps the existing id
	// and created_at, so the caller must be told what is stored rather than
	// what was proposed.
	UpsertRole(ctx context.Context, r Role) (Role, error)
	ListRoles(ctx context.Context, tenant string) ([]Role, error)
	DeleteRole(ctx context.Context, tenant, name string) error
	// CountAccountsWithRole reports how many accounts still name the role.
	CountAccountsWithRole(ctx context.Context, tenant, name string) (int, error)

	// LockRole reads one role and holds an EXCLUSIVE row lock on it for the
	// rest of the transaction. DeleteRole takes it before it counts the
	// holders, so no assignment can land between the count and the delete.
	// It reports NotFound when the tenant has no such role.
	LockRole(ctx context.Context, tenant, name string) (Role, error)
	// LockRolesShared reports which of names the tenant has, and holds a
	// SHARED row lock on each for the rest of the transaction. An assignment
	// takes it before it writes the account row, so a concurrent DeleteRole
	// — which wants the exclusive lock — waits and then sees the holder.
	LockRolesShared(ctx context.Context, tenant string, names []string) ([]string, error)
}

// Interactor implements the provisioning usecases.
type Interactor struct {
	store Store
	// unit runs each mutating usecase as ONE transaction and writes the
	// activity entry the usecase records in that same transaction. It is
	// never nil: WithUnitOfWork replaces the direct fallback below.
	unit uow.UnitOfWork
	// authCache, when set, is evicted after a rotation or a revocation so the
	// old credential stops working at once rather than at the end of the cache
	// TTL. Nil when no caching authenticator is configured.
	authCache serviceaccount.Invalidator
	now       func() time.Time
}

// NewInteractor wires the admin usecases.
//
// Without WithUnitOfWork the usecases run as bare store calls and record no
// audit entry — the pre-1.4 behaviour, kept so a caller that builds the
// interactor over its own store still compiles. Every database-backed
// deployment gets the real unit of work from Service.AdminInteractor.
func NewInteractor(store Store, opts ...Option) *Interactor {
	i := &Interactor{store: store, now: uow.UTCNow, unit: directUnitOfWork{}}
	for _, opt := range opts {
		opt(i)
	}
	return i
}

// Option customises the admin interactor.
type Option func(*Interactor)

// WithAuthCache lets a rotation or a revocation evict the account's cached
// authentications immediately. Without it, both take effect only when the
// cache entry expires.
func WithAuthCache(inv serviceaccount.Invalidator) Option {
	return func(i *Interactor) { i.authCache = inv }
}

// WithUnitOfWork runs every mutating usecase in one transaction and writes
// its activity entry in that transaction.
//
// A credential or a role change is the change an incident review asks about
// first, so it must leave a record that exists if and only if the change
// committed. The transaction also closes two check-then-act races that bare
// store calls left open: the count-then-delete in DeleteRole and the
// lookup-then-insert in CreateAccount.
func WithUnitOfWork(u uow.UnitOfWork) Option {
	return func(i *Interactor) {
		if u != nil {
			i.unit = u
		}
	}
}

// directUnitOfWork runs a usecase body with no transaction and drops the
// audit changes it records. It keeps an interactor built without
// WithUnitOfWork working exactly as it did before, for a caller that has no
// transactor to give.
type directUnitOfWork struct{}

func (directUnitOfWork) Execute(_ context.Context, fn func(tx db.Transactor, c *uow.Collector) error) error {
	return fn(nil, &uow.Collector{})
}

// storeFor binds the store to the unit of work's transaction. A nil tx comes
// from directUnitOfWork, which has none.
func (i *Interactor) storeFor(tx db.Transactor) Store {
	if tx == nil {
		return i.store
	}
	return i.store.WithTx(tx)
}

// forTenant stamps the AFFECTED tenant onto the context.
//
// The unit of work takes an activity entry's tenant from the context, and an
// admin request carries no tenant of its own — it acts across tenants. Without
// this stamp every provisioning entry would land under the default tenant,
// where the affected tenant's own operators cannot read it.
func forTenant(ctx context.Context, tenant string) context.Context {
	return uow.WithTenant(ctx, valueobjects.TenantID(tenant))
}

// activeState is the audit descriptor for an enable/disable flip.
type activeState struct {
	Active bool `json:"active"`
}

// secretRotation is the audit descriptor for a credential rotation. It
// records THAT the secret changed. The secret, its hash and the minted token
// are never written to the activity log.
type secretRotation struct {
	SecretRotated bool `json:"secret_rotated"`
}

// roleAssignment is the audit descriptor for an account's roles and its own
// per-attribute overrides.
type roleAssignment struct {
	Roles []string `json:"roles"`
	// FieldPermissions names attributes. It is recorded in full: the field
	// ACL governs attribute VALUES, not attribute definitions, so a principal
	// that can read the activity log can already list the same names through
	// the schema API. What a permission change grants or removes is the
	// substance of the entry, so redacting it would leave nothing to audit.
	FieldPermissions map[string]string `json:"field_permissions,omitempty"`
}

// invalidate drops an account's cached authentications, if a cache is wired.
func (i *Interactor) invalidate(id ulid.ID) {
	if i.authCache != nil {
		i.authCache.Invalidate(id.String())
	}
}

// invalidateTenant drops every cached authentication for one tenant.
//
// A role edit and a tenant deactivation both change what many accounts may do
// at once, and the cache is keyed by token, so neither is expressible as a
// per-account eviction. Without this, the change an operator most needs to
// take effect at once — removing a grant during an incident — was the one
// deferred by up to the cache TTL on every replica, while the API had already
// confirmed it.
//
// An authenticator with no tenant index simply does not implement the
// interface, and the change then waits for the TTL as before.
func (i *Interactor) invalidateTenant(tenant string) {
	if inv, ok := i.authCache.(serviceaccount.TenantInvalidator); ok && i.authCache != nil {
		inv.InvalidateTenant(tenant)
	}
}

// CreateTenant provisions a new tenant.
func (i *Interactor) CreateTenant(ctx context.Context, name string) (*Tenant, error) {
	if !namePattern.MatchString(name) {
		return nil, domainerrors.NewValidation("tenant name must be lowercase alphanumeric with _ or -, 2-64 chars")
	}

	now := i.now()
	t := Tenant{ID: ulid.New(), Name: name, Active: true, CreatedAt: now, UpdatedAt: now}
	tctx := forTenant(ctx, name)
	err := i.unit.Execute(tctx, func(tx db.Transactor, c *uow.Collector) error {
		s := i.storeFor(tx)
		if _, err := s.GetTenantByName(tctx, name); err == nil {
			return domainerrors.NewConflict("a tenant with this name already exists", "name", name)
		} else if !domainerrors.IsNotFound(err) {
			return err
		}
		if err := s.CreateTenant(tctx, t); err != nil {
			return err
		}
		c.RecordChange(activity.Change{
			Entity:   EntityTenant,
			EntityID: t.ID.String(),
			Action:   activity.ActionCreated,
			After:    t,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTenants returns every provisioned tenant.
func (i *Interactor) ListTenants(ctx context.Context) ([]Tenant, error) {
	return i.store.ListTenants(ctx)
}

// SetTenantActive enables or disables a tenant.
//
// Deactivation is enforced at authentication (the tenant's active flag is
// joined in), so it suspends every account under the tenant. The tenant's
// cached authentications are dropped, or a suspension would not take effect
// until the cache TTL expired on every replica.
func (i *Interactor) SetTenantActive(ctx context.Context, name string, active bool) error {
	tctx := forTenant(ctx, name)
	err := i.unit.Execute(tctx, func(tx db.Transactor, c *uow.Collector) error {
		s := i.storeFor(tx)
		before, err := s.GetTenantByName(tctx, name)
		if err != nil {
			return err
		}
		if err := s.SetTenantActive(tctx, name, active, i.now()); err != nil {
			return err
		}
		c.RecordChange(activity.Change{
			Entity:   EntityTenant,
			EntityID: before.ID.String(),
			Action:   activity.ActionUpdated,
			Before:   activeState{Active: before.Active},
			After:    activeState{Active: active},
		})
		return nil
	})
	if err != nil {
		return err
	}
	// The cache eviction runs only after the commit. Evicting inside the
	// transaction would drop live authentications for a change that then
	// rolled back.
	i.invalidateTenant(name)
	return nil
}

// AccountWithToken pairs a created/rotated account with its one-time
// plaintext token.
type AccountWithToken struct {
	Account ServiceAccount `json:"account"`
	// Token is shown exactly once; it is never recoverable afterward.
	Token string `json:"token"`
}

// CreateAccountInput provisions a service account.
type CreateAccountInput struct {
	TenantName string
	Name       string
	Scopes     []string
	// Roles the account holds from the start, so a person can be provisioned
	// with a permission set rather than a copy of one.
	Roles []string
	// FieldPermissions are per-account overrides; roles are the normal way.
	FieldPermissions map[string]string

	// ID sets the account's identifier instead of generating one. It must be
	// a ULID.
	//
	// Together with Secret it makes a caller-supplied credential possible: a
	// token is `ft_<id>_<secret>`, so a deployment that wants to know the
	// bootstrap token BEFORE the service starts has to fix both halves. Leave
	// both empty for the normal path, where the service mints the credential
	// and returns it once.
	ID string
	// Secret sets the account's secret instead of generating one. It must be
	// at least MinSuppliedSecretLength characters, so a weak literal in a
	// manifest fails at creation rather than at exploitation.
	//
	// It is never logged and never returned; only the token built from it is,
	// and only once.
	Secret string
}

// MinSuppliedSecretLength is the shortest secret CreateAccountInput.Secret
// accepts. A generated secret is 32 random bytes in base64url, which is 43
// characters; this floor is lower so an operator can use a different
// generator, and high enough that a guessable literal is refused.
const MinSuppliedSecretLength = 32

// CreateAccount provisions a service account under an existing tenant and
// returns its bearer token once.
func (i *Interactor) CreateAccount(ctx context.Context, in CreateAccountInput) (*AccountWithToken, error) {
	if !namePattern.MatchString(in.Name) {
		return nil, domainerrors.NewValidation("account name must be lowercase alphanumeric with _ or -, 2-64 chars")
	}
	// An account whose whole permission set comes from its roles needs no
	// scope of its own. Requiring one would force every role holder to be
	// granted a scope directly, which is the duplication roles remove.
	scopes, err := parseScopesAllowEmpty(in.Scopes)
	if err != nil {
		return nil, err
	}
	if len(scopes) == 0 && len(in.Roles) == 0 {
		return nil, domainerrors.NewValidation("at least one scope or role is required")
	}
	acctID, secret, err := identityFor(in)
	if err != nil {
		return nil, err
	}
	now := i.now()

	var out AccountWithToken
	tctx := forTenant(ctx, in.TenantName)
	err = i.unit.Execute(tctx, func(tx db.Transactor, c *uow.Collector) error {
		s := i.storeFor(tx)
		tenant, err := s.GetTenantByName(tctx, in.TenantName)
		if err != nil {
			return err
		}
		if err := validateFieldPermissions(in.FieldPermissions); err != nil {
			return err
		}
		// The shared role lock is taken inside this transaction, so a
		// DeleteRole running concurrently either waits and then sees this
		// account as a holder, or completes first and makes the name
		// resolve to NotFound here.
		if err := i.checkRolesExist(tctx, s, tenant.Name, in.Roles); err != nil {
			return err
		}
		acct := ServiceAccount{
			ID:               acctID,
			TenantID:         tenant.Name,
			Name:             in.Name,
			Scopes:           scopes,
			Roles:            in.Roles,
			FieldPermissions: in.FieldPermissions,
			Active:           true,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if err := s.CreateAccount(tctx, acct, serviceaccount.HashSecret(secret)); err != nil {
			return err
		}
		// ServiceAccount carries no secret field, so the stored struct is
		// already a safe descriptor. The secret, its hash and the token are
		// never recorded.
		c.RecordChange(activity.Change{
			Entity:   EntityServiceAccount,
			EntityID: acct.ID.String(),
			Action:   activity.ActionCreated,
			After:    acct,
		})
		out = AccountWithToken{Account: acct, Token: serviceaccount.MintToken(acct.ID.String(), secret)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListAccounts returns a tenant's service accounts (no secrets).
func (i *Interactor) ListAccounts(ctx context.Context, tenant string) ([]ServiceAccount, error) {
	return i.store.ListAccounts(ctx, tenant)
}

// RotateSecret issues a new secret for an account and returns the new token
// once.
//
// The old secret stops working immediately when a cache is wired (see
// WithAuthCache); otherwise it stops working when its cached authentication
// expires, within FLEXITYPE_AUTH_CACHE_TTL. The distinction matters during
// incident response: an operator who rotates a leaked token and records the
// time needs to know whether requests after that timestamp could still have
// used it.
func (i *Interactor) RotateSecret(ctx context.Context, rawID string) (*AccountWithToken, error) {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	// The account is read once outside the transaction to learn which tenant
	// the audit entry belongs to; the transaction reads it again so the
	// recorded state is the state the write replaced.
	owner, err := i.store.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	secret, err := generateSecret()
	if err != nil {
		return nil, err
	}

	var out AccountWithToken
	tctx := forTenant(ctx, owner.TenantID)
	err = i.unit.Execute(tctx, func(tx db.Transactor, c *uow.Collector) error {
		s := i.storeFor(tx)
		acct, err := s.GetAccount(tctx, id)
		if err != nil {
			return err
		}
		if err := s.UpdateSecret(tctx, id, serviceaccount.HashSecret(secret), i.now()); err != nil {
			return err
		}
		// Record THAT the credential changed. Recording the secret, its hash
		// or the token would put the credential in a table the tenant's own
		// readers can list.
		c.RecordChange(activity.Change{
			Entity:   EntityServiceAccount,
			EntityID: id.String(),
			Action:   activity.ActionUpdated,
			After:    secretRotation{SecretRotated: true},
		})
		out = AccountWithToken{Account: acct, Token: serviceaccount.MintToken(id.String(), secret)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	i.invalidate(id)
	return &out, nil
}

// Revoke deactivates a service account. Its token stops working immediately
// when a cache is wired (see WithAuthCache), otherwise within the auth cache
// TTL.
func (i *Interactor) Revoke(ctx context.Context, rawID string) error {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	owner, err := i.store.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	tctx := forTenant(ctx, owner.TenantID)
	err = i.unit.Execute(tctx, func(tx db.Transactor, c *uow.Collector) error {
		s := i.storeFor(tx)
		before, err := s.GetAccount(tctx, id)
		if err != nil {
			return err
		}
		if err := s.SetAccountActive(tctx, id, false, i.now()); err != nil {
			return err
		}
		c.RecordChange(activity.Change{
			Entity:   EntityServiceAccount,
			EntityID: id.String(),
			Action:   activity.ActionUpdated,
			Before:   activeState{Active: before.Active},
			After:    activeState{Active: false},
		})
		return nil
	})
	if err != nil {
		return err
	}
	i.invalidate(id)
	return nil
}

// parseScopesAllowEmpty validates scope names without requiring any.
//
// A role may carry field permissions and no scope at all: "this person sees
// every attribute except salary" is a real grant, and forcing a scope onto it
// would widen what the role gives beyond what the operator asked for.
func parseScopesAllowEmpty(raw []string) ([]serviceaccount.Scope, error) {
	out := make([]serviceaccount.Scope, 0, len(raw))
	for _, s := range raw {
		switch serviceaccount.Scope(s) {
		case serviceaccount.ScopeRead, serviceaccount.ScopeWrite, serviceaccount.ScopeAdmin:
			out = append(out, serviceaccount.Scope(s))
		default:
			return nil, domainerrors.NewValidation("unknown scope", "scope", s)
		}
	}
	return out, nil
}

// generateSecret returns a 256-bit URL-safe random secret.
// identityFor resolves the account id and secret: generated, or supplied by
// the caller and validated here.
//
// Both halves must be supplied together. Supplying one would produce a token
// the caller cannot predict while looking as though it could, which is the
// failure this feature exists to remove.
func identityFor(in CreateAccountInput) (ulid.ID, string, error) {
	if in.ID == "" && in.Secret == "" {
		secret, err := generateSecret()
		if err != nil {
			return ulid.ID{}, "", err
		}
		return ulid.New(), secret, nil
	}
	if in.ID == "" || in.Secret == "" {
		return ulid.ID{}, "", domainerrors.NewValidation(
			"a supplied credential needs both an id and a secret: a token is ft_<id>_<secret>")
	}
	id, err := ulid.Parse(in.ID)
	if err != nil {
		return ulid.ID{}, "", domainerrors.NewValidation("the supplied account id is not a ULID")
	}
	if len(in.Secret) < MinSuppliedSecretLength {
		return ulid.ID{}, "", domainerrors.NewValidation(fmt.Sprintf(
			"a supplied secret must be at least %d characters", MinSuppliedSecretLength))
	}
	return id, in.Secret, nil
}

func generateSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// --- roles -------------------------------------------------------------------

// UpsertRoleInput creates or replaces a role by name within a tenant.
type UpsertRoleInput struct {
	TenantName       string
	Name             string
	Description      string
	Scopes           []string
	FieldPermissions map[string]string
}

// UpsertRole creates or replaces a role.
//
// Replace rather than patch: a permission set is read as a whole by the
// person auditing it, and a partial update makes "what does this role allow"
// a question about history rather than about the current record.
func (i *Interactor) UpsertRole(ctx context.Context, in UpsertRoleInput) (*Role, error) {
	if !namePattern.MatchString(in.Name) {
		return nil, domainerrors.NewValidation("role name must be lowercase alphanumeric with _ or -, 2-64 chars")
	}
	scopes, err := parseScopesAllowEmpty(in.Scopes)
	if err != nil {
		return nil, err
	}
	// A role may not carry admin. The admin scope is a global,
	// cross-tenant privilege AND it short-circuits the whole field ACL, so a
	// role granting it would void the account's own field_permissions —
	// contradicting this feature's stated precedence rule — and confer the
	// control plane. It would also be invisible: the account row would still
	// read scopes:["read"] with a field restriction, so a permissions review
	// would call it safe. Grant admin directly on an account, where it shows.
	for _, sc := range scopes {
		if sc == serviceaccount.ScopeAdmin {
			return nil, domainerrors.NewValidation(
				"a role may not grant the admin scope; grant it on the account, where it is visible in the account row")
		}
	}
	if err := validateFieldPermissions(in.FieldPermissions); err != nil {
		return nil, err
	}
	now := i.now()

	var stored Role
	tctx := forTenant(ctx, in.TenantName)
	err = i.unit.Execute(tctx, func(tx db.Transactor, c *uow.Collector) error {
		s := i.storeFor(tx)
		tenant, err := s.GetTenantByName(tctx, in.TenantName)
		if err != nil {
			return err
		}
		// Lock the row this upsert replaces, if there is one. It gives the
		// entry an accurate before-state and it serializes the upsert against
		// a concurrent delete of the same role.
		before, err := s.LockRole(tctx, tenant.Name, in.Name)
		replaced := err == nil
		if err != nil && !domainerrors.IsNotFound(err) {
			return err
		}
		role := Role{
			ID:               ulid.New(),
			TenantID:         tenant.Name,
			Name:             in.Name,
			Description:      in.Description,
			Scopes:           scopes,
			FieldPermissions: in.FieldPermissions,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		// The store returns what is STORED. An update keeps the existing id
		// and created_at, so returning the proposed struct described a row
		// that does not exist: a provisioning script that stores the returned
		// id as its handle, or logs it for audit, recorded a different
		// non-existent id on every idempotent re-run.
		stored, err = s.UpsertRole(tctx, role)
		if err != nil {
			return err
		}
		ch := activity.Change{
			Entity:   EntityRole,
			EntityID: stored.ID.String(),
			Action:   activity.ActionCreated,
			After:    stored,
		}
		if replaced {
			ch.Action = activity.ActionUpdated
			ch.Before = before
		}
		c.RecordChange(ch)
		return nil
	})
	if err != nil {
		return nil, err
	}
	i.invalidateTenant(in.TenantName)
	return &stored, nil
}

// ListRoles returns a tenant's roles.
func (i *Interactor) ListRoles(ctx context.Context, tenant string) ([]Role, error) {
	return i.store.ListRoles(ctx, tenant)
}

// DeleteRole removes a role that no account holds.
//
// It refuses while accounts still name it. Deleting a held role used to leave
// those accounts pointing at nothing, and a permission set that resolves to
// nothing is an EMPTY map — which read as "unrestricted" and granted every
// attribute the role was hiding. The exposure was silent: no error, no log
// line, and no change to the account rows, which still listed the role name,
// so the account listing looked unchanged while the effective permission had
// inverted. Because roles are the bulk-restriction mechanism, the blast
// radius scaled with adoption.
//
// Reassign the accounts first (PUT /service-accounts/{id}/roles). To retire a
// role's grants without touching every account, upsert it with an empty scope
// set and the restrictions you want to keep.
// The count and the delete run in ONE transaction, and the role row is locked
// EXCLUSIVELY before the count. Without the lock the guard was
// check-then-act: an assignment naming the role could commit between the
// count of zero and the delete, and the account was left holding a role that
// no longer existed.
func (i *Interactor) DeleteRole(ctx context.Context, tenant, name string) error {
	tctx := forTenant(ctx, tenant)
	err := i.unit.Execute(tctx, func(tx db.Transactor, c *uow.Collector) error {
		s := i.storeFor(tx)
		// Lock FIRST, count SECOND. An assignment holds the shared lock on
		// the same row until it commits, so this waits and then counts it.
		role, err := s.LockRole(tctx, tenant, name)
		if err != nil && !domainerrors.IsNotFound(err) {
			return err
		}
		held, cerr := s.CountAccountsWithRole(tctx, tenant, name)
		if cerr != nil {
			return cerr
		}
		if held > 0 {
			return domainerrors.NewConflict(
				"the role is still assigned; reassign those accounts before deleting it",
				"role", name, "accounts", held)
		}
		if derr := s.DeleteRole(tctx, tenant, name); derr != nil {
			return derr
		}
		c.RecordChange(activity.Change{
			Entity:   EntityRole,
			EntityID: role.ID.String(),
			Action:   activity.ActionRemoved,
			Before:   role,
		})
		return nil
	})
	if err != nil {
		return err
	}
	i.invalidateTenant(tenant)
	return nil
}

// EffectiveAccount reports what a principal can ACTUALLY do: its scopes and
// field permissions after its roles are merged in.
//
// The listing endpoints report accounts and roles as STORED, which is what an
// operator edits — but it meant a permissions review had to fetch every role
// an account names and union them by hand. Anyone doing that by eye would
// have read `scopes: ["read"]` with a field restriction and called the
// account safe, while a role it holds granted something wider.
//
// The merge runs through serviceaccount.Resolve, the same function
// authentication uses, so this view cannot report one thing while the
// enforcement path does another.
func (i *Interactor) EffectiveAccount(ctx context.Context, rawID string) (*EffectiveAccountView, error) {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	acct, err := i.store.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	roles, err := i.store.ListRoles(ctx, acct.TenantID)
	if err != nil {
		return nil, err
	}
	grants := make([]serviceaccount.RoleGrant, 0, len(roles))
	for _, r := range roles {
		grants = append(grants, serviceaccount.RoleGrant{
			Name: r.Name, Scopes: r.Scopes, FieldPermissions: r.FieldPermissions,
		})
	}
	resolved := serviceaccount.Resolve(serviceaccount.Account{
		ID:               acct.ID.String(),
		Name:             acct.Name,
		TenantID:         acct.TenantID,
		Scopes:           acct.Scopes,
		FieldPermissions: acct.FieldPermissions,
	}, acct.Roles, grants)

	// The enforced policy, derived by the same function the request path
	// uses. The merged map alone answered the wrong question: enforcement
	// ignores it for an admin, and ignores it entirely when a role is
	// unresolved.
	enforced := uow.AccessFromPermissions(
		resolved.HasScope(serviceaccount.ScopeAdmin),
		len(resolved.UnresolvedRoles),
		resolved.FieldPermissions)

	return &EffectiveAccountView{
		ID:               acct.ID,
		TenantID:         acct.TenantID,
		Name:             acct.Name,
		Active:           acct.Active,
		Roles:            acct.Roles,
		Scopes:           resolved.Scopes,
		FieldPermissions: resolved.FieldPermissions,
		UnresolvedRoles:  resolved.UnresolvedRoles,
		FieldACLBypassed: enforced.Admin,
		DeniedAll:        len(resolved.UnresolvedRoles) > 0,
	}, nil
}

// EffectiveAccountView is an account's resolved permissions.
type EffectiveAccountView struct {
	ID       ulid.ID `json:"id"`
	TenantID string  `json:"tenant_id"`
	Name     string  `json:"name"`
	Active   bool    `json:"active"`
	// Roles are the names the account holds, as stored.
	Roles []string `json:"roles"`
	// Scopes are the account's own scopes unioned with its roles'.
	Scopes []serviceaccount.Scope `json:"scopes"`
	// FieldPermissions are the merged per-attribute levels: the most
	// permissive any role grants, with the account's own entry winning.
	FieldPermissions map[string]string `json:"field_permissions,omitempty"`
	// UnresolvedRoles names roles the account holds that no longer exist.
	// A principal carrying one is denied every attribute, so this being
	// non-empty is a fault to repair, not a note.
	UnresolvedRoles []string `json:"unresolved_roles,omitempty"`
	// FieldACLBypassed reports that enforcement grants EVERY attribute
	// regardless of FieldPermissions — the account holds admin, or it
	// declares no restrictions at all. When it is true, FieldPermissions
	// describes what the roles merged to, NOT what applies: a reviewer
	// reading `salary: none` under an admin account would otherwise sign off
	// an account with unrestricted field access.
	FieldACLBypassed bool `json:"field_acl_bypassed"`
	// DeniedAll reports that enforcement denies EVERY attribute because a
	// role could not be resolved. It takes precedence over FieldACLBypassed
	// and over FieldPermissions.
	DeniedAll bool `json:"denied_all"`
}

// AssignRolesInput sets an account's roles and its own overrides.
type AssignRolesInput struct {
	AccountID        string
	Roles            []string
	FieldPermissions map[string]string
}

// AssignRoles replaces an account's roles and per-account overrides.
//
// The auth cache is evicted so the change takes effect at once rather than at
// the end of the TTL — a permission REMOVAL that waits is the one that
// matters.
func (i *Interactor) AssignRoles(ctx context.Context, in AssignRolesInput) error {
	id, err := ulid.Parse(in.AccountID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	if err := validateFieldPermissions(in.FieldPermissions); err != nil {
		return err
	}
	owner, err := i.store.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	tctx := forTenant(ctx, owner.TenantID)
	err = i.unit.Execute(tctx, func(tx db.Transactor, c *uow.Collector) error {
		s := i.storeFor(tx)
		acct, err := s.GetAccount(tctx, id)
		if err != nil {
			return err
		}
		// The shared role lock is held until this transaction commits, so a
		// concurrent DeleteRole cannot remove a role this account is being
		// given.
		if err := i.checkRolesExist(tctx, s, acct.TenantID, in.Roles); err != nil {
			return err
		}
		if err := s.SetAccountRoles(tctx, id, in.Roles, in.FieldPermissions, i.now()); err != nil {
			return err
		}
		c.RecordChange(activity.Change{
			Entity:   EntityServiceAccount,
			EntityID: id.String(),
			Action:   activity.ActionUpdated,
			Before:   roleAssignment{Roles: acct.Roles, FieldPermissions: acct.FieldPermissions},
			After:    roleAssignment{Roles: in.Roles, FieldPermissions: in.FieldPermissions},
		})
		return nil
	})
	if err != nil {
		return err
	}
	i.invalidate(id)
	return nil
}

// checkRolesExist refuses an assignment that names a role the tenant does not
// have.
//
// A missing role resolves to no scopes and no field permissions, so a typo
// would look exactly like a role that grants nothing. The operator would see
// the name on the account and believe the grant was in place. The check turns
// that into a 422 at the moment of the mistake.
//
// It reads the roles under a SHARED row lock, held until the caller's
// transaction ends. The check and the account write are then one atomic step
// against DeleteRole, which wants the EXCLUSIVE lock on the same rows.
func (i *Interactor) checkRolesExist(ctx context.Context, s Store, tenant string, roles []string) error {
	if len(roles) == 0 {
		return nil
	}
	existing, err := s.LockRolesShared(ctx, tenant, roles)
	if err != nil {
		return err
	}
	have := make(map[string]bool, len(existing))
	for _, name := range existing {
		have[name] = true
	}
	for _, name := range roles {
		if !have[name] {
			return domainerrors.NewNotFound("role", name)
		}
	}
	return nil
}

// validateFieldPermissions refuses a level that is not one of the three.
//
// An unrecognised level would rank below "none" at merge time and silently
// deny, so a typo would look like a deliberate restriction. Refusing it at
// write time makes the mistake visible to the person making it.
func validateFieldPermissions(perms map[string]string) error {
	for attr, level := range perms {
		switch level {
		case "none", "read", "write":
		default:
			return domainerrors.NewValidation(
				"field permission must be none, read or write",
				"attribute", attr, "level", level)
		}
	}
	return nil
}
