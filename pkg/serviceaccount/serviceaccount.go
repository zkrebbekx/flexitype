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

// Authenticator resolves a bearer token to an account. Both the
// file-backed Store and a database-backed store satisfy it, so the auth
// middleware works the same over either.
type Authenticator interface {
	Authenticate(token string) (Account, error)
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
