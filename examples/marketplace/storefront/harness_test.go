package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/client"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
)

// testSchema is this suite's private Postgres schema. It is dropped and
// recreated per test, so one test never sees another's rows.
const testSchema = "mkt_storefront_test"

// quietLogger keeps expected error paths out of the test output.
func quietLogger() Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestStore returns a store over a freshly created private schema. It skips
// the test when no database is configured, exactly as the repository's other
// Postgres suites do.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("MARKETPLACE_TEST_DSN")
	if dsn == "" {
		dsn = os.Getenv("FLEXITYPE_TEST_DSN")
	}
	if dsn == "" {
		t.Skip("FLEXITYPE_TEST_DSN not set; skipping the storefront database tests")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("DROP SCHEMA IF EXISTS " + testSchema + " CASCADE"); err != nil {
		t.Fatalf("drop test schema: %v", err)
	}
	store := NewStore(db, testSchema)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}
	return store
}

// merchantAccount describes one tenant's service account in the test
// flexitype.
type merchantAccount struct {
	tenant string
	token  string
}

// newFlexitype boots a real flexitype behind httptest, with one service
// account per tenant.
//
// Real accounts rather than anonymous access: a token IS a tenant in
// flexitype, and tenant separation is the property these tests are about. An
// anonymous service would put every merchant in one tenant and prove nothing.
func newFlexitype(t *testing.T, tenants ...string) (string, map[string]merchantAccount) {
	t.Helper()
	accounts := make([]serviceaccount.Account, 0, len(tenants))
	out := map[string]merchantAccount{}
	for i, tenant := range tenants {
		id := "acct" + string(rune('a'+i))
		secret := "secret-" + tenant
		accounts = append(accounts, serviceaccount.Account{
			ID:         id,
			Name:       tenant,
			TenantID:   tenant,
			Scopes:     []serviceaccount.Scope{serviceaccount.ScopeRead, serviceaccount.ScopeWrite},
			SecretHash: serviceaccount.HashSecret(secret),
		})
		out[tenant] = merchantAccount{tenant: tenant, token: serviceaccount.MintToken(id, secret)}
	}
	svc := flexitype.NewInMemory()
	srv := httptest.NewServer(svc.APIHandler(flexitype.APIConfig{
		Accounts: serviceaccount.NewStore(accounts),
		// Signed media links, so a shopper's browser fetches an image straight
		// from flexitype instead of through the storefront.
		MediaURLSecret: "storefront-test-media-signing-secret-0123456789",
	}))
	t.Cleanup(srv.Close)
	return srv.URL, out
}

// seedMerchant applies the ecommerce template into a tenant and registers the
// merchant with the storefront, which is what onboarding does in production.
func seedMerchant(t *testing.T, store *Store, baseURL string, acct merchantAccount, displayName, secret string) *client.Client {
	t.Helper()
	c, err := client.New(baseURL, client.WithToken(acct.token))
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	if _, err := c.Schema().ApplyTemplate(context.Background(), "ecommerce"); err != nil {
		t.Fatalf("apply template to %s: %v", acct.tenant, err)
	}
	err = store.UpsertMerchant(context.Background(), Merchant{
		Tenant: acct.tenant, DisplayName: displayName, Token: acct.token, WebhookSecret: secret,
	})
	if err != nil {
		t.Fatalf("register merchant: %v", err)
	}
	return c
}

// subtype creates a subtype of product with one extra attribute, which is how
// a merchant extends the shared starter schema.
func subtype(t *testing.T, c *client.Client, internalName, displayName, attrName, dataType string) string {
	t.Helper()
	ctx := context.Background()
	parent := typeID(t, c, "product")
	created, err := c.Types().Create(ctx, client.CreateTypeInput{
		InternalName: internalName, DisplayName: displayName, ExtendsID: parent,
	})
	if err != nil {
		t.Fatalf("create subtype %s: %v", internalName, err)
	}
	_, err = c.Attributes().Create(ctx, client.CreateAttributeInput{
		TypeDefinitionID: created.ID, InternalName: attrName,
		DisplayName: attrName, DataType: dataType,
	})
	if err != nil {
		t.Fatalf("create attribute %s: %v", attrName, err)
	}
	return created.ID
}

// typeID resolves a type definition id from its internal name.
func typeID(t *testing.T, c *client.Client, internalName string) string {
	t.Helper()
	page, err := c.Types().List(context.Background(), client.ListTypesOptions{InternalNames: []string{internalName}})
	if err != nil {
		t.Fatalf("list types: %v", err)
	}
	for _, item := range page.Items {
		if item.InternalName == internalName {
			return item.ID
		}
	}
	t.Fatalf("no type %q", internalName)
	return ""
}

// attrID resolves an attribute id from a type's EFFECTIVE attributes, so an
// inherited field resolves as readily as an own one.
func attrID(t *testing.T, c *client.Client, typeDefID, name string) string {
	t.Helper()
	attrs, err := c.Types().EffectiveAttributes(context.Background(), typeDefID)
	if err != nil {
		t.Fatalf("effective attributes: %v", err)
	}
	for _, a := range attrs {
		if a.Attribute.InternalName == name {
			return a.Attribute.ID
		}
	}
	t.Fatalf("no attribute %q on type %s", name, typeDefID)
	return ""
}

// writeProduct writes a whole product in one batch, keyed by attribute name.
func writeProduct(t *testing.T, c *client.Client, typeDefID, entityID string, fields map[string]any) {
	t.Helper()
	batch := make([]client.SetValueInput, 0, len(fields))
	for name, value := range fields {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		batch = append(batch, client.SetValueInput{
			AttributeDefinitionID: attrID(t, c, typeDefID, name),
			EntityID:              entityID,
			TypeDefinitionID:      typeDefID,
			Value:                 raw,
		})
	}
	if _, err := c.Values().SetBatch(context.Background(), batch); err != nil {
		t.Fatalf("write product %s: %v", entityID, err)
	}
}

// addAttribute declares one more attribute on an existing type.
func addAttribute(t *testing.T, c *client.Client, typeDefID, name, dataType string) {
	t.Helper()
	_, err := c.Attributes().Create(context.Background(), client.CreateAttributeInput{
		TypeDefinitionID: typeDefID, InternalName: name, DisplayName: name, DataType: dataType,
	})
	if err != nil {
		t.Fatalf("create attribute %s: %v", name, err)
	}
}

// jsonBody wraps a literal JSON request body.
func jsonBody(s string) io.Reader { return strings.NewReader(s) }
