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
	ID        ulid.ID                `json:"id"`
	TenantID  string                 `json:"tenant_id"`
	Name      string                 `json:"name"`
	Scopes    []serviceaccount.Scope `json:"scopes"`
	Active    bool                   `json:"active"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
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
}

// Interactor implements the provisioning usecases.
type Interactor struct {
	store Store
	now   func() time.Time
}

// NewInteractor wires the admin usecases.
func NewInteractor(store Store) *Interactor {
	return &Interactor{store: store, now: time.Now}
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

	now := i.now().UTC()
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
	return i.store.SetTenantActive(ctx, name, active, i.now().UTC())
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
}

// CreateAccount provisions a service account under an existing tenant and
// returns its bearer token once.
func (i *Interactor) CreateAccount(ctx context.Context, in CreateAccountInput) (*AccountWithToken, error) {
	if !namePattern.MatchString(in.Name) {
		return nil, domainerrors.NewValidation("account name must be lowercase alphanumeric with _ or -, 2-64 chars")
	}
	scopes, err := parseScopes(in.Scopes)
	if err != nil {
		return nil, err
	}
	tenant, err := i.store.GetTenantByName(ctx, in.TenantName)
	if err != nil {
		return nil, err
	}

	secret, err := generateSecret()
	if err != nil {
		return nil, err
	}
	now := i.now().UTC()
	acct := ServiceAccount{
		ID:        ulid.New(),
		TenantID:  tenant.Name,
		Name:      in.Name,
		Scopes:    scopes,
		Active:    true,
		CreatedAt: now,
		UpdatedAt: now,
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
// once; the old secret stops working immediately.
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
	if err := i.store.UpdateSecret(ctx, id, serviceaccount.HashSecret(secret), i.now().UTC()); err != nil {
		return nil, err
	}
	return &AccountWithToken{Account: acct, Token: serviceaccount.MintToken(id.String(), secret)}, nil
}

// Revoke deactivates a service account; its token stops working within the
// auth cache TTL.
func (i *Interactor) Revoke(ctx context.Context, rawID string) error {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	if _, err := i.store.GetAccount(ctx, id); err != nil {
		return err
	}
	return i.store.SetAccountActive(ctx, id, false, i.now().UTC())
}

func parseScopes(raw []string) ([]serviceaccount.Scope, error) {
	if len(raw) == 0 {
		return nil, domainerrors.NewValidation("at least one scope is required")
	}
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
