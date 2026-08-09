package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zkrebbekx/flexitype/client"
)

// coreAttributes are the product fields the projection promotes to their own
// column. Everything else a merchant declares on a subtype lands in the
// attributes JSONB, which is how one storefront renders heterogeneous
// schemas without knowing them in advance.
var coreAttributes = map[string]bool{
	"name": true, "description": true, "sku": true, "status": true,
	"price": true, "currency": true, "in_stock": true, "image": true,
}

// Projector rebuilds one catalog row from flexitype.
//
// It never applies the event's payload. On any event for entity E in tenant T
// it RE-READS E's whole value set with T's client and overwrites the row.
//
// That is what makes the projector idempotent and order-independent. A
// delivery is at-least-once and unordered, so the same event can arrive twice
// and a "price = 20" event can arrive after the "price = 25" that superseded
// it. Applying a payload would then write a stale price and keep it. Re-reading
// asks flexitype for the CURRENT truth, so every delivery converges on the
// same row whatever its order or count — the event is only a signal that
// something changed, never the data.
type Projector struct {
	store   *Store
	clients *clientCache
	schemas *schemaCache
	// entityLocks serializes projections of one entity. Two deliveries for
	// the same entity can be in flight at once; without this, the slower
	// read can land after the faster one and leave the older value in the
	// row. Re-reading makes the projector order-independent across
	// DELIVERIES, not across concurrent reads of the same entity.
	entityLocks *keyedMutex
}

// UseCrossTenantReader points every read at ONE credential that may read any
// tenant and write none, instead of one full read/write token per merchant.
//
// It is the single biggest security improvement available to a service shaped
// like this one: a projection re-reads entities and never writes them, so
// holding a write-capable credential for every tenant is a standing risk that
// buys nothing.
func (p *Projector) UseCrossTenantReader(token string) error {
	return p.clients.withReader(token)
}

// NewProjector wires a projector over a store.
func NewProjector(store *Store, baseURL string, httpTimeout time.Duration) *Projector {
	cache := newClientCache(store, baseURL, httpTimeout)
	return &Projector{
		store:       store,
		clients:     cache,
		schemas:     newSchemaCache(cache),
		entityLocks: newKeyedMutex(),
	}
}

// Project rebuilds the catalog row for one entity.
//
// An entity with no values left is not a product any more — a purge or a
// cascade removal looks exactly like that from outside — so the row is
// deleted. Deleting an absent row is a no-op, so this stays idempotent.
func (p *Projector) Project(ctx context.Context, tenant, typeID, entityID string) error {
	unlock := p.entityLocks.lock(tenant + "\x00" + entityID)
	defer unlock()

	merchant, ok, err := p.store.Merchant(ctx, tenant)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("project %s/%s: tenant is not an onboarded merchant", tenant, entityID)
	}

	c, err := p.clients.get(ctx, tenant)
	if err != nil {
		return err
	}

	values, err := c.Entities().Values(ctx, typeID, entityID)
	if err != nil {
		return fmt.Errorf("re-read %s/%s: %w", tenant, entityID, err)
	}
	if len(values) == 0 {
		return p.store.DeleteProduct(ctx, tenant, entityID)
	}

	schema, err := p.schemas.get(ctx, tenant, typeID)
	if err != nil {
		return err
	}
	// An attribute the merchant added after the schema was cached is unknown
	// here. Refresh once rather than projecting the new field as missing.
	for _, v := range values {
		if _, known := schema.attrName[v.AttributeDefinitionID]; !known {
			if schema, err = p.schemas.refresh(ctx, tenant, typeID); err != nil {
				return err
			}
			break
		}
	}

	row := buildProduct(merchant, typeID, entityID, schema, values)
	return p.store.UpsertProduct(ctx, row)
}

// buildProduct folds a value set into one projection row. It is pure, so the
// projection rules are testable without a database or a flexitype.
func buildProduct(m Merchant, typeID, entityID string, schema typeSchema, values []client.AttributeValue) Product {
	row := Product{
		Tenant:       m.Tenant,
		MerchantName: m.DisplayName,
		EntityID:     entityID,
		TypeID:       typeID,
		Subtype:      schema.internalName,
	}
	extra := map[string]json.RawMessage{}

	for _, v := range values {
		name, ok := schema.attrName[v.AttributeDefinitionID]
		if !ok {
			// An attribute this projector cannot name is still worth keeping,
			// keyed by id, rather than silently dropped.
			extra[v.AttributeDefinitionID] = v.Value
			continue
		}
		// A localizable or scopable attribute yields one value per scope. The
		// base (unscoped) value is the storefront's default rendering; every
		// scoped one is kept under "name@locale" so a localized front end can
		// still find it.
		if v.Locale != "" || v.Channel != "" {
			extra[scopedKey(name, v.Locale, v.Channel)] = v.Value
			continue
		}
		if !coreAttributes[name] {
			extra[name] = v.Value
			continue
		}
		applyCore(&row, name, v.Value)
	}

	if raw, err := json.Marshal(extra); err == nil {
		row.Attributes = raw
	} else {
		row.Attributes = json.RawMessage("{}")
	}
	return row
}

// applyCore writes one core attribute into its own column. A value that does
// not decode into the expected Go type is left at its zero value rather than
// failing the whole projection: one bad field must not stall a merchant's
// whole catalog.
func applyCore(row *Product, name string, raw json.RawMessage) {
	switch name {
	case "name":
		row.Name = decodeString(raw)
	case "description":
		row.Description = decodeString(raw)
	case "sku":
		row.SKU = decodeString(raw)
	case "status":
		row.Status = decodeString(raw)
	case "currency":
		row.Currency = decodeString(raw)
	case "price":
		// A decimal arrives as a JSON string ("19.99") so it keeps its exact
		// digits; the column is numeric, so it is passed through as text.
		if s := decodeString(raw); s != "" {
			row.Price = &s
		} else {
			var n json.Number
			if json.Unmarshal(raw, &n) == nil && n.String() != "" {
				s := n.String()
				row.Price = &s
			}
		}
	case "in_stock":
		var b bool
		if json.Unmarshal(raw, &b) == nil {
			row.InStock = &b
		}
	case "image":
		row.Image = raw
	}
}

func decodeString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// scopedKey names a scoped value in the attributes JSONB.
func scopedKey(name, locale, channel string) string {
	key := name
	if locale != "" {
		key += "@" + locale
	}
	if channel != "" {
		key += "#" + channel
	}
	return key
}

// typeSchema is the attribute id to internal name map for one flexitype type,
// plus that type's own internal name (the merchant's subtype).
type typeSchema struct {
	internalName string
	attrName     map[string]string
}

// schemaCache keeps each merchant type's attribute names for schemaTTL.
//
// Every delivery needs the map, and a merchant's schema changes far less often
// than its values, so reading it per event would multiply the read load on
// flexitype by the number of attributes on a product.
type schemaCache struct {
	clients *clientCache
	mu      sync.Mutex
	entries map[string]schemaEntry
	ttl     time.Duration
}

type schemaEntry struct {
	schema  typeSchema
	fetched time.Time
}

const schemaTTL = 30 * time.Second

func newSchemaCache(clients *clientCache) *schemaCache {
	return &schemaCache{clients: clients, entries: map[string]schemaEntry{}, ttl: schemaTTL}
}

func (s *schemaCache) get(ctx context.Context, tenant, typeID string) (typeSchema, error) {
	s.mu.Lock()
	entry, ok := s.entries[tenant+"\x00"+typeID]
	s.mu.Unlock()
	if ok && time.Since(entry.fetched) < s.ttl {
		return entry.schema, nil
	}
	return s.refresh(ctx, tenant, typeID)
}

// refresh re-reads one type's schema, whatever the cache says.
func (s *schemaCache) refresh(ctx context.Context, tenant, typeID string) (typeSchema, error) {
	c, err := s.clients.get(ctx, tenant)
	if err != nil {
		return typeSchema{}, err
	}
	def, err := c.Types().Get(ctx, typeID)
	if err != nil {
		return typeSchema{}, fmt.Errorf("read type %s: %w", typeID, err)
	}
	// EffectiveAttributes, not Attributes: a subtype's own list omits every
	// field it inherits from product, which is most of what the projection
	// promotes to a column.
	attrs, err := c.Types().EffectiveAttributes(ctx, typeID)
	if err != nil {
		return typeSchema{}, fmt.Errorf("read attributes of %s: %w", typeID, err)
	}
	schema := typeSchema{internalName: def.InternalName, attrName: make(map[string]string, len(attrs))}
	for _, a := range attrs {
		schema.attrName[a.Attribute.ID] = a.Attribute.InternalName
	}
	s.mu.Lock()
	s.entries[tenant+"\x00"+typeID] = schemaEntry{schema: schema, fetched: time.Now()}
	s.mu.Unlock()
	return schema, nil
}

// clientCache holds one flexitype client per merchant tenant.
//
// A token IS a tenant, so the storefront cannot read a merchant's data with
// anything but that merchant's own credential. One client per tenant also
// keeps one HTTP connection pool per tenant.
// clientCache resolves the flexitype client to read one merchant's tenant
// with.
//
// There are two ways to hold that credential, and the difference is the
// biggest security decision in this example:
//
//   - A CROSS-TENANT READER (readerToken, the read_any_tenant scope). ONE
//     credential that reads every tenant and writes none. The tenant travels
//     per request, so this service holds no merchant secret at all.
//   - One MERCHANT credential per tenant, read out of the merchant table.
//     Each is a full read/write token, so a leak of this service's database
//     hands over every merchant's catalog — for a projection that only ever
//     reads.
//
// The reader is used when it is configured, and the per-merchant tokens
// remain the fallback so the example still runs against a service that has no
// such account.
type clientCache struct {
	store   *Store
	baseURL string
	timeout time.Duration
	// reader is the cross-tenant credential, when the deployment has one.
	reader  *client.Client
	mu      sync.Mutex
	clients map[string]*client.Client
}

func newClientCache(store *Store, baseURL string, timeout time.Duration) *clientCache {
	return &clientCache{store: store, baseURL: baseURL, timeout: timeout, clients: map[string]*client.Client{}}
}

// withReader attaches a cross-tenant reader credential. With one, no merchant
// token is ever used for a read.
func (c *clientCache) withReader(token string) error {
	if token == "" {
		return nil
	}
	opts := []client.Option{client.WithToken(token), client.WithUserAgent("marketplace-storefront")}
	if c.timeout > 0 {
		opts = append(opts, client.WithHTTPClient(&http.Client{Timeout: c.timeout}))
	}
	reader, err := client.New(c.baseURL, opts...)
	if err != nil {
		return fmt.Errorf("build the cross-tenant reader: %w", err)
	}
	c.reader = reader
	return nil
}

func (c *clientCache) get(ctx context.Context, tenant string) (*client.Client, error) {
	// One credential, pointed at the tenant this read is for. It can read
	// every tenant and write none, so this service holds no merchant secret.
	if c.reader != nil {
		return c.reader.ForTenant(tenant), nil
	}

	c.mu.Lock()
	if cl, ok := c.clients[tenant]; ok {
		c.mu.Unlock()
		return cl, nil
	}
	c.mu.Unlock()

	merchant, ok, err := c.store.Merchant(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no merchant registered for tenant %q", tenant)
	}
	opts := []client.Option{client.WithToken(merchant.Token), client.WithUserAgent("marketplace-storefront")}
	if c.timeout > 0 {
		opts = append(opts, client.WithHTTPClient(&http.Client{Timeout: c.timeout}))
	}
	cl, err := client.New(c.baseURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("build client for tenant %q: %w", tenant, err)
	}
	c.mu.Lock()
	c.clients[tenant] = cl
	c.mu.Unlock()
	return cl, nil
}

// forget drops a cached client, so a rotated token is picked up on the next
// call rather than at the next restart.
func (c *clientCache) forget(tenant string) {
	c.mu.Lock()
	delete(c.clients, tenant)
	c.mu.Unlock()
}

// keyedMutex is a mutex per key, allocated on demand.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}

type keyedLock struct {
	mu       sync.Mutex
	refCount int
}

func newKeyedMutex() *keyedMutex { return &keyedMutex{locks: map[string]*keyedLock{}} }

// lock takes the lock for key and returns its release function. The entry is
// reference-counted and removed when the last holder releases it, so the map
// does not grow with every entity the storefront has ever seen.
func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	entry, ok := k.locks[key]
	if !ok {
		entry = &keyedLock{}
		k.locks[key] = entry
	}
	entry.refCount++
	k.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		k.mu.Lock()
		entry.refCount--
		if entry.refCount == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}

// entityKey identifies one entity across tenants.
type entityKey struct {
	Tenant   string
	TypeID   string
	EntityID string
}

func (e entityKey) String() string {
	return strings.Join([]string{e.Tenant, e.TypeID, e.EntityID}, "/")
}
