// Package client is the first-party Go client for a standalone flexitype
// service. It mirrors the embedded usecase surface — Types, Attributes,
// Values, Query, Relationships and the rest — over the versioned REST API, so
// code that talks to a remote flexitype reads almost the same as code that
// embeds it.
//
//	c, _ := client.New("https://flexitype.internal", client.WithToken(tok))
//	prod, err := c.Types().Create(ctx, client.CreateTypeInput{InternalName: "product", DisplayName: "Product"})
//	for row, err := range c.Query(ctx, "product", `price > 10`) { ... }
//
// The client depends only on the standard library.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// Client talks to a standalone flexitype service's REST API. It is safe for
// concurrent use.
type Client struct {
	base      string // service base including /api/v1, no trailing slash
	http      *http.Client
	token     string
	userAgent string
}

// Option configures a Client.
type Option func(*Client)

// WithToken sets the service-account bearer token
// (ft_<account>_<secret>). Omit it only against a development service with
// authentication disabled.
func WithToken(token string) Option {
	return func(c *Client) { c.token = token }
}

// WithHTTPClient supplies a custom *http.Client (timeouts, transport, tracing).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// WithUserAgent overrides the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.userAgent = ua }
}

// New builds a client for the service at baseURL (its root, e.g.
// "https://flexitype.internal:8080"); the "/api/v1" prefix is added
// automatically.
func New(baseURL string, opts ...Option) (*Client, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("flexitype client: base URL is required")
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("flexitype client: invalid base URL: %w", err)
	}
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(base, "/api/v1") {
		base += "/api/v1"
	}
	c := &Client{base: base, http: http.DefaultClient, userAgent: "flexitype-go-client"}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Resource accessors mirror the embedded interactor groups.

// Types returns the type-definition operations.
func (c *Client) Types() *TypesService { return &TypesService{c} }

// Attributes returns the attribute-definition operations.
func (c *Client) Attributes() *AttributesService { return &AttributesService{c} }

// Values returns the value operations.
func (c *Client) Values() *ValuesService { return &ValuesService{c} }

// Entities returns the entity-level operations (browse, grid, import/export,
// media, revisions).
func (c *Client) Entities() *EntitiesService { return &EntitiesService{c} }

// Revisions returns the entity-revision operations.
func (c *Client) Revisions() *RevisionsService { return &RevisionsService{c} }

// Dependencies returns the attribute-dependency operations.
func (c *Client) Dependencies() *DependenciesService { return &DependenciesService{c} }

// UnitFamilies returns the quantity unit-family operations.
func (c *Client) UnitFamilies() *UnitFamiliesService { return &UnitFamiliesService{c} }

// SavedViews returns the saved-view operations.
func (c *Client) SavedViews() *SavedViewsService { return &SavedViewsService{c} }

// ChangeSets returns the change-management operations.
func (c *Client) ChangeSets() *ChangeSetsService { return &ChangeSetsService{c} }

// Schema returns schema import/export and template operations.
func (c *Client) Schema() *SchemaService { return &SchemaService{c} }

// RelationshipDefinitions returns relationship-definition operations.
func (c *Client) RelationshipDefinitions() *RelationshipDefinitionsService {
	return &RelationshipDefinitionsService{c}
}

// Relationships returns relationship (link) operations.
func (c *Client) Relationships() *RelationshipsService { return &RelationshipsService{c} }

// MatchRules returns duplicate-detection operations.
func (c *Client) MatchRules() *MatchRulesService { return &MatchRulesService{c} }

// Activity returns audit-log operations.
func (c *Client) Activity() *ActivityService { return &ActivityService{c} }

// Webhooks returns webhook-subscription and delivery operations.
func (c *Client) Webhooks() *WebhooksService { return &WebhooksService{c} }

// Events returns event-feed operations.
func (c *Client) Events() *EventsService { return &EventsService{c} }

// Admin returns tenant/service-account provisioning operations.
func (c *Client) Admin() *AdminService { return &AdminService{c} }

// Features fetches the deployment's enabled capabilities.
func (c *Client) Features(ctx context.Context) (*Features, error) {
	var out Features
	if err := c.do(ctx, http.MethodGet, "/features", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Reindex rebuilds the search projection for the caller's tenant, returning the
// number of entities reindexed. Requires the search index feature.
func (c *Client) Reindex(ctx context.Context) (int, error) {
	var out struct {
		Reindexed int `json:"reindexed"`
	}
	if err := c.do(ctx, http.MethodPost, "/search/reindex", nil, nil, &out); err != nil {
		return 0, err
	}
	return out.Reindexed, nil
}

// GraphQL runs a read-only GraphQL query and unmarshals data into dst (pass a
// pointer). variables may be nil. Query-level errors are returned as an error.
func (c *Client) GraphQL(ctx context.Context, query string, variables map[string]any, dst any) error {
	body := map[string]any{"query": query}
	if variables != nil {
		body["variables"] = variables
	}
	var res struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.do(ctx, http.MethodPost, "/graphql", nil, body, &res); err != nil {
		return err
	}
	if len(res.Errors) > 0 {
		msgs := make([]string, 0, len(res.Errors))
		for _, e := range res.Errors {
			msgs = append(msgs, e.Message)
		}
		return &APIError{Code: CodeValidation, Message: "graphql: " + strings.Join(msgs, "; ")}
	}
	if dst != nil && len(res.Data) > 0 {
		return json.Unmarshal(res.Data, dst)
	}
	return nil
}

// do executes one request: it marshals body (when non-nil) as JSON, applies
// auth and headers, and decodes a 2xx response into out (when non-nil) or a
// non-2xx into a typed APIError.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("flexitype client: encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return fmt.Errorf("flexitype client: build request: %w", err)
	}
	c.setHeaders(req, body != nil)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("flexitype client: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("flexitype client: decode response: %w", err)
	}
	return nil
}

// doRaw performs a GET returning the raw body and its content type — for
// endpoints that stream bytes (CSV export, media download).
func (c *Client) doRaw(ctx context.Context, path string, query url.Values) ([]byte, string, error) {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", fmt.Errorf("flexitype client: build request: %w", err)
	}
	c.setHeaders(req, false)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("flexitype client: GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", decodeError(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("flexitype client: read response: %w", err)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// doMultipart POSTs a multipart form (extra text fields plus one file) and
// decodes a 2xx response into out.
func (c *Client) doMultipart(ctx context.Context, path string, fields map[string]string, fileField, filename, fileMIME string, file io.Reader, out any) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, fileField, filename))
	if fileMIME != "" {
		hdr.Set("Content-Type", fileMIME)
	}
	part, err := mw.CreatePart(hdr)
	if err != nil {
		return fmt.Errorf("flexitype client: build upload: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("flexitype client: build upload: %w", err)
	}
	contentType := mw.FormDataContentType()
	if err := mw.Close(); err != nil {
		return fmt.Errorf("flexitype client: build upload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, &buf)
	if err != nil {
		return fmt.Errorf("flexitype client: build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	c.setHeaders(req, false)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("flexitype client: POST %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) setHeaders(req *http.Request, hasBody bool) {
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}
