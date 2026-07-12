package client

import (
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// --- dependencies ------------------------------------------------------------

// DependenciesService operates on attribute dependencies.
type DependenciesService struct{ c *Client }

// CreateDependencyInput creates a dependency. Conditions and Effect are raw
// JSON (arrays/objects matching the wire schema).
type CreateDependencyInput struct {
	SourceAttributeID string          `json:"source_attribute_id"`
	TargetAttributeID string          `json:"target_attribute_id"`
	Conditions        json.RawMessage `json:"conditions,omitempty"`
	Effect            json.RawMessage `json:"effect,omitempty"`
	Description       string          `json:"description,omitempty"`
}

// List returns one page of dependencies.
func (s *DependenciesService) List(ctx context.Context, opts ...ListOptions) (*Page[Dependency], error) {
	return listPage[Dependency](ctx, s.c, "/dependencies", url.Values{}, firstOpts(opts))
}

// All iterates every dependency across pages.
func (s *DependenciesService) All(ctx context.Context, opts ...ListOptions) iter.Seq2[Dependency, error] {
	o := firstOpts(opts)
	return paginate(func(cursor string) (*Page[Dependency], error) {
		o.Cursor = cursor
		return s.List(ctx, o)
	})
}

// Get loads one dependency.
func (s *DependenciesService) Get(ctx context.Context, id string) (*Dependency, error) {
	var out Dependency
	return &out, s.c.do(ctx, http.MethodGet, "/dependencies/"+id, nil, nil, &out)
}

// Create creates a dependency.
func (s *DependenciesService) Create(ctx context.Context, in CreateDependencyInput) (*Dependency, error) {
	var out Dependency
	return &out, s.c.do(ctx, http.MethodPost, "/dependencies", nil, in, &out)
}

// Update mutates a dependency.
func (s *DependenciesService) Update(ctx context.Context, id string, in CreateDependencyInput) (*Dependency, error) {
	var out Dependency
	return &out, s.c.do(ctx, http.MethodPatch, "/dependencies/"+id, nil, in, &out)
}

// Archive soft-deletes a dependency.
func (s *DependenciesService) Archive(ctx context.Context, id string) error {
	return s.c.do(ctx, http.MethodDelete, "/dependencies/"+id, nil, nil, nil)
}

// --- unit families -----------------------------------------------------------

// UnitFamiliesService operates on quantity unit families.
type UnitFamiliesService struct{ c *Client }

// CreateUnitFamilyInput creates a unit family. The base unit must be present in
// Units with factor 1.
type CreateUnitFamilyInput struct {
	Name     string             `json:"name"`
	BaseUnit string             `json:"base_unit"`
	Units    map[string]float64 `json:"units"`
}

// List returns the tenant's unit families.
func (s *UnitFamiliesService) List(ctx context.Context) ([]UnitFamily, error) {
	return items[UnitFamily](ctx, s.c, "/unit-families", nil)
}

// Get loads one unit family.
func (s *UnitFamiliesService) Get(ctx context.Context, id string) (*UnitFamily, error) {
	var out UnitFamily
	return &out, s.c.do(ctx, http.MethodGet, "/unit-families/"+id, nil, nil, &out)
}

// Create creates a unit family.
func (s *UnitFamiliesService) Create(ctx context.Context, in CreateUnitFamilyInput) (*UnitFamily, error) {
	var out UnitFamily
	return &out, s.c.do(ctx, http.MethodPost, "/unit-families", nil, in, &out)
}

// Delete removes a unit family.
func (s *UnitFamiliesService) Delete(ctx context.Context, id string) error {
	return s.c.do(ctx, http.MethodDelete, "/unit-families/"+id, nil, nil, nil)
}

// --- saved views -------------------------------------------------------------

// SavedViewsService operates on saved views.
type SavedViewsService struct{ c *Client }

// SavedViewInput creates or updates a saved view.
type SavedViewInput struct {
	Name     string   `json:"name"`
	RootType string   `json:"root_type"`
	Query    string   `json:"query,omitempty"`
	Columns  []string `json:"columns,omitempty"`
}

// List returns the tenant's saved views.
func (s *SavedViewsService) List(ctx context.Context) ([]SavedView, error) {
	return items[SavedView](ctx, s.c, "/saved-views", nil)
}

// Get loads one saved view.
func (s *SavedViewsService) Get(ctx context.Context, id string) (*SavedView, error) {
	var out SavedView
	return &out, s.c.do(ctx, http.MethodGet, "/saved-views/"+id, nil, nil, &out)
}

// Create creates a saved view.
func (s *SavedViewsService) Create(ctx context.Context, in SavedViewInput) (*SavedView, error) {
	var out SavedView
	return &out, s.c.do(ctx, http.MethodPost, "/saved-views", nil, in, &out)
}

// Update mutates a saved view.
func (s *SavedViewsService) Update(ctx context.Context, id string, in SavedViewInput) (*SavedView, error) {
	var out SavedView
	return &out, s.c.do(ctx, http.MethodPatch, "/saved-views/"+id, nil, in, &out)
}

// Delete removes a saved view.
func (s *SavedViewsService) Delete(ctx context.Context, id string) error {
	return s.c.do(ctx, http.MethodDelete, "/saved-views/"+id, nil, nil, nil)
}

// --- revisions ---------------------------------------------------------------

// RevisionsService operates on entity revisions by id.
type RevisionsService struct{ c *Client }

// Get loads one revision.
func (s *RevisionsService) Get(ctx context.Context, id string) (*EntityRevision, error) {
	var out EntityRevision
	return &out, s.c.do(ctx, http.MethodGet, "/revisions/"+id, nil, nil, &out)
}

// Diff returns the difference between a revision and the entity's current
// values as raw JSON (add/change/remove per attribute).
func (s *RevisionsService) Diff(ctx context.Context, id string) (json.RawMessage, error) {
	var out json.RawMessage
	return out, s.c.do(ctx, http.MethodGet, "/revisions/"+id+"/diff", nil, nil, &out)
}

// Restore rolls the entity back to a revision (replayed as normal writes, so
// events and activity fire).
func (s *RevisionsService) Restore(ctx context.Context, id string) error {
	return s.c.do(ctx, http.MethodPost, "/revisions/"+id+"/restore", nil, nil, nil)
}

// --- match rules (duplicate detection) --------------------------------------

// MatchRulesService operates on duplicate-detection rules by id. Create and
// list rules through Types().MatchRules / Types().CreateMatchRule.
type MatchRulesService struct{ c *Client }

// Scan runs a rule and returns scored candidate duplicate pairs.
func (s *MatchRulesService) Scan(ctx context.Context, ruleID string) (*MatchScan, error) {
	var out MatchScan
	return &out, s.c.do(ctx, http.MethodGet, "/match-rules/"+ruleID+"/scan", nil, nil, &out)
}

// Dismiss records a candidate pair as not-a-duplicate so it never resurfaces.
func (s *MatchRulesService) Dismiss(ctx context.Context, ruleID, entityA, entityB string) error {
	return s.c.do(ctx, http.MethodPost, "/match-rules/"+ruleID+"/dismiss", nil,
		map[string]string{"entity_a": entityA, "entity_b": entityB}, nil)
}

// Delete removes a match rule.
func (s *MatchRulesService) Delete(ctx context.Context, ruleID string) error {
	return s.c.do(ctx, http.MethodDelete, "/match-rules/"+ruleID, nil, nil, nil)
}

// --- schema (import/export + templates) -------------------------------------

// SchemaService moves a tenant's schema as a portable bundle and applies
// curated templates.
type SchemaService struct{ c *Client }

// Export gathers the caller's tenant schema into a portable, name-keyed bundle.
func (s *SchemaService) Export(ctx context.Context) (json.RawMessage, error) {
	var out json.RawMessage
	return out, s.c.do(ctx, http.MethodGet, "/schema/export", nil, nil, &out)
}

// Import applies a bundle idempotently and reports what was created vs skipped.
func (s *SchemaService) Import(ctx context.Context, bundle json.RawMessage) (*SchemaImportResult, error) {
	var out SchemaImportResult
	return &out, s.c.do(ctx, http.MethodPost, "/schema/import", nil, bundle, &out)
}

// Templates lists the curated starter schemas.
func (s *SchemaService) Templates(ctx context.Context) ([]SchemaTemplate, error) {
	return items[SchemaTemplate](ctx, s.c, "/schema/templates", nil)
}

// ApplyTemplate imports a curated template into the caller's tenant.
func (s *SchemaService) ApplyTemplate(ctx context.Context, name string) (*SchemaImportResult, error) {
	var out SchemaImportResult
	return &out, s.c.do(ctx, http.MethodPost, "/schema/templates/"+url.PathEscape(name)+"/apply", nil, nil, &out)
}

// --- change-sets -------------------------------------------------------------

// ChangeSetsService operates on staged change-sets.
type ChangeSetsService struct{ c *Client }

// CreateChangeSetInput opens a new change-set.
type CreateChangeSetInput struct {
	Name            string  `json:"name"`
	RequireApproval bool    `json:"require_approval,omitempty"`
	PublishAt       *string `json:"publish_at,omitempty"` // RFC3339
}

// List returns the tenant's change-sets.
func (s *ChangeSetsService) List(ctx context.Context) ([]ChangeSet, error) {
	return items[ChangeSet](ctx, s.c, "/changesets", nil)
}

// Get loads one change-set.
func (s *ChangeSetsService) Get(ctx context.Context, id string) (*ChangeSet, error) {
	var out ChangeSet
	return &out, s.c.do(ctx, http.MethodGet, "/changesets/"+id, nil, nil, &out)
}

// Create opens a change-set.
func (s *ChangeSetsService) Create(ctx context.Context, in CreateChangeSetInput) (*ChangeSet, error) {
	var out ChangeSet
	return &out, s.c.do(ctx, http.MethodPost, "/changesets", nil, in, &out)
}

// AddMutation stages one value change on a draft change-set.
func (s *ChangeSetsService) AddMutation(ctx context.Context, id string, m Mutation) (*ChangeSet, error) {
	var out ChangeSet
	return &out, s.c.do(ctx, http.MethodPost, "/changesets/"+id+"/mutations", nil, m, &out)
}

// Submit moves a draft into review.
func (s *ChangeSetsService) Submit(ctx context.Context, id string) (*ChangeSet, error) {
	return s.transition(ctx, id, "submit")
}

// Approve approves a change-set in review (must be a different actor than the author).
func (s *ChangeSetsService) Approve(ctx context.Context, id string) (*ChangeSet, error) {
	return s.transition(ctx, id, "approve")
}

// Reject rejects a change-set.
func (s *ChangeSetsService) Reject(ctx context.Context, id string) (*ChangeSet, error) {
	return s.transition(ctx, id, "reject")
}

// Publish applies an approved change-set's mutations now.
func (s *ChangeSetsService) Publish(ctx context.Context, id string) (*ChangeSet, error) {
	return s.transition(ctx, id, "publish")
}

func (s *ChangeSetsService) transition(ctx context.Context, id, action string) (*ChangeSet, error) {
	var out ChangeSet
	return &out, s.c.do(ctx, http.MethodPost, "/changesets/"+id+"/"+action, nil, nil, &out)
}

// --- relationship definitions -----------------------------------------------

// RelationshipDefinitionsService operates on relationship definitions.
type RelationshipDefinitionsService struct{ c *Client }

// CreateRelationshipDefinitionInput creates a relationship definition.
type CreateRelationshipDefinitionInput struct {
	InternalName        string `json:"internal_name"`
	DisplayName         string `json:"display_name"`
	Description         string `json:"description,omitempty"`
	Kind                string `json:"kind,omitempty"` // directed (default) | symmetric
	ParentTypeID        string `json:"parent_type_id"`
	ChildTypeID         string `json:"child_type_id"`
	ParentLabel         string `json:"parent_label,omitempty"`
	ChildLabel          string `json:"child_label,omitempty"`
	ExtendsID           string `json:"extends_id,omitempty"`
	ParentVersionPolicy string `json:"parent_version_policy,omitempty"`
	ChildVersionPolicy  string `json:"child_version_policy,omitempty"`
	MinChildren         *int   `json:"min_children,omitempty"`
	MaxChildren         *int   `json:"max_children,omitempty"`
	MinParents          *int   `json:"min_parents,omitempty"`
	MaxParents          *int   `json:"max_parents,omitempty"`
}

// ListRelationshipDefinitionsOptions filters a definition listing.
type ListRelationshipDefinitionsOptions struct {
	ListOptions
	TypeDefinitionID string
	IncludeArchived  bool
}

// List returns one page of relationship definitions.
func (s *RelationshipDefinitionsService) List(ctx context.Context, opts ...ListRelationshipDefinitionsOptions) (*Page[RelationshipDefinition], error) {
	o := ListRelationshipDefinitionsOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	q := url.Values{}
	if o.TypeDefinitionID != "" {
		q.Set("type_definition_id", o.TypeDefinitionID)
	}
	if o.IncludeArchived {
		q.Set("include_archived", "true")
	}
	return listPage[RelationshipDefinition](ctx, s.c, "/relationship-definitions", q, o.ListOptions)
}

// Get loads one relationship definition.
func (s *RelationshipDefinitionsService) Get(ctx context.Context, id string) (*RelationshipDefinition, error) {
	var out RelationshipDefinition
	return &out, s.c.do(ctx, http.MethodGet, "/relationship-definitions/"+id, nil, nil, &out)
}

// Create creates a relationship definition.
func (s *RelationshipDefinitionsService) Create(ctx context.Context, in CreateRelationshipDefinitionInput) (*RelationshipDefinition, error) {
	var out RelationshipDefinition
	return &out, s.c.do(ctx, http.MethodPost, "/relationship-definitions", nil, in, &out)
}

// Archive soft-deletes a relationship definition.
func (s *RelationshipDefinitionsService) Archive(ctx context.Context, id string) (*RelationshipDefinition, error) {
	var out RelationshipDefinition
	return &out, s.c.do(ctx, http.MethodPost, "/relationship-definitions/"+id+"/archive", nil, nil, &out)
}

// Restore reverses an archive.
func (s *RelationshipDefinitionsService) Restore(ctx context.Context, id string) (*RelationshipDefinition, error) {
	var out RelationshipDefinition
	return &out, s.c.do(ctx, http.MethodPost, "/relationship-definitions/"+id+"/restore", nil, nil, &out)
}

// --- relationships (links) --------------------------------------------------

// RelationshipsService operates on relationship instances (links).
type RelationshipsService struct{ c *Client }

// LinkInput creates one link between two entities.
type LinkInput struct {
	DefinitionID string `json:"relationship_definition_id"`
	ParentEntity string `json:"parent_entity_id"`
	ChildEntity  string `json:"child_entity_id"`
}

// List returns one page of relationships.
func (s *RelationshipsService) List(ctx context.Context, opts ...ListOptions) (*Page[Relationship], error) {
	return listPage[Relationship](ctx, s.c, "/relationships", url.Values{}, firstOpts(opts))
}

// Get loads one relationship.
func (s *RelationshipsService) Get(ctx context.Context, id string) (*Relationship, error) {
	var out Relationship
	return &out, s.c.do(ctx, http.MethodGet, "/relationships/"+id, nil, nil, &out)
}

// Link creates a relationship between two entities.
func (s *RelationshipsService) Link(ctx context.Context, in LinkInput) (*Relationship, error) {
	var out Relationship
	return &out, s.c.do(ctx, http.MethodPost, "/relationships", nil, in, &out)
}

// Unlink archives a relationship.
func (s *RelationshipsService) Unlink(ctx context.Context, id string) error {
	return s.c.do(ctx, http.MethodDelete, "/relationships/"+id, nil, nil, nil)
}

// --- activity ----------------------------------------------------------------

// ActivityService reads the audit log.
type ActivityService struct{ c *Client }

// ListActivityOptions filters the audit log.
type ListActivityOptions struct {
	ListOptions
	Entity   string
	EntityID string
	Actor    string
}

// List returns one page of audit entries (newest first).
func (s *ActivityService) List(ctx context.Context, opts ...ListActivityOptions) (*Page[ActivityEntry], error) {
	o := ListActivityOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	q := url.Values{}
	if o.Entity != "" {
		q.Set("entity", o.Entity)
	}
	if o.EntityID != "" {
		q.Set("entity_id", o.EntityID)
	}
	if o.Actor != "" {
		q.Set("actor", o.Actor)
	}
	return listPage[ActivityEntry](ctx, s.c, "/activity", q, o.ListOptions)
}

// All iterates every audit entry across pages.
func (s *ActivityService) All(ctx context.Context, opts ...ListActivityOptions) iter.Seq2[ActivityEntry, error] {
	o := ListActivityOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	return paginate(func(cursor string) (*Page[ActivityEntry], error) {
		o.Cursor = cursor
		return s.List(ctx, o)
	})
}

// --- webhooks ----------------------------------------------------------------

// WebhooksService manages webhook subscriptions and inspects deliveries
// (requires the outbox / event delivery feature).
type WebhooksService struct{ c *Client }

// SubscriptionInput creates or updates a webhook subscription.
type SubscriptionInput struct {
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	Secret     string   `json:"secret,omitempty"`
	EventTypes []string `json:"event_types,omitempty"`
	Active     *bool    `json:"active,omitempty"`
}

// List returns the tenant's webhook subscriptions.
func (s *WebhooksService) List(ctx context.Context) ([]WebhookSubscription, error) {
	return items[WebhookSubscription](ctx, s.c, "/webhook-subscriptions", nil)
}

// Get loads one subscription.
func (s *WebhooksService) Get(ctx context.Context, id string) (*WebhookSubscription, error) {
	var out WebhookSubscription
	return &out, s.c.do(ctx, http.MethodGet, "/webhook-subscriptions/"+id, nil, nil, &out)
}

// Create creates a subscription.
func (s *WebhooksService) Create(ctx context.Context, in SubscriptionInput) (*WebhookSubscription, error) {
	var out WebhookSubscription
	return &out, s.c.do(ctx, http.MethodPost, "/webhook-subscriptions", nil, in, &out)
}

// Update mutates a subscription.
func (s *WebhooksService) Update(ctx context.Context, id string, in SubscriptionInput) (*WebhookSubscription, error) {
	var out WebhookSubscription
	return &out, s.c.do(ctx, http.MethodPatch, "/webhook-subscriptions/"+id, nil, in, &out)
}

// Delete removes a subscription.
func (s *WebhooksService) Delete(ctx context.Context, id string) error {
	return s.c.do(ctx, http.MethodDelete, "/webhook-subscriptions/"+id, nil, nil, nil)
}

// Deliveries returns a subscription's delivery log, newest first; status
// filters (e.g. "failed", "dead") when non-empty.
func (s *WebhooksService) Deliveries(ctx context.Context, id, status string, opts ...ListOptions) (*Page[WebhookDelivery], error) {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	return listPage[WebhookDelivery](ctx, s.c, "/webhook-subscriptions/"+id+"/deliveries", q, firstOpts(opts))
}

// Redeliver requeues a dead or delivered delivery.
func (s *WebhooksService) Redeliver(ctx context.Context, deliveryID string) error {
	return s.c.do(ctx, http.MethodPost, "/webhook-deliveries/"+deliveryID+"/redeliver", nil, nil, nil)
}

// --- events ------------------------------------------------------------------

// EventsService reads the events feed and cursors (requires event delivery).
type EventsService struct{ c *Client }

// ListEventsOptions pages the feed.
type ListEventsOptions struct {
	After int64
	Types []string
	Limit int
}

// List returns feed events after the given sequence.
func (s *EventsService) List(ctx context.Context, opts ...ListEventsOptions) ([]FeedEvent, error) {
	o := ListEventsOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	q := url.Values{}
	if o.After > 0 {
		q.Set("after", strconv.FormatInt(o.After, 10))
	}
	if len(o.Types) > 0 {
		q.Set("types", strings.Join(o.Types, ","))
	}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	return items[FeedEvent](ctx, s.c, "/events", q)
}

// GetCursor reads a named consumer cursor's committed sequence.
func (s *EventsService) GetCursor(ctx context.Context, consumer string) (int64, error) {
	var out struct {
		AfterSeq int64 `json:"after_seq"`
	}
	if err := s.c.do(ctx, http.MethodGet, "/event-cursors/"+url.PathEscape(consumer), nil, nil, &out); err != nil {
		return 0, err
	}
	return out.AfterSeq, nil
}

// CommitCursor advances a named consumer cursor with compare-and-swap on the
// expected previous sequence.
func (s *EventsService) CommitCursor(ctx context.Context, consumer string, afterSeq, expectedSeq int64) error {
	return s.c.do(ctx, http.MethodPut, "/event-cursors/"+url.PathEscape(consumer), nil,
		map[string]int64{"after_seq": afterSeq, "expected_seq": expectedSeq}, nil)
}

// --- admin (provisioning) ----------------------------------------------------

// AdminService provisions tenants and service accounts (admin-scoped;
// database-backed deployments only).
type AdminService struct{ c *Client }

// ListTenants returns the provisioned tenants.
func (s *AdminService) ListTenants(ctx context.Context) ([]Tenant, error) {
	return items[Tenant](ctx, s.c, "/tenants", nil)
}

// CreateTenant provisions a tenant.
func (s *AdminService) CreateTenant(ctx context.Context, name string) (*Tenant, error) {
	var out Tenant
	return &out, s.c.do(ctx, http.MethodPost, "/tenants", nil, map[string]string{"name": name}, &out)
}

// SetTenantActive enables or disables a tenant.
func (s *AdminService) SetTenantActive(ctx context.Context, name string, active bool) (*Tenant, error) {
	var out Tenant
	return &out, s.c.do(ctx, http.MethodPatch, "/tenants/"+url.PathEscape(name), nil, map[string]bool{"active": active}, &out)
}

// ListServiceAccounts returns the service accounts (optionally for one tenant).
func (s *AdminService) ListServiceAccounts(ctx context.Context, tenant string) ([]ServiceAccount, error) {
	q := url.Values{}
	if tenant != "" {
		q.Set("tenant", tenant)
	}
	return items[ServiceAccount](ctx, s.c, "/service-accounts", q)
}

// CreateServiceAccountInput provisions a credential. The returned Token is
// shown once.
type CreateServiceAccountInput struct {
	TenantName string   `json:"tenant_name"`
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes,omitempty"`
}

// CreateServiceAccount provisions a service account and returns its one-time token.
func (s *AdminService) CreateServiceAccount(ctx context.Context, in CreateServiceAccountInput) (*ServiceAccount, error) {
	var out ServiceAccount
	return &out, s.c.do(ctx, http.MethodPost, "/service-accounts", nil, in, &out)
}

// RotateServiceAccount issues a new token for a service account.
func (s *AdminService) RotateServiceAccount(ctx context.Context, id string) (*ServiceAccount, error) {
	var out ServiceAccount
	return &out, s.c.do(ctx, http.MethodPost, "/service-accounts/"+id+"/rotate", nil, nil, &out)
}

// RevokeServiceAccount deletes a service account.
func (s *AdminService) RevokeServiceAccount(ctx context.Context, id string) error {
	return s.c.do(ctx, http.MethodDelete, "/service-accounts/"+id, nil, nil, nil)
}
