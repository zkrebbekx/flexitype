package postgres

import (
	"context"
	"encoding/json"
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

// WithTx binds the store to an open transaction, so a provisioning usecase's
// reads, its writes, its row locks and its activity entry share one
// transaction.
func (s *adminStore) WithTx(tx db.Tx) admin.Store {
	return &adminStore{q: txExecer(tx)}
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
	ID         ulid.ID        `db:"id"`
	TenantID   string         `db:"tenant_id"`
	Name       string         `db:"name"`
	Scopes     pq.StringArray `db:"scopes"`
	Roles      pq.StringArray `db:"roles"`
	FieldPerms []byte         `db:"field_permissions"`
	Active     bool           `db:"active"`
	CreatedAt  time.Time      `db:"created_at"`
	UpdatedAt  time.Time      `db:"updated_at"`
}

// toAccount reports the account as stored, not as resolved: Roles holds the
// names, and FieldPermissions holds only this account's own overrides. The
// role merge happens at authentication (see applyRoles). An operator reading
// this list therefore sees what was assigned, which is what they can edit.
func (r accountRow) toAccount() (admin.ServiceAccount, error) {
	scopes := make([]serviceaccount.Scope, 0, len(r.Scopes))
	for _, sc := range r.Scopes {
		scopes = append(scopes, serviceaccount.Scope(sc))
	}
	acct := admin.ServiceAccount{
		ID:        r.ID,
		TenantID:  r.TenantID,
		Name:      r.Name,
		Scopes:    scopes,
		Roles:     []string(r.Roles),
		Active:    r.Active,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
	if err := json.Unmarshal(nonEmptyJSON(r.FieldPerms), &acct.FieldPermissions); err != nil {
		return admin.ServiceAccount{}, fmt.Errorf("decode account %q field permissions: %w", r.Name, err)
	}
	return acct, nil
}

func (s *adminStore) CreateAccount(ctx context.Context, a admin.ServiceAccount, secretHash string) error {
	scopes := make(pq.StringArray, 0, len(a.Scopes))
	for _, sc := range a.Scopes {
		scopes = append(scopes, string(sc))
	}
	perms, err := json.Marshal(nonNilPerms(a.FieldPermissions))
	if err != nil {
		return fmt.Errorf("encode field permissions: %w", err)
	}
	_, err = s.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_service_account
		   (id, tenant_id, name, secret_hash, scopes, roles, field_permissions,
		    active, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		a.ID, a.TenantID, a.Name, secretHash, scopes,
		pq.Array(nonNilRoles(a.Roles)), jsonbParam(perms),
		a.Active, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert service account: %w", err)
	}
	return nil
}

// SetAccountRoles replaces an account's roles and its own field-permission
// overrides in one statement, so a caller never observes half a change.
func (s *adminStore) SetAccountRoles(ctx context.Context, id ulid.ID, roles []string, perms map[string]string, now time.Time) error {
	raw, err := json.Marshal(nonNilPerms(perms))
	if err != nil {
		return fmt.Errorf("encode field permissions: %w", err)
	}
	res, err := s.q.ExecContext(ctx, bind(
		`UPDATE flexitype_service_account
		    SET roles = ?, field_permissions = ?, updated_at = ?
		  WHERE id = ?`),
		pq.Array(nonNilRoles(roles)), jsonbParam(raw), now, id)
	if err != nil {
		return fmt.Errorf("set account roles: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set account roles: %w", err)
	}
	if n == 0 {
		return domainerrors.NewNotFound("service_account", id.String())
	}
	return nil
}

// UpsertRole creates or replaces a role by (tenant, name) and returns the
// PERSISTED row.
//
// An update keeps the existing id and created_at — they are deliberately not
// in the SET list — so RETURNING is the only way the caller learns what is
// stored rather than what it proposed.
func (s *adminStore) UpsertRole(ctx context.Context, r admin.Role) (admin.Role, error) {
	raw, err := json.Marshal(nonNilPerms(r.FieldPermissions))
	if err != nil {
		return admin.Role{}, fmt.Errorf("encode field permissions: %w", err)
	}
	scopes := make(pq.StringArray, 0, len(r.Scopes))
	for _, sc := range r.Scopes {
		scopes = append(scopes, string(sc))
	}
	var out struct {
		ID        ulid.ID   `db:"id"`
		CreatedAt time.Time `db:"created_at"`
	}
	err = s.q.GetContext(ctx, &out, bind(
		`INSERT INTO flexitype_role
		   (id, tenant_id, name, description, scopes, field_permissions, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (tenant_id, name) DO UPDATE SET
		   description = EXCLUDED.description, scopes = EXCLUDED.scopes,
		   field_permissions = EXCLUDED.field_permissions,
		   updated_at = EXCLUDED.updated_at
		 RETURNING id, created_at`),
		r.ID, r.TenantID, r.Name, r.Description, scopes, jsonbParam(raw), r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return admin.Role{}, fmt.Errorf("upsert role: %w", err)
	}
	r.ID, r.CreatedAt = out.ID, out.CreatedAt
	return r, nil
}

// CountAccountsWithRole reports how many of the tenant's accounts name the
// role, so a delete that would strand them can be refused.
func (s *adminStore) CountAccountsWithRole(ctx context.Context, tenant, name string) (int, error) {
	var n int
	if err := s.q.GetContext(ctx, &n, bind(
		`SELECT COUNT(*) FROM flexitype_service_account
		  WHERE tenant_id = ? AND ? = ANY(roles)`), tenant, name); err != nil {
		return 0, fmt.Errorf("count accounts with role: %w", err)
	}
	return n, nil
}

// roleColumns is the role projection every role read shares.
const roleColumns = `id, tenant_id, name, description, scopes, field_permissions, created_at, updated_at`

type roleRow struct {
	ID          ulid.ID        `db:"id"`
	TenantID    string         `db:"tenant_id"`
	Name        string         `db:"name"`
	Description string         `db:"description"`
	Scopes      pq.StringArray `db:"scopes"`
	FieldPerms  []byte         `db:"field_permissions"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
}

func (r roleRow) toRole() (admin.Role, error) {
	role := admin.Role{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Description: r.Description,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	for _, sc := range r.Scopes {
		role.Scopes = append(role.Scopes, serviceaccount.Scope(sc))
	}
	if err := json.Unmarshal(nonEmptyJSON(r.FieldPerms), &role.FieldPermissions); err != nil {
		return admin.Role{}, fmt.Errorf("decode role %q field permissions: %w", r.Name, err)
	}
	return role, nil
}

// ListRoles returns a tenant's roles, by name.
func (s *adminStore) ListRoles(ctx context.Context, tenant string) ([]admin.Role, error) {
	var rows []roleRow
	if err := s.q.SelectContext(ctx, &rows, bind(
		`SELECT `+roleColumns+`
		   FROM flexitype_role WHERE tenant_id = ? ORDER BY name`), tenant); err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	out := make([]admin.Role, 0, len(rows))
	for _, r := range rows {
		role, err := r.toRole()
		if err != nil {
			return nil, err
		}
		out = append(out, role)
	}
	return out, nil
}

// LockRole reads one role and holds an EXCLUSIVE row lock on it until the
// transaction ends.
//
// DeleteRole takes it before it counts the accounts that hold the role. The
// count and the delete were two unlocked statements, so an assignment could
// commit between them and leave an account naming a role that no longer
// exists — the unresolved-role state the count exists to prevent.
//
// The lock conflicts with the SHARED lock LockRolesShared takes, and with
// nothing else. It therefore serializes exactly the racing pair, one role row
// at a time; readers of the role (authentication, the listing) are untouched.
func (s *adminStore) LockRole(ctx context.Context, tenant, name string) (admin.Role, error) {
	var row roleRow
	err := s.q.GetContext(ctx, &row, bind(
		`SELECT `+roleColumns+`
		   FROM flexitype_role WHERE tenant_id = ? AND name = ? FOR UPDATE`), tenant, name)
	if isNoRows(err) {
		return admin.Role{}, domainerrors.NewNotFound("role", name)
	}
	if err != nil {
		return admin.Role{}, fmt.Errorf("lock role: %w", err)
	}
	return row.toRole()
}

// LockRolesShared reports which of names the tenant has, holding a SHARED row
// lock on each until the transaction ends.
//
// An assignment takes it before it writes the account row. A DeleteRole for
// one of those roles wants the EXCLUSIVE lock, so it waits for this
// transaction to commit and then counts this account as a holder. In the
// other order, the delete commits first and the name no longer resolves here,
// which the caller reports as a 404.
//
// Rows are locked in name order, so two assignments naming overlapping role
// sets cannot deadlock. Shared locks do not conflict with each other, so
// concurrent assignments still run in parallel.
func (s *adminStore) LockRolesShared(ctx context.Context, tenant string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	var out []string
	if err := s.q.SelectContext(ctx, &out, bind(
		`SELECT name FROM flexitype_role
		  WHERE tenant_id = ? AND name = ANY(?) ORDER BY name FOR SHARE`),
		tenant, pq.Array(names)); err != nil {
		return nil, fmt.Errorf("lock roles: %w", err)
	}
	return out, nil
}

// DeleteRole removes a role. Accounts naming it simply resolve nothing for
// it, which removes grants rather than leaving them pointing at a permission
// set nobody can read.
func (s *adminStore) DeleteRole(ctx context.Context, tenant, name string) error {
	res, err := s.q.ExecContext(ctx, bind(
		`DELETE FROM flexitype_role WHERE tenant_id = ? AND name = ?`), tenant, name)
	if err != nil {
		return fmt.Errorf("delete role: %w", err)
	}
	if n, aerr := res.RowsAffected(); aerr == nil && n == 0 {
		return domainerrors.NewNotFound("role", name)
	}
	return nil
}

// nonNilPerms and nonNilRoles keep a nil map/slice out of the column, so a
// cleared assignment stores an empty value rather than SQL NULL.
func nonNilPerms(p map[string]string) map[string]string {
	if p == nil {
		return map[string]string{}
	}
	return p
}

func nonNilRoles(r []string) []string {
	if r == nil {
		return []string{}
	}
	return r
}

func (s *adminStore) ListAccounts(ctx context.Context, tenant string) ([]admin.ServiceAccount, error) {
	var rows []accountRow
	if err := s.q.SelectContext(ctx, &rows, bind(
		`SELECT id, tenant_id, name, scopes, roles, field_permissions,
		        active, created_at, updated_at
		 FROM flexitype_service_account WHERE tenant_id = ? ORDER BY name`), tenant); err != nil {
		return nil, fmt.Errorf("list service accounts: %w", err)
	}
	out := make([]admin.ServiceAccount, 0, len(rows))
	for _, r := range rows {
		acct, err := r.toAccount()
		if err != nil {
			return nil, err
		}
		out = append(out, acct)
	}
	return out, nil
}

func (s *adminStore) GetAccount(ctx context.Context, id ulid.ID) (admin.ServiceAccount, error) {
	var row accountRow
	err := s.q.GetContext(ctx, &row, bind(
		`SELECT id, tenant_id, name, scopes, roles, field_permissions,
		        active, created_at, updated_at
		 FROM flexitype_service_account WHERE id = ?`), id)
	if isNoRows(err) {
		return admin.ServiceAccount{}, domainerrors.NewNotFound("service_account", id.String())
	}
	if err != nil {
		return admin.ServiceAccount{}, fmt.Errorf("get service account: %w", err)
	}
	return row.toAccount()
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

// Authenticate resolves a token with a background context. The auth middleware
// prefers AuthenticateCtx (via the AuthenticatorCtx assertion) so the request
// context — cancellation, deadline, trace — reaches this per-request query.
func (l *AccountLookup) Authenticate(token string) (serviceaccount.Account, error) {
	return l.AuthenticateCtx(context.Background(), token)
}

// AuthenticateCtx resolves a token to an account, verifying the secret hash in
// constant time and rejecting inactive accounts, under the caller's context.
func (l *AccountLookup) AuthenticateCtx(ctx context.Context, token string) (serviceaccount.Account, error) {
	id, secret, err := serviceaccount.SplitToken(token)
	if err != nil {
		return serviceaccount.Account{}, err
	}

	var row struct {
		TenantID     string         `db:"tenant_id"`
		Name         string         `db:"name"`
		SecretHash   string         `db:"secret_hash"`
		Scopes       pq.StringArray `db:"scopes"`
		Active       bool           `db:"active"`
		TenantActive bool           `db:"tenant_active"`
		Roles        pq.StringArray `db:"roles"`
		FieldPerms   []byte         `db:"field_permissions"`
	}
	// The tenant's own active flag is joined in, so deactivating a tenant
	// actually suspends it. Consulting only the account flag meant
	// PATCH /api/v1/tenants/{name} {"active": false} — the action an operator
	// takes during an incident, an offboarding or a non-payment suspension —
	// left every service account under that tenant reading and writing, while
	// the control plane reported the tenant as inactive.
	err = l.q.GetContext(ctx, &row, bind(
		`SELECT a.tenant_id, a.name, a.secret_hash, a.scopes, a.active,
		        a.roles, a.field_permissions,
		        COALESCE(t.active, true) AS tenant_active
		   FROM flexitype_service_account a
		   LEFT JOIN flexitype_tenant t ON t.name = a.tenant_id
		  WHERE a.id = ?`), id)
	if isNoRows(err) {
		return serviceaccount.Account{}, serviceaccount.VerifyOnlyTiming(secret)
	}
	if err != nil {
		return serviceaccount.Account{}, fmt.Errorf("look up service account: %w", err)
	}

	// Verify the secret BEFORE assembling any authorization state. Resolving
	// roles first meant an attacker who knew an account id — they appear in
	// provisioning responses, activity logs and the console — spent two
	// unbounded database round trips per garbage secret instead of one, and a
	// known id with roles answered measurably slower than an unknown id,
	// which returns after VerifyOnlyTiming with no role query at all. That
	// timing difference is an account-id oracle and the extra work is
	// attacker-controlled.
	if err := serviceaccount.VerifySecret(secret, row.SecretHash); err != nil {
		return serviceaccount.Account{}, err
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
	if err := json.Unmarshal(nonEmptyJSON(row.FieldPerms), &acct.FieldPermissions); err != nil {
		return serviceaccount.Account{}, fmt.Errorf("decode field permissions: %w", err)
	}
	// Roles are resolved here, at authentication, so a permission change to a
	// role takes effect for every account holding it as soon as the auth
	// cache expires — which is the whole reason the indirection exists.
	if len(row.Roles) > 0 {
		if err := l.applyRoles(ctx, &acct, row.Roles); err != nil {
			return serviceaccount.Account{}, err
		}
	}
	if !row.Active {
		return serviceaccount.Account{}, fmt.Errorf("service account is revoked")
	}
	if !row.TenantActive {
		return serviceaccount.Account{}, fmt.Errorf("tenant is deactivated")
	}
	return acct, nil
}

// nonEmptyJSON substitutes an empty object for a null or empty column, so a
// row written before the column existed decodes rather than erroring.
func nonEmptyJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte(`{}`)
	}
	return raw
}

// applyRoles resolves the named roles and merges them into the account.
//
// The merge rules live in serviceaccount.Resolve, so this path and the
// effective-permissions view cannot drift apart.
func (l *AccountLookup) applyRoles(ctx context.Context, acct *serviceaccount.Account, roles []string) error {
	var rows []struct {
		Name       string         `db:"name"`
		Scopes     pq.StringArray `db:"scopes"`
		FieldPerms []byte         `db:"field_permissions"`
	}
	if err := l.q.SelectContext(ctx, &rows, bind(
		`SELECT name, scopes, field_permissions
		   FROM flexitype_role
		  WHERE tenant_id = ? AND name = ANY(?)`),
		acct.TenantID, pq.Array(roles)); err != nil {
		return fmt.Errorf("resolve roles: %w", err)
	}

	grants := make([]serviceaccount.RoleGrant, 0, len(rows))
	for _, r := range rows {
		g := serviceaccount.RoleGrant{Name: r.Name}
		for _, sc := range r.Scopes {
			g.Scopes = append(g.Scopes, serviceaccount.Scope(sc))
		}
		if err := json.Unmarshal(nonEmptyJSON(r.FieldPerms), &g.FieldPermissions); err != nil {
			return fmt.Errorf("decode role %q field permissions: %w", r.Name, err)
		}
		grants = append(grants, g)
	}
	*acct = serviceaccount.Resolve(*acct, roles, grants)
	return nil
}
