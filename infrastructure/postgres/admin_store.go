package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/zkrebbekx/flexitype/application/admin"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// adminStore persists tenants and service accounts for the provisioning
// control plane.
type adminStore struct {
	q db.QueryExecer
}

// NewAdminStore builds the tenant/service-account store.
func NewAdminStore(q db.QueryExecer) admin.Store {
	return &adminStore{q: q}
}

type tenantRow struct {
	ID        ulid.ID   `db:"id"`
	Name      string    `db:"name"`
	Active    bool      `db:"active"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func (r tenantRow) toTenant() admin.Tenant {
	return admin.Tenant{ID: r.ID, Name: r.Name, Active: r.Active, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func (s *adminStore) CreateTenant(ctx context.Context, t admin.Tenant) error {
	_, err := s.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_tenant (id, name, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`),
		t.ID, t.Name, t.Active, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert tenant: %w", err)
	}
	return nil
}

func (s *adminStore) ListTenants(ctx context.Context) ([]admin.Tenant, error) {
	var rows []tenantRow
	if err := s.q.SelectContext(ctx, &rows,
		`SELECT id, name, active, created_at, updated_at FROM flexitype_tenant ORDER BY name`); err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	out := make([]admin.Tenant, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toTenant())
	}
	return out, nil
}

func (s *adminStore) GetTenantByName(ctx context.Context, name string) (admin.Tenant, error) {
	var row tenantRow
	err := s.q.GetContext(ctx, &row, bind(
		`SELECT id, name, active, created_at, updated_at FROM flexitype_tenant WHERE name = ?`), name)
	if isNoRows(err) {
		return admin.Tenant{}, domainerrors.NewNotFound("tenant", name)
	}
	if err != nil {
		return admin.Tenant{}, fmt.Errorf("get tenant: %w", err)
	}
	return row.toTenant(), nil
}

func (s *adminStore) SetTenantActive(ctx context.Context, name string, active bool, now time.Time) error {
	_, err := s.q.ExecContext(ctx, bind(
		`UPDATE flexitype_tenant SET active = ?, updated_at = ? WHERE name = ?`), active, now, name)
	if err != nil {
		return fmt.Errorf("set tenant active: %w", err)
	}
	return nil
}

type accountRow struct {
	ID        ulid.ID        `db:"id"`
	TenantID  string         `db:"tenant_id"`
	Name      string         `db:"name"`
	Scopes    pq.StringArray `db:"scopes"`
	Active    bool           `db:"active"`
	CreatedAt time.Time      `db:"created_at"`
	UpdatedAt time.Time      `db:"updated_at"`
}

func (r accountRow) toAccount() admin.ServiceAccount {
	scopes := make([]serviceaccount.Scope, 0, len(r.Scopes))
	for _, sc := range r.Scopes {
		scopes = append(scopes, serviceaccount.Scope(sc))
	}
	return admin.ServiceAccount{
		ID:        r.ID,
		TenantID:  r.TenantID,
		Name:      r.Name,
		Scopes:    scopes,
		Active:    r.Active,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

func (s *adminStore) CreateAccount(ctx context.Context, a admin.ServiceAccount, secretHash string) error {
	scopes := make(pq.StringArray, 0, len(a.Scopes))
	for _, sc := range a.Scopes {
		scopes = append(scopes, string(sc))
	}
	_, err := s.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_service_account
		   (id, tenant_id, name, secret_hash, scopes, active, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
		a.ID, a.TenantID, a.Name, secretHash, scopes, a.Active, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert service account: %w", err)
	}
	return nil
}

func (s *adminStore) ListAccounts(ctx context.Context, tenant string) ([]admin.ServiceAccount, error) {
	var rows []accountRow
	if err := s.q.SelectContext(ctx, &rows, bind(
		`SELECT id, tenant_id, name, scopes, active, created_at, updated_at
		 FROM flexitype_service_account WHERE tenant_id = ? ORDER BY name`), tenant); err != nil {
		return nil, fmt.Errorf("list service accounts: %w", err)
	}
	out := make([]admin.ServiceAccount, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toAccount())
	}
	return out, nil
}

func (s *adminStore) GetAccount(ctx context.Context, id ulid.ID) (admin.ServiceAccount, error) {
	var row accountRow
	err := s.q.GetContext(ctx, &row, bind(
		`SELECT id, tenant_id, name, scopes, active, created_at, updated_at
		 FROM flexitype_service_account WHERE id = ?`), id)
	if isNoRows(err) {
		return admin.ServiceAccount{}, domainerrors.NewNotFound("service_account", id.String())
	}
	if err != nil {
		return admin.ServiceAccount{}, fmt.Errorf("get service account: %w", err)
	}
	return row.toAccount(), nil
}

func (s *adminStore) UpdateSecret(ctx context.Context, id ulid.ID, secretHash string, now time.Time) error {
	_, err := s.q.ExecContext(ctx, bind(
		`UPDATE flexitype_service_account SET secret_hash = ?, updated_at = ? WHERE id = ?`),
		secretHash, now, id)
	if err != nil {
		return fmt.Errorf("update service-account secret: %w", err)
	}
	return nil
}

func (s *adminStore) SetAccountActive(ctx context.Context, id ulid.ID, active bool, now time.Time) error {
	_, err := s.q.ExecContext(ctx, bind(
		`UPDATE flexitype_service_account SET active = ?, updated_at = ? WHERE id = ?`), active, now, id)
	if err != nil {
		return fmt.Errorf("set service-account active: %w", err)
	}
	return nil
}

// --- database-backed authenticator ------------------------------------------

// AccountLookup resolves bearer tokens against the service-account table.
// It satisfies serviceaccount.Authenticator so the auth middleware works
// over the database exactly as over the file store.
type AccountLookup struct {
	q db.QueryExecer
}

// NewAccountLookup builds the DB-backed authenticator.
func NewAccountLookup(q db.QueryExecer) *AccountLookup {
	return &AccountLookup{q: q}
}

// Authenticate resolves a token to an account, verifying the secret hash in
// constant time and rejecting inactive accounts.
func (l *AccountLookup) Authenticate(token string) (serviceaccount.Account, error) {
	id, secret, err := serviceaccount.SplitToken(token)
	if err != nil {
		return serviceaccount.Account{}, err
	}

	var row struct {
		TenantID   string         `db:"tenant_id"`
		Name       string         `db:"name"`
		SecretHash string         `db:"secret_hash"`
		Scopes     pq.StringArray `db:"scopes"`
		Active     bool           `db:"active"`
	}
	err = l.q.GetContext(context.Background(), &row, bind(
		`SELECT tenant_id, name, secret_hash, scopes, active
		 FROM flexitype_service_account WHERE id = ?`), id)
	if isNoRows(err) {
		return serviceaccount.Account{}, serviceaccount.VerifyOnlyTiming(secret)
	}
	if err != nil {
		return serviceaccount.Account{}, fmt.Errorf("look up service account: %w", err)
	}

	acct := serviceaccount.Account{
		ID:         id,
		Name:       row.Name,
		TenantID:   row.TenantID,
		SecretHash: row.SecretHash,
	}
	for _, sc := range row.Scopes {
		acct.Scopes = append(acct.Scopes, serviceaccount.Scope(sc))
	}
	if err := serviceaccount.VerifySecret(secret, row.SecretHash); err != nil {
		return serviceaccount.Account{}, err
	}
	if !row.Active {
		return serviceaccount.Account{}, fmt.Errorf("service account is revoked")
	}
	return acct, nil
}
