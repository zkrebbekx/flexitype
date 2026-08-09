// Package serviceaccount implements machine-to-machine authentication for
// the standalone service. Accounts live in a JSON file (secrets stored as
// SHA-256 hashes); tokens are "ft_<account-id>_<secret>" bearer tokens.
package serviceaccount

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// Scope gates what an account may do.
type Scope string

// The supported scopes.
const (
	ScopeRead  Scope = "read"
	ScopeWrite Scope = "write"
	ScopeAdmin Scope = "admin"
)

// Account is one machine identity.
type Account struct {
	// ID is the stable identifier embedded in tokens.
	ID string `json:"id"`
	// Name is the human-readable label used in activity logs.
	Name string `json:"name"`
	// TenantID scopes every request the account makes.
	TenantID string `json:"tenant_id"`
	// Scopes list what the account may do.
	Scopes []Scope `json:"scopes"`
	// FieldPermissions restricts read/write on specific attributes by
	// internal name: "none" | "read" | "write". Unlisted attributes are
	// fully accessible; an admin-scoped account ignores this entirely.
	FieldPermissions map[string]string `json:"field_permissions,omitempty"`
	// SecretHash is hex(SHA-256(secret)).
	SecretHash string `json:"secret_hash"`
	// UnresolvedRoles names roles the account holds that no longer exist.
	//
	// A role that resolves to nothing contributes no field permissions, and
	// an empty permission map otherwise reads as "unrestricted" — so deleting
	// a role would silently grant every attribute to the accounts it
	// restricted. A principal carrying an unresolved role is therefore denied
	// field access outright (see accessFor), which fails closed on data while
	// leaving the credential usable so an operator can repair it.
	UnresolvedRoles []string `json:"unresolved_roles,omitempty"`
}

// HasScope reports whether the account holds the scope (admin implies all).
func (a Account) HasScope(s Scope) bool {
	for _, have := range a.Scopes {
		if have == s || have == ScopeAdmin {
			return true
		}
	}
	return false
}

// Tenant parses the account's tenant.
func (a Account) Tenant() valueobjects.TenantID {
	t, err := valueobjects.ParseTenantID(a.TenantID)
	if err != nil {
		return valueobjects.DefaultTenant
	}
	return t
}

// Authenticator resolves a bearer token to an account. Both the
// file-backed Store and a database-backed store satisfy it, so the auth
// middleware works the same over either.
type Authenticator interface {
	Authenticate(token string) (Account, error)
}

// AuthenticatorCtx is the context-aware form of Authenticator: it threads the
// request's context (cancellation, deadline, trace span) into the credential
// lookup — which matters for the database-backed store, whose Authenticate
// runs a per-request SQL query on the hottest path (before every handler).
// The auth middleware prefers it via a type assertion and falls back to
// Authenticate, so existing Authenticator implementations keep working (1.0
// compatibility).
type AuthenticatorCtx interface {
	AuthenticateCtx(ctx context.Context, token string) (Account, error)
}

// Invalidator drops cached authentication state for one account. A caching
// authenticator implements it; a plain store has nothing to drop and does not.
//
// The admin interactor calls it after rotating or revoking a secret, so the
// old credential stops working at once rather than at the end of the cache
// TTL. Callers type-assert, so an authenticator without a cache needs no
// change.
type Invalidator interface {
	Invalidate(accountID string)
}

// TenantInvalidator drops cached authentication state for every account in
// one tenant.
//
// A role edit or a tenant deactivation changes what many accounts may do at
// once, and the cache is keyed by token, so neither can be expressed as a
// per-account eviction. It is a separate interface, not a method added to
// Invalidator, so an embedder's existing Invalidator keeps compiling; callers
// type-assert for it.
type TenantInvalidator interface {
	InvalidateTenant(tenantID string)
}

// Store holds the configured accounts.
type Store struct {
	accounts map[string]Account
}

// NewStore builds a store from accounts.
func NewStore(accounts []Account) *Store {
	m := make(map[string]Account, len(accounts))
	for _, a := range accounts {
		m[a.ID] = a
	}
	return &Store{accounts: m}
}

// LoadFile reads a JSON array of accounts from disk.
func LoadFile(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read service accounts: %w", err)
	}
	var accounts []Account
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, fmt.Errorf("decode service accounts: %w", err)
	}
	for _, a := range accounts {
		if a.ID == "" || a.SecretHash == "" {
			return nil, fmt.Errorf("service account %q missing id or secret_hash", a.Name)
		}
	}
	return NewStore(accounts), nil
}

// TokenPrefix marks flexitype service-account tokens.
const TokenPrefix = "ft_"

// MintToken renders the bearer token for an account id + raw secret.
// Utility for provisioning tooling; the service itself never sees raw
// secrets at rest.
func MintToken(accountID, secret string) string {
	return TokenPrefix + accountID + "_" + secret
}

// HashSecret computes the stored hash for a raw secret.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// SplitToken parses "ft_<id>_<secret>" into its id and secret. Both must
// be non-empty; the id never contains an underscore (it is a ULID), so the
// split is on the first underscore and secrets may contain any character.
func SplitToken(token string) (id, secret string, err error) {
	rest, ok := strings.CutPrefix(token, TokenPrefix)
	if !ok {
		return "", "", fmt.Errorf("malformed token")
	}
	id, secret, ok = strings.Cut(rest, "_")
	if !ok || id == "" || secret == "" {
		return "", "", fmt.Errorf("malformed token")
	}
	return id, secret, nil
}

// VerifySecret compares a raw secret against a stored hash in constant
// time.
func VerifySecret(secret, storedHash string) error {
	if subtle.ConstantTimeCompare([]byte(HashSecret(secret)), []byte(storedHash)) != 1 {
		return fmt.Errorf("invalid credentials")
	}
	return nil
}

// VerifyOnlyTiming burns a hash comparison so an unknown account is
// timing-indistinguishable from a wrong secret, then reports the account as
// unknown.
func VerifyOnlyTiming(secret string) error {
	subtle.ConstantTimeCompare([]byte(HashSecret(secret)), []byte(HashSecret(secret)))
	return fmt.Errorf("unknown service account")
}

// Authenticate resolves a bearer token to its account using constant-time
// hash comparison.
func (s *Store) Authenticate(token string) (Account, error) {
	id, secret, err := SplitToken(token)
	if err != nil {
		return Account{}, err
	}
	account, exists := s.accounts[id]
	if !exists {
		return Account{}, VerifyOnlyTiming(secret)
	}
	if err := VerifySecret(secret, account.SecretHash); err != nil {
		return Account{}, err
	}
	return account, nil
}

// AuthenticateCtx satisfies AuthenticatorCtx. The file store is entirely
// in-memory, so the context is unused; the method exists so a file-backed
// deployment takes the same middleware path as a database-backed one.
func (s *Store) AuthenticateCtx(_ context.Context, token string) (Account, error) {
	return s.Authenticate(token)
}

// permissionRank orders the field-permission levels. An unknown or empty
// level ranks lowest, so a typo denies rather than grants.
var permissionRank = map[string]int{"none": 1, "read": 2, "write": 3}

// MorePermissive reports whether level a allows strictly more than b.
//
// Roles merge additively — holding two roles is holding what either allows —
// so a merge takes the most permissive level each role grants. An unknown
// level ranks below "none", which is the safe direction: a typo in a role
// definition must not out-rank a real grant.
func MorePermissive(a, b string) bool { return permissionRank[a] > permissionRank[b] }

// RoleGrant is one role's contribution to an account's permissions, as the
// resolver needs it: the storage layer supplies these, and the admin API
// builds the same shape to report what a principal can actually do.
type RoleGrant struct {
	Name             string
	Scopes           []Scope
	FieldPermissions map[string]string
}

// Resolve merges an account's roles into it and returns the effective
// account.
//
// The rules, in one place so the authentication path and the
// effective-permissions view cannot drift:
//
//   - Scopes UNION. A role grants; it never revokes. Holding two roles is
//     holding what either allows.
//   - Field permissions take the MOST PERMISSIVE level any role grants for
//     that attribute — the same additive rule as scopes.
//   - The account's OWN entry for an attribute wins over every role, so one
//     person can be given an exception without inventing a role for them.
//   - A name with no matching grant is recorded in UnresolvedRoles. It
//     contributes nothing, and an empty permission map otherwise reads as
//     "unrestricted", so the caller must be able to deny rather than permit.
//
// admin is never merged in from a role: UpsertRole refuses it, because it is
// a cross-tenant privilege that also voids the account's own field
// permissions, and it would be invisible in the account row. Resolve drops it
// defensively, so a row written before that rule — or edited directly in the
// database — cannot escalate either.
func Resolve(base Account, names []string, grants []RoleGrant) Account {
	byName := make(map[string]RoleGrant, len(grants))
	for _, g := range grants {
		byName[g.Name] = g
	}

	held := map[Scope]bool{}
	for _, sc := range base.Scopes {
		held[sc] = true
	}
	merged := map[string]string{}
	for _, name := range names {
		g, ok := byName[name]
		if !ok {
			base.UnresolvedRoles = append(base.UnresolvedRoles, name)
			continue
		}
		for _, sc := range g.Scopes {
			if sc == ScopeAdmin || held[sc] {
				continue
			}
			held[sc] = true
			base.Scopes = append(base.Scopes, sc)
		}
		for attr, level := range g.FieldPermissions {
			if MorePermissive(level, merged[attr]) {
				merged[attr] = level
			}
		}
	}
	for attr, level := range base.FieldPermissions {
		merged[attr] = level
	}
	if len(merged) > 0 {
		base.FieldPermissions = merged
	}
	return base
}
