package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Store owns the platform's merchant directory.
//
// A merchant record binds a marketplace identity to a flexitype TENANT and to
// the service-account token that reaches it. The token is a bearer credential
// with full read and write access to that merchant's catalog.
//
// Keeping it in a table is what makes this example runnable in one command.
// A real deployment stores it in a secret manager (AWS Secrets Manager, GCP
// Secret Manager, Vault) and keeps only the secret's NAME in this row, so a
// database dump or a read-only SQL leak does not hand over every merchant's
// catalog. See README.md.
type Store struct {
	db     *sql.DB
	schema string
}

// NewStore binds a store to one Postgres schema. The schema is a code-level
// constant, never caller input: it is interpolated into DDL and queries, where
// a placeholder cannot be used.
func NewStore(db *sql.DB, schema string) *Store {
	return &Store{db: db, schema: schema}
}

// Merchant is one onboarded merchant.
//
// Token and WebhookSecret marshal to nothing. The merchant-facing API returns
// this struct directly, so the `json:"-"` tags are the mechanism that keeps a
// credential out of every response — not a rule each handler has to remember.
type Merchant struct {
	ID            string    `json:"id"`
	DisplayName   string    `json:"display_name"`
	Tenant        string    `json:"tenant"`
	Token         string    `json:"-"`
	WebhookSecret string    `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
}

const schemaDDL = `
CREATE SCHEMA IF NOT EXISTS %[1]s;

CREATE TABLE IF NOT EXISTS %[1]s.merchant (
    id             text PRIMARY KEY,
    display_name   text NOT NULL,
    tenant         text NOT NULL UNIQUE,
    token          text NOT NULL,
    webhook_secret text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);
`

// Migrate creates the schema and its table.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(schemaDDL, s.schema)); err != nil {
		return fmt.Errorf("migrate platform schema: %w", err)
	}
	return nil
}

// Save writes a merchant record. Onboarding is idempotent, so a second
// onboarding of the same merchant updates the row rather than failing.
func (s *Store) Save(ctx context.Context, m Merchant) error {
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s.merchant (id, display_name, tenant, token, webhook_secret)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET
			display_name   = EXCLUDED.display_name,
			tenant         = EXCLUDED.tenant,
			token          = EXCLUDED.token,
			webhook_secret = EXCLUDED.webhook_secret`, s.schema),
		m.ID, m.DisplayName, m.Tenant, m.Token, m.WebhookSecret)
	if err != nil {
		return fmt.Errorf("save merchant: %w", err)
	}
	return nil
}

// Get reads one merchant by its marketplace id.
func (s *Store) Get(ctx context.Context, id string) (Merchant, bool, error) {
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT id, display_name, tenant, token, webhook_secret, created_at
		FROM %s.merchant WHERE id = $1`, s.schema), id)
	m, err := scanMerchant(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Merchant{}, false, nil
	case err != nil:
		return Merchant{}, false, fmt.Errorf("read merchant: %w", err)
	}
	return m, true, nil
}

// List returns every merchant, newest last.
func (s *Store) List(ctx context.Context) ([]Merchant, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, display_name, tenant, token, webhook_secret, created_at
		FROM %s.merchant ORDER BY created_at, id`, s.schema))
	if err != nil {
		return nil, fmt.Errorf("list merchants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Merchant{}
	for rows.Next() {
		m, err := scanMerchant(rows)
		if err != nil {
			return nil, fmt.Errorf("scan merchant: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scanMerchant(sc scanner) (Merchant, error) {
	var m Merchant
	err := sc.Scan(&m.ID, &m.DisplayName, &m.Tenant, &m.Token, &m.WebhookSecret, &m.CreatedAt)
	return m, err
}
