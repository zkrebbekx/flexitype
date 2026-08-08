package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zkrebbekx/flexitype/client"
)

// TemplateName is the curated schema bundle every merchant starts from. It
// lives in application/schema/templates/ecommerce.json and ships inside the
// flexitype binary.
//
// Applying a template — rather than sharing one schema — is what makes each
// merchant's `product` type its OWN. A merchant can rename a field, add a
// constraint or archive an attribute without touching any other merchant.
const TemplateName = "ecommerce"

// accountName is the service account the platform creates in each merchant
// tenant. One well-known name makes onboarding recognisable as idempotent.
const accountName = "marketplace-platform"

// subscriptionName is the storefront's webhook subscription inside a merchant
// tenant.
const subscriptionName = "marketplace-storefront"

// Onboarder provisions a merchant across flexitype and the storefront.
//
// Every step is idempotent, because onboarding is a multi-service sequence
// with no distributed transaction: a failure at step 4 leaves steps 1 to 3
// applied, and the only safe repair is to run the whole sequence again.
type Onboarder struct {
	store *Store
	// admin is an admin-scoped client. The provisioning control plane takes
	// the target tenant from the request, so ONE admin credential provisions
	// every merchant.
	admin *client.Client
	// newTenantClient builds a client bound to one merchant's token. It is a
	// field so a test can point it at its own service.
	newTenantClient func(token string) (*client.Client, error)

	storefront *StorefrontClient
	hookBase   string
	log        Logger
}

// NewOnboarder wires the onboarding usecase.
func NewOnboarder(store *Store, admin *client.Client, flexitypeURL string, storefront *StorefrontClient, hookBase string, log Logger) *Onboarder {
	return &Onboarder{
		store: store,
		admin: admin,
		newTenantClient: func(token string) (*client.Client, error) {
			return client.New(flexitypeURL, client.WithToken(token), client.WithUserAgent("marketplace-platform"))
		},
		storefront: storefront,
		hookBase:   strings.TrimRight(hookBase, "/"),
		log:        log,
	}
}

// OnboardInput asks for a new merchant.
type OnboardInput struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Tenant      string `json:"tenant"`
}

// Onboard provisions a merchant and returns its record.
//
// Running it twice with the same input must neither duplicate anything nor
// fail. Each step below therefore reads before it writes, or uses an operation
// that is idempotent by construction.
func (o *Onboarder) Onboard(ctx context.Context, in OnboardInput) (Merchant, error) {
	if in.ID == "" || in.DisplayName == "" {
		return Merchant{}, errors.New("id and display_name are required")
	}
	tenant := in.Tenant
	if tenant == "" {
		tenant = in.ID
	}

	existing, found, err := o.store.Get(ctx, in.ID)
	if err != nil {
		return Merchant{}, err
	}
	if found && existing.Tenant != tenant {
		// Re-pointing a merchant at another tenant would silently orphan its
		// whole catalog, so it is refused rather than applied.
		return Merchant{}, fmt.Errorf("merchant %q is already bound to tenant %q", in.ID, existing.Tenant)
	}

	// 1. The tenant. A conflict means it already exists, which is the
	//    idempotent outcome, not a failure.
	if _, err := o.admin.Admin().CreateTenant(ctx, tenant); err != nil && !errors.Is(err, client.ErrConflict) {
		return Merchant{}, fmt.Errorf("create tenant %q: %w", tenant, err)
	}

	// 2. The service account. flexitype returns a token ONCE, at creation, so
	//    a stored token is the only way to reuse an account. When the record
	//    is gone but the account is not, the account is rotated: that is the
	//    only supported way to obtain a working credential for it again.
	token := existing.Token
	if token == "" {
		token, err = o.ensureAccount(ctx, tenant)
		if err != nil {
			return Merchant{}, err
		}
	}

	tenantClient, err := o.newTenantClient(token)
	if err != nil {
		return Merchant{}, fmt.Errorf("build client for tenant %q: %w", tenant, err)
	}

	// 3. The starter schema. Import is idempotent — an object that is already
	//    present is skipped, never duplicated — so applying it again is safe.
	if _, err := tenantClient.Schema().ApplyTemplate(ctx, TemplateName); err != nil {
		return Merchant{}, fmt.Errorf("apply template %q to %q: %w", TemplateName, tenant, err)
	}

	// 4. The storefront's webhook subscription, inside the merchant's tenant.
	secret := existing.WebhookSecret
	if secret == "" {
		secret = newSecret()
	}
	if err := o.ensureSubscription(ctx, tenantClient, tenant, secret); err != nil {
		return Merchant{}, err
	}

	merchant := Merchant{
		ID: in.ID, DisplayName: in.DisplayName, Tenant: tenant,
		Token: token, WebhookSecret: secret,
	}
	if err := o.store.Save(ctx, merchant); err != nil {
		return Merchant{}, err
	}

	// 5. Hand the storefront the credential it needs to re-read this
	//    merchant's entities, then project everything that already exists.
	//    A merchant onboarded after its catalogue was imported has no events
	//    to replay, and the subscription only carries what happens next.
	if err := o.storefront.RegisterMerchant(ctx, merchant); err != nil {
		return Merchant{}, err
	}
	projected, err := o.storefront.Backfill(ctx, tenant)
	if err != nil {
		return Merchant{}, err
	}

	o.log.Info("merchant onboarded", "merchant", merchant.ID, "tenant", tenant, "projected", projected)
	return merchant, nil
}

// ensureAccount returns a usable token for the platform's account in tenant.
func (o *Onboarder) ensureAccount(ctx context.Context, tenant string) (string, error) {
	accounts, err := o.admin.Admin().ListServiceAccounts(ctx, tenant)
	if err != nil {
		return "", fmt.Errorf("list service accounts of %q: %w", tenant, err)
	}
	for _, a := range accounts {
		if a.Name != accountName {
			continue
		}
		// The account exists but this platform has no token for it. A
		// created token is shown once and never again, so rotating is the
		// only way back to a working credential. The old one stops working,
		// which is correct: nothing else holds it.
		rotated, err := o.admin.Admin().RotateServiceAccount(ctx, a.ID)
		if err != nil {
			return "", fmt.Errorf("rotate service account of %q: %w", tenant, err)
		}
		o.log.Info("rotated an existing service account with no stored token", "tenant", tenant)
		return rotated.Token, nil
	}

	created, err := o.admin.Admin().CreateServiceAccount(ctx, client.CreateServiceAccountInput{
		TenantName: tenant,
		Name:       accountName,
		Scopes:     []string{"read", "write"},
	})
	if err != nil {
		return "", fmt.Errorf("create service account in %q: %w", tenant, err)
	}
	return created.Token, nil
}

// ensureSubscription registers, or re-points, the storefront's webhook
// subscription in the merchant's tenant.
func (o *Onboarder) ensureSubscription(ctx context.Context, c *client.Client, tenant, secret string) error {
	// The tenant rides in the delivery PATH, so the storefront can pick the
	// right secret with one HMAC verification instead of trying every
	// merchant's. The path is untrusted: it only selects a secret, and the
	// storefront takes the tenant from the signed envelope.
	in := client.SubscriptionInput{
		Name:   subscriptionName,
		URL:    o.hookBase + "/" + tenant,
		Secret: secret,
		EventTypes: []string{
			"flexitype.attribute_value.set",
			"flexitype.attribute_value.updated",
			"flexitype.attribute_value.removed",
		},
	}
	existing, err := c.Webhooks().List(ctx)
	if err != nil {
		return fmt.Errorf("list webhook subscriptions of %q: %w", tenant, err)
	}
	for _, sub := range existing {
		if sub.Name != subscriptionName {
			continue
		}
		if sub.URL == in.URL {
			return nil // already pointing at this storefront
		}
		// The storefront moved. The subscription is replaced rather than
		// patched: client.WebhooksService.Update sends a `name` field that
		// PATCH /webhook-subscriptions/{id} rejects, so every Update call
		// fails with VALIDATION "invalid request body" (see README, "What the
		// API made awkward"). Replacing loses the delivery history of a
		// subscription that was pointing somewhere else anyway.
		if err := c.Webhooks().Delete(ctx, sub.ID); err != nil {
			return fmt.Errorf("replace webhook subscription of %q: %w", tenant, err)
		}
		break
	}
	if _, err := c.Webhooks().Create(ctx, in); err != nil {
		return fmt.Errorf("create webhook subscription in %q: %w", tenant, err)
	}
	return nil
}

// newSecret mints a webhook signing secret. crypto/rand, because the secret is
// the only thing that tells the storefront a delivery really came from this
// merchant's flexitype.
func newSecret() string {
	buf := make([]byte, 32)
	// crypto/rand.Read never returns an error on any supported platform; it
	// panics internally instead, so the process cannot proceed with a
	// predictable secret.
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// StorefrontClient talks to the storefront's internal API.
type StorefrontClient struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewStorefrontClient builds the internal client.
func NewStorefrontClient(baseURL, token string) *StorefrontClient {
	return &StorefrontClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// RegisterMerchant hands the storefront a merchant's credential.
func (s *StorefrontClient) RegisterMerchant(ctx context.Context, m Merchant) error {
	body, err := json.Marshal(map[string]string{
		"display_name":   m.DisplayName,
		"token":          m.Token,
		"webhook_secret": m.WebhookSecret,
	})
	if err != nil {
		return err
	}
	_, err = s.do(ctx, http.MethodPut, "/internal/merchants/"+m.Tenant, body)
	if err != nil {
		return fmt.Errorf("register merchant with the storefront: %w", err)
	}
	return nil
}

// Backfill asks the storefront to project everything a merchant already has,
// and reports how many products it projected.
func (s *StorefrontClient) Backfill(ctx context.Context, tenant string) (int, error) {
	raw, err := s.do(ctx, http.MethodPost, "/internal/merchants/"+tenant+"/backfill", nil)
	if err != nil {
		return 0, fmt.Errorf("backfill %q: %w", tenant, err)
	}
	var out struct {
		Projected int `json:"projected"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, fmt.Errorf("decode backfill result: %w", err)
	}
	return out.Projected, nil
}

func (s *StorefrontClient) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	var req *http.Request
	var err error
	if reader != nil {
		req, err = http.NewRequestWithContext(ctx, method, s.baseURL+path, reader)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, s.baseURL+path, nil)
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Internal-Token", s.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	out := &bytes.Buffer{}
	_, _ = out.ReadFrom(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The response body can echo the request, which carried a token, so
		// only the status is reported.
		return nil, fmt.Errorf("storefront %s %s: status %d", method, path, resp.StatusCode)
	}
	return out.Bytes(), nil
}
