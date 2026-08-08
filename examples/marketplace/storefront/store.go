package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Store owns the storefront's OWN database: the merchant directory and the
// denormalized catalog projection that shoppers query.
//
// The projection exists because flexitype has no cross-tenant query. A
// service-account token IS a tenant, so a shopper query that spans merchants
// cannot be expressed against flexitype at all. The storefront therefore
// keeps its own copy, fed by webhooks, and answers shoppers from it.
type Store struct {
	db     *sql.DB
	schema string
}

// NewStore binds a store to one Postgres schema. The schema is a code-level
// constant or a test-supplied name, never caller input: it is interpolated
// into DDL and queries, where a placeholder cannot be used.
func NewStore(db *sql.DB, schema string) *Store {
	return &Store{db: db, schema: schema}
}

// Merchant is one onboarded merchant, as the platform registered it.
//
// Token is that merchant's flexitype service-account token. The storefront
// needs one token per merchant because re-reading an entity is a tenant-scoped
// call; there is no read-only credential that spans tenants.
type Merchant struct {
	Tenant        string `json:"tenant"`
	DisplayName   string `json:"display_name"`
	Token         string `json:"-"`
	WebhookSecret string `json:"-"`
}

// Product is one row of the catalog projection.
type Product struct {
	Tenant       string          `json:"tenant"`
	MerchantName string          `json:"merchant"`
	EntityID     string          `json:"entity_id"`
	TypeID       string          `json:"-"`
	Subtype      string          `json:"subtype"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	SKU          string          `json:"sku"`
	Status       string          `json:"status"`
	Price        *string         `json:"price,omitempty"`
	Currency     string          `json:"currency"`
	InStock      *bool           `json:"in_stock,omitempty"`
	Image        json.RawMessage `json:"image,omitempty"`
	Attributes   json.RawMessage `json:"attributes"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// schemaDDL is the projection schema. It is applied on start and is safe to
// re-apply, so a redeploy needs no migration step.
const schemaDDL = `
CREATE SCHEMA IF NOT EXISTS %[1]s;

CREATE TABLE IF NOT EXISTS %[1]s.merchant (
    tenant         text PRIMARY KEY,
    display_name   text NOT NULL,
    token          text NOT NULL,
    webhook_secret text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS %[1]s.catalog_product (
    tenant      text NOT NULL,
    entity_id   text NOT NULL,
    type_id     text NOT NULL,
    subtype     text NOT NULL,
    name        text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    sku         text NOT NULL DEFAULT '',
    status      text NOT NULL DEFAULT '',
    price       numeric,
    currency    text NOT NULL DEFAULT '',
    in_stock    boolean,
    image       jsonb,
    attributes  jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    -- The search vector is a GENERATED column, so it can never drift from the
    -- text it indexes: there is no code path that writes name without
    -- rewriting the vector.
    search tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(sku, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'C')
    ) STORED,
    PRIMARY KEY (tenant, entity_id)
);

CREATE INDEX IF NOT EXISTS catalog_product_search_idx
    ON %[1]s.catalog_product USING gin (search);
-- Every shopper query is filtered to status = 'active', so the partial index
-- covers the whole read path and stays small: a merchant's drafts and archive
-- never enter it.
CREATE INDEX IF NOT EXISTS catalog_product_active_idx
    ON %[1]s.catalog_product (tenant, price) WHERE status = 'active';
`

// Migrate creates the schema and its tables.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(schemaDDL, s.schema))
	if err != nil {
		return fmt.Errorf("migrate storefront schema: %w", err)
	}
	return nil
}

// UpsertMerchant records a merchant the platform onboarded. Onboarding is
// idempotent, so this must be too.
func (s *Store) UpsertMerchant(ctx context.Context, m Merchant) error {
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s.merchant (tenant, display_name, token, webhook_secret)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant) DO UPDATE SET
			display_name   = EXCLUDED.display_name,
			token          = EXCLUDED.token,
			webhook_secret = EXCLUDED.webhook_secret`, s.schema),
		m.Tenant, m.DisplayName, m.Token, m.WebhookSecret)
	if err != nil {
		return fmt.Errorf("upsert merchant: %w", err)
	}
	return nil
}

// Merchant reads one merchant by tenant. It reports false when the tenant is
// unknown, which the webhook ingest treats as an unsigned request.
func (s *Store) Merchant(ctx context.Context, tenant string) (Merchant, bool, error) {
	var m Merchant
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT tenant, display_name, token, webhook_secret FROM %s.merchant WHERE tenant = $1`, s.schema),
		tenant).Scan(&m.Tenant, &m.DisplayName, &m.Token, &m.WebhookSecret)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Merchant{}, false, nil
	case err != nil:
		return Merchant{}, false, fmt.Errorf("read merchant: %w", err)
	}
	return m, true, nil
}

// Merchants lists every onboarded merchant, for the storefront's merchant
// filter.
func (s *Store) Merchants(ctx context.Context) ([]Merchant, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(
		`SELECT tenant, display_name, token, webhook_secret FROM %s.merchant ORDER BY display_name`, s.schema))
	if err != nil {
		return nil, fmt.Errorf("list merchants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Merchant
	for rows.Next() {
		var m Merchant
		if err := rows.Scan(&m.Tenant, &m.DisplayName, &m.Token, &m.WebhookSecret); err != nil {
			return nil, fmt.Errorf("scan merchant: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpsertProduct writes one projected product. It is a blind upsert of the
// whole row: the projector always carries the entity's CURRENT value set, so
// there is nothing to merge.
func (s *Store) UpsertProduct(ctx context.Context, p Product) error {
	// Marshalled JSON goes to the driver as a string, never as []byte.
	// A pooled connection negotiates binary_parameters=yes, and lib/pq then
	// sends a []byte parameter as a binary bytea; Postgres reads its first
	// byte as a jsonb version number and fails with "unsupported jsonb
	// version number 123" (123 is '{').
	attrs := "{}"
	if len(p.Attributes) > 0 {
		attrs = string(p.Attributes)
	}
	var image any
	if len(p.Image) > 0 {
		image = string(p.Image)
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s.catalog_product
			(tenant, entity_id, type_id, subtype, name, description, sku, status,
			 price, currency, in_stock, image, attributes, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::numeric,$10,$11,$12::jsonb,$13::jsonb, now())
		ON CONFLICT (tenant, entity_id) DO UPDATE SET
			type_id = EXCLUDED.type_id, subtype = EXCLUDED.subtype,
			name = EXCLUDED.name, description = EXCLUDED.description,
			sku = EXCLUDED.sku, status = EXCLUDED.status,
			price = EXCLUDED.price, currency = EXCLUDED.currency,
			in_stock = EXCLUDED.in_stock, image = EXCLUDED.image,
			attributes = EXCLUDED.attributes, updated_at = now()`, s.schema),
		p.Tenant, p.EntityID, p.TypeID, p.Subtype, p.Name, p.Description, p.SKU,
		p.Status, p.Price, p.Currency, p.InStock, image, attrs)
	if err != nil {
		return fmt.Errorf("upsert product: %w", err)
	}
	return nil
}

// DeleteProduct removes a projected product. The projector calls it when the
// entity has no values left in flexitype, which is what a removed product
// looks like from the outside.
func (s *Store) DeleteProduct(ctx context.Context, tenant, entityID string) error {
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(
		`DELETE FROM %s.catalog_product WHERE tenant = $1 AND entity_id = $2`, s.schema),
		tenant, entityID)
	if err != nil {
		return fmt.Errorf("delete product: %w", err)
	}
	return nil
}

// Filter is a shopper's search. Status is deliberately absent: see Search.
type Filter struct {
	Query    string
	Tenant   string
	MinPrice string
	MaxPrice string
	Limit    int
	Offset   int
}

// Search answers a shopper query over the projection.
//
// Every query is clamped to status = 'active'. A draft is work in progress and
// an archived product is withdrawn; neither is an offer, so neither is visible
// to a shopper on any path. The clamp lives here, in the one place that reads
// the catalog table, rather than in each handler — a handler that forgot it
// would leak a merchant's unreleased products.
func (s *Store) Search(ctx context.Context, f Filter) ([]Product, error) {
	var where []string
	var args []any
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	where = append(where, "p.status = 'active'")
	if f.Tenant != "" {
		add("p.tenant = $%d", f.Tenant)
	}
	if f.MinPrice != "" {
		add("p.price >= $%d::numeric", f.MinPrice)
	}
	if f.MaxPrice != "" {
		add("p.price <= $%d::numeric", f.MaxPrice)
	}
	order := "p.updated_at DESC"
	if f.Query != "" {
		add("p.search @@ websearch_to_tsquery('english', $%d)", f.Query)
		// Rank by match quality when there is a query; websearch_to_tsquery
		// is used rather than to_tsquery so a shopper's raw words never
		// produce a syntax error.
		order = fmt.Sprintf("ts_rank(p.search, websearch_to_tsquery('english', $%d)) DESC, p.updated_at DESC", len(args))
	}

	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	args = append(args, limit, f.Offset)

	query := fmt.Sprintf(`
		SELECT p.tenant, m.display_name, p.entity_id, p.type_id, p.subtype, p.name,
		       p.description, p.sku, p.status, p.price::text, p.currency, p.in_stock,
		       p.image, p.attributes, p.updated_at
		FROM %s.catalog_product p
		JOIN %s.merchant m ON m.tenant = p.tenant
		WHERE %s
		ORDER BY %s
		LIMIT $%d OFFSET $%d`,
		s.schema, s.schema, strings.Join(where, " AND "), order, len(args)-1, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search catalog: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Product{}
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get reads one product for the detail page. It applies the same active-only
// clamp as Search, so a shopper cannot reach a draft by guessing its id.
func (s *Store) Get(ctx context.Context, tenant, entityID string) (Product, bool, error) {
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT p.tenant, m.display_name, p.entity_id, p.type_id, p.subtype, p.name,
		       p.description, p.sku, p.status, p.price::text, p.currency, p.in_stock,
		       p.image, p.attributes, p.updated_at
		FROM %s.catalog_product p
		JOIN %s.merchant m ON m.tenant = p.tenant
		WHERE p.tenant = $1 AND p.entity_id = $2 AND p.status = 'active'`, s.schema, s.schema),
		tenant, entityID)
	p, err := scanProduct(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Product{}, false, nil
	case err != nil:
		return Product{}, false, err
	}
	return p, true, nil
}

// scanner is the shared shape of *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func scanProduct(sc scanner) (Product, error) {
	var p Product
	var price, image, attrs sql.NullString
	var inStock sql.NullBool
	err := sc.Scan(&p.Tenant, &p.MerchantName, &p.EntityID, &p.TypeID, &p.Subtype,
		&p.Name, &p.Description, &p.SKU, &p.Status, &price, &p.Currency, &inStock,
		&image, &attrs, &p.UpdatedAt)
	if err != nil {
		return Product{}, err
	}
	if price.Valid {
		v := price.String
		p.Price = &v
	}
	if inStock.Valid {
		v := inStock.Bool
		p.InStock = &v
	}
	if image.Valid {
		p.Image = json.RawMessage(image.String)
	}
	if attrs.Valid {
		p.Attributes = json.RawMessage(attrs.String)
	}
	return p, nil
}
