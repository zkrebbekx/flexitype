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

	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)

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

	UpsertRole(ctx context.Context, r Role) error
	ListRoles(ctx context.Context, tenant string) ([]Role, error)
	DeleteRole(ctx context.Context, tenant, name string) error
}

// Interactor implements the provisioning usecases.
type Interactor struct {
	store Store
	// authCache, when set, is evicted after a rotation or a revocation so the
	// old credential stops working at once rather than at the end of the cache
	// TTL. Nil when no caching authenticator is configured.
	authCache serviceaccount.Invalidator
	now       func() time.Time
}

// NewInteractor wires the admin usecases.
func NewInteractor(store Store, opts ...Option) *Interactor {
	i := &Interactor{store: store, now: uow.UTCNow}
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

// invalidate drops an account's cached authentications, if a cache is wired.
func (i *Interactor) invalidate(id ulid.ID) {
	if i.authCache != nil {
		i.authCache.Invalidate(id.String())
	}
}

// CreateTenant provisions a new tenant.
func (i *Interactor) CreateTenant(ctx context.Context, name string) (*Tenant, error) {
	if !namePattern.MatchString(name) {
		return nil, domainerrors.NewValidation("tenant name must be lowercase alphanumeric with _ or -, 2-64 chars")
	}
	if _, err := i.store.GetTenantByName(ctx, name); err == nil {
		return nil, domainerrors.NewConflict("a tenant with this name already exists", "name", name)
	} else if !domainerrors.IsNotFound(err) {
		return nil, err
	}

	now := i.now()
	t := Tenant{ID: ulid.New(), Name: name, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := i.store.CreateTenant(ctx, t); err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTenants returns every provisioned tenant.
func (i *Interactor) ListTenants(ctx context.Context) ([]Tenant, error) {
	return i.store.ListTenants(ctx)
}

// SetTenantActive activates or deactivates a tenant. A deactivated tenant's
// accounts stay defined but their access is governed by their own active
// flag; deactivation is advisory metadata for the control plane.
func (i *Interactor) SetTenantActive(ctx context.Context, name string, active bool) error {
	if _, err := i.store.GetTenantByName(ctx, name); err != nil {
		return err
	}
	return i.store.SetTenantActive(ctx, name, active, i.now())
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
}

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
	tenant, err := i.store.GetTenantByName(ctx, in.TenantName)
	if err != nil {
		return nil, err
	}

	secret, err := generateSecret()
	if err != nil {
		return nil, err
	}
	now := i.now()
	if err := validateFieldPermissions(in.FieldPermissions); err != nil {
		return nil, err
	}
	if err := i.checkRolesExist(ctx, tenant.Name, in.Roles); err != nil {
		return nil, err
	}
	acct := ServiceAccount{
		ID:               ulid.New(),
		TenantID:         tenant.Name,
		Name:             in.Name,
		Scopes:           scopes,
		Roles:            in.Roles,
		FieldPermissions: in.FieldPermissions,
		Active:           true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := i.store.CreateAccount(ctx, acct, serviceaccount.HashSecret(secret)); err != nil {
		return nil, err
	}
	return &AccountWithToken{Account: acct, Token: serviceaccount.MintToken(acct.ID.String(), secret)}, nil
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
	acct, err := i.store.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	secret, err := generateSecret()
	if err != nil {
		return nil, err
	}
	if err := i.store.UpdateSecret(ctx, id, serviceaccount.HashSecret(secret), i.now()); err != nil {
		return nil, err
	}
	i.invalidate(id)
	return &AccountWithToken{Account: acct, Token: serviceaccount.MintToken(id.String(), secret)}, nil
}

// Revoke deactivates a service account. Its token stops working immediately
// when a cache is wired (see WithAuthCache), otherwise within the auth cache
// TTL.
func (i *Interactor) Revoke(ctx context.Context, rawID string) error {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	if _, err := i.store.GetAccount(ctx, id); err != nil {
		return err
	}
	if err := i.store.SetAccountActive(ctx, id, false, i.now()); err != nil {
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
	if err := validateFieldPermissions(in.FieldPermissions); err != nil {
		return nil, err
	}
	tenant, err := i.store.GetTenantByName(ctx, in.TenantName)
	if err != nil {
		return nil, err
	}
	now := i.now()
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
	if err := i.store.UpsertRole(ctx, role); err != nil {
		return nil, err
	}
	return &role, nil
}

// ListRoles returns a tenant's roles.
func (i *Interactor) ListRoles(ctx context.Context, tenant string) ([]Role, error) {
	return i.store.ListRoles(ctx, tenant)
}

// DeleteRole removes a role.
//
// Accounts holding it keep the name and simply resolve nothing for it, which
// is the safe direction: a deleted role removes grants rather than leaving
// accounts pointing at a permission set nobody can read.
func (i *Interactor) DeleteRole(ctx context.Context, tenant, name string) error {
	return i.store.DeleteRole(ctx, tenant, name)
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
	acct, err := i.store.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	if err := i.checkRolesExist(ctx, acct.TenantID, in.Roles); err != nil {
		return err
	}
	if err := i.store.SetAccountRoles(ctx, id, in.Roles, in.FieldPermissions, i.now()); err != nil {
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
func (i *Interactor) checkRolesExist(ctx context.Context, tenant string, roles []string) error {
	if len(roles) == 0 {
		return nil
	}
	existing, err := i.store.ListRoles(ctx, tenant)
	if err != nil {
		return err
	}
	have := make(map[string]bool, len(existing))
	for _, r := range existing {
		have[r.Name] = true
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
