// Package serviceaccount implements machine-to-machine authentication for
// the standalone service. Accounts live in a JSON file (secrets stored as
// SHA-256 hashes); tokens are "ft_<account-id>_<secret>" bearer tokens.
package serviceaccount

import (
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
	// SecretHash is hex(SHA-256(secret)).
	SecretHash string `json:"secret_hash"`
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

// Authenticate resolves a bearer token to its account using constant-time
// hash comparison.
func (s *Store) Authenticate(token string) (Account, error) {
	rest, ok := strings.CutPrefix(token, TokenPrefix)
	if !ok {
		return Account{}, fmt.Errorf("malformed token")
	}
	id, secret, ok := strings.Cut(rest, "_")
	if !ok || id == "" || secret == "" {
		return Account{}, fmt.Errorf("malformed token")
	}

	account, exists := s.accounts[id]
	if !exists {
		// Burn comparable time so unknown accounts are indistinguishable.
		subtle.ConstantTimeCompare([]byte(HashSecret(secret)), []byte(HashSecret(secret)))
		return Account{}, fmt.Errorf("unknown service account")
	}
	if subtle.ConstantTimeCompare([]byte(HashSecret(secret)), []byte(account.SecretHash)) != 1 {
		return Account{}, fmt.Errorf("invalid credentials")
	}
	return account, nil
}
