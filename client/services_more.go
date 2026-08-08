package client

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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
	if err := s.c.do(ctx, http.MethodGet, "/dependencies/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Create creates a dependency.
func (s *DependenciesService) Create(ctx context.Context, in CreateDependencyInput) (*Dependency, error) {
	var out Dependency
	if err := s.c.do(ctx, http.MethodPost, "/dependencies", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Update mutates a dependency.
func (s *DependenciesService) Update(ctx context.Context, id string, in CreateDependencyInput) (*Dependency, error) {
	var out Dependency
	if err := s.c.do(ctx, http.MethodPatch, "/dependencies/"+id, nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
	if err := s.c.do(ctx, http.MethodGet, "/unit-families/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Create creates a unit family.
func (s *UnitFamiliesService) Create(ctx context.Context, in CreateUnitFamilyInput) (*UnitFamily, error) {
	var out UnitFamily
	if err := s.c.do(ctx, http.MethodPost, "/unit-families", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete removes a unit family.
func (s *UnitFamiliesService) Delete(ctx context.Context, id string) error {
	return s.c.do(ctx, http.MethodDelete, "/unit-families/"+id, nil, nil, nil)
}

// --- saved views -------------------------------------------------------------

// SavedViewsService operates on saved views.
type SavedViewsService struct{ c *Client }

// SavedViewInput creates or fully replaces a saved view. Every field is sent,
// so a zero value clears the stored one — use SavedViewPatch to change some
// fields and leave the rest alone.
type SavedViewInput struct {
	Name     string `json:"name"`
	RootType string `json:"root_type"`
	Query    string `json:"query"`
	// Columns is always transmitted, as [] when nil.
	//
	// The server's sparse decoder reads "" as "clear" and null as "absent",
	// so a nil slice made Update neither a full replace nor sparse: it
	// cleared query and sort while leaving columns as stored. Sending an
	// explicit empty array makes the documented full-replace true for every
	// field.
	Columns []string `json:"columns"`
	Sort    string   `json:"sort"`
}

// MarshalJSON transmits a nil Columns as [], so Update replaces every field.
func (in SavedViewInput) MarshalJSON() ([]byte, error) {
	type wire SavedViewInput
	w := wire(in)
	if w.Columns == nil {
		w.Columns = []string{}
	}
	return json.Marshal(w)
}

// SavedViewPatch is a sparse update: a nil field is left as it is stored.
//
// This exists because the full-replace form loses data on an unrelated edit.
// Renaming a view built in the console used to omit its sort order and clear
// it, and the SDK could not even read that field back.
type SavedViewPatch struct {
	Name     *string   `json:"name,omitempty"`
	RootType *string   `json:"root_type,omitempty"`
	Query    *string   `json:"query,omitempty"`
	Columns  *[]string `json:"columns,omitempty"`
	Sort     *string   `json:"sort,omitempty"`
	// Version is the version the caller read (SavedView.Version). Sending it
	// makes the patch a compare-and-swap: a view someone else edited in the
	// meantime answers 409 rather than being overwritten. Leaving it nil
	// keeps last-write-wins.
	Version *int `json:"version,omitempty"`
}

// List returns the tenant's saved views.
func (s *SavedViewsService) List(ctx context.Context) ([]SavedView, error) {
	return items[SavedView](ctx, s.c, "/saved-views", nil)
}

// Get loads one saved view.
func (s *SavedViewsService) Get(ctx context.Context, id string) (*SavedView, error) {
	var out SavedView
	if err := s.c.do(ctx, http.MethodGet, "/saved-views/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Create creates a saved view.
func (s *SavedViewsService) Create(ctx context.Context, in SavedViewInput) (*SavedView, error) {
	var out SavedView
	if err := s.c.do(ctx, http.MethodPost, "/saved-views", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Update replaces every field of a saved view. A zero value in the input
// clears the stored value.
//
// Use Patch for anything else — including a rename. Update is a full replace,
// so renaming with it clears the query, columns and sort unless the caller
// restates them, which is how a rename came to wipe a view's sort order.
func (s *SavedViewsService) Update(ctx context.Context, id string, in SavedViewInput) (*SavedView, error) {
	var out SavedView
	if err := s.c.do(ctx, http.MethodPatch, "/saved-views/"+id, nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Patch changes only the fields set on in, leaving the rest as stored.
func (s *SavedViewsService) Patch(ctx context.Context, id string, in SavedViewPatch) (*SavedView, error) {
	var out SavedView
	if err := s.c.do(ctx, http.MethodPatch, "/saved-views/"+id, nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
	if err := s.c.do(ctx, http.MethodGet, "/revisions/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Diff returns the difference between two revisions of one entity as raw
// JSON (add/change/remove per attribute). Both ids are required.
//
// The endpoint diffs revision against revision. An earlier signature took
// one id and sent no query, so the server answered
// "to (revision id) is required" on every call.
func (s *RevisionsService) Diff(ctx context.Context, fromID, toID string) (json.RawMessage, error) {
	if fromID == "" || toID == "" {
		return nil, fmt.Errorf("flexitype: Diff requires both revision ids")
	}
	var out json.RawMessage
	q := url.Values{"to": {toID}}
	return out, s.c.do(ctx, http.MethodGet, "/revisions/"+fromID+"/diff", q, nil, &out)
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
	if err := s.c.do(ctx, http.MethodGet, "/match-rules/"+ruleID+"/scan", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
	if err := s.c.do(ctx, http.MethodPost, "/schema/import", nil, bundle, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Templates lists the curated starter schemas.
func (s *SchemaService) Templates(ctx context.Context) ([]SchemaTemplate, error) {
	return items[SchemaTemplate](ctx, s.c, "/schema/templates", nil)
}

// ApplyTemplate imports a curated template into the caller's tenant.
func (s *SchemaService) ApplyTemplate(ctx context.Context, name string) (*SchemaImportResult, error) {
	var out SchemaImportResult
	if err := s.c.do(ctx, http.MethodPost, "/schema/templates/"+url.PathEscape(name)+"/apply", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
	if err := s.c.do(ctx, http.MethodGet, "/changesets/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Create opens a change-set.
func (s *ChangeSetsService) Create(ctx context.Context, in CreateChangeSetInput) (*ChangeSet, error) {
	var out ChangeSet
	if err := s.c.do(ctx, http.MethodPost, "/changesets", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AddMutation stages one value change on a draft change-set.
func (s *ChangeSetsService) AddMutation(ctx context.Context, id string, m Mutation) (*ChangeSet, error) {
	var out ChangeSet
	if err := s.c.do(ctx, http.MethodPost, "/changesets/"+id+"/mutations", nil, m, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
	if err := s.c.do(ctx, http.MethodPost, "/changesets/"+id+"/"+action, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
	if err := s.c.do(ctx, http.MethodGet, "/relationship-definitions/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Create creates a relationship definition.
func (s *RelationshipDefinitionsService) Create(ctx context.Context, in CreateRelationshipDefinitionInput) (*RelationshipDefinition, error) {
	var out RelationshipDefinition
	if err := s.c.do(ctx, http.MethodPost, "/relationship-definitions", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Archive soft-deletes a relationship definition.
func (s *RelationshipDefinitionsService) Archive(ctx context.Context, id string) (*RelationshipDefinition, error) {
	var out RelationshipDefinition
	if err := s.c.do(ctx, http.MethodPost, "/relationship-definitions/"+id+"/archive", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Restore reverses an archive.
func (s *RelationshipDefinitionsService) Restore(ctx context.Context, id string) (*RelationshipDefinition, error) {
	var out RelationshipDefinition
	if err := s.c.do(ctx, http.MethodPost, "/relationship-definitions/"+id+"/restore", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
	if err := s.c.do(ctx, http.MethodGet, "/relationships/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Link creates a relationship between two entities.
func (s *RelationshipsService) Link(ctx context.Context, in LinkInput) (*Relationship, error) {
	var out Relationship
	if err := s.c.do(ctx, http.MethodPost, "/relationships", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
	if err := s.c.do(ctx, http.MethodGet, "/webhook-subscriptions/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Create creates a subscription.
func (s *WebhooksService) Create(ctx context.Context, in SubscriptionInput) (*WebhookSubscription, error) {
	var out WebhookSubscription
	if err := s.c.do(ctx, http.MethodPost, "/webhook-subscriptions", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// updateSubscriptionBody is the shape PATCH /webhook-subscriptions/{id}
// accepts. It decodes with DisallowUnknownFields, so a field it does not name
// fails the whole request.
type updateSubscriptionBody struct {
	URL          *string   `json:"url,omitempty"`
	EventTypes   *[]string `json:"event_types,omitempty"`
	Active       *bool     `json:"active,omitempty"`
	RotateSecret *string   `json:"rotate_secret,omitempty"`
}

// Update mutates a subscription: its URL, its event types, whether it is
// active, and its secret.
//
// Only those four change. The API cannot rename a subscription, so a Name is
// REFUSED rather than dropped — silently ignoring it would report success for
// a rename that did not happen. Secret maps to the API's rotate_secret.
//
// This method could not succeed before. It sent SubscriptionInput, whose
// name field carries no omitempty and whose secret field the API does not
// know under that name, into a handler that rejects an unknown field: every
// call returned VALIDATION "invalid request body". The signature is unchanged
// so no caller has to be edited — there is no working caller to break.
func (s *WebhooksService) Update(ctx context.Context, id string, in SubscriptionInput) (*WebhookSubscription, error) {
	if in.Name != "" {
		return nil, &APIError{
			Code:    CodeValidation,
			Message: "a webhook subscription cannot be renamed; leave Name empty when updating one",
		}
	}
	body := updateSubscriptionBody{}
	if in.URL != "" {
		body.URL = &in.URL
	}
	if in.EventTypes != nil {
		body.EventTypes = &in.EventTypes
	}
	if in.Active != nil {
		body.Active = in.Active
	}
	if in.Secret != "" {
		body.RotateSecret = &in.Secret
	}

	var out WebhookSubscription
	if err := s.c.do(ctx, http.MethodPatch, "/webhook-subscriptions/"+id, nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
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

// RedeliverDead returns every dead delivery to pending and reports how many
// it moved. An empty subscriptionID covers the whole tenant. Admin scope.
//
// The per-delivery Redeliver is not a recovery path after an outage: the dead
// letters number in the thousands, and one call each is a script an operator
// has to write while the incident is still open.
func (s *WebhooksService) RedeliverDead(ctx context.Context, subscriptionID string) (int, error) {
	q := url.Values{}
	if subscriptionID != "" {
		q.Set("subscription_id", subscriptionID)
	}
	var out struct {
		Redelivered int `json:"redelivered"`
	}
	if err := s.c.do(ctx, http.MethodPost, "/webhook-deliveries/redeliver-dead", q, nil, &out); err != nil {
		return 0, err
	}
	return out.Redelivered, nil
}

// Redeliver requeues a dead or delivered delivery.
func (s *WebhooksService) Redeliver(ctx context.Context, deliveryID string) error {
	return s.c.do(ctx, http.MethodPost, "/webhook-deliveries/"+deliveryID+"/redeliver", nil, nil, nil)
}

// --- events ------------------------------------------------------------------

// EventsService reads the events feed and cursors (requires event delivery).
//
// Streaming is out of scope for this SDK. GET /api/v1/events/stream serves
// server-sent events, which needs a long-lived connection with its own
// reconnect and backoff policy; that belongs to the consumer, not to a
// request/response client. Poll with ListPage and commit a cursor instead —
// the cursor gives at-least-once delivery across restarts, which a stream
// alone does not.
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

// GetCursor reads a named consumer cursor's committed position.
//
// The wire field is `position`, matching the server and api/openapi.yaml. It
// was `after_seq` here, which the server never writes, so this returned 0 on
// every call with a NIL error — indistinguishable from a fresh consumer's
// legitimate start, so a documented at-least-once consumer replayed the whole
// retained feed on every restart with nothing reporting a failure.
func (s *EventsService) GetCursor(ctx context.Context, consumer string) (int64, error) {
	var out struct {
		Position int64 `json:"position"`
	}
	if err := s.c.do(ctx, http.MethodGet, "/event-cursors/"+url.PathEscape(consumer), nil, nil, &out); err != nil {
		return 0, err
	}
	return out.Position, nil
}

// CommitCursor advances a named consumer cursor with compare-and-swap on the
// expected previous position.
//
// The wire fields are `position` and `expected`. They were `after_seq` and
// `expected_seq`, and the server decodes strictly, so every commit was a hard
// 422 — the cursor never advanced and the CURSOR_CONFLICT / CURSOR_EXPIRED
// recovery paths this endpoint exists for were unreachable.
func (s *EventsService) CommitCursor(ctx context.Context, consumer string, position, expected int64) error {
	return s.c.do(ctx, http.MethodPut, "/event-cursors/"+url.PathEscape(consumer), nil,
		map[string]int64{"position": position, "expected": expected}, nil)
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
	if err := s.c.do(ctx, http.MethodPost, "/tenants", nil, map[string]string{"name": name}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetTenantActive enables or disables a tenant.
func (s *AdminService) SetTenantActive(ctx context.Context, name string, active bool) (*Tenant, error) {
	var out Tenant
	if err := s.c.do(ctx, http.MethodPatch, "/tenants/"+url.PathEscape(name), nil, map[string]bool{"active": active}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListServiceAccounts returns one tenant's service accounts. tenant is
// required: the endpoint has no cross-tenant listing.
//
// The query parameter is `tenant_name`. An earlier version sent `tenant`,
// which the server ignored, so the call answered
// "tenant_name is required" whether or not a tenant was passed.
func (s *AdminService) ListServiceAccounts(ctx context.Context, tenant string) ([]ServiceAccount, error) {
	if tenant == "" {
		return nil, fmt.Errorf("flexitype: ListServiceAccounts requires a tenant name")
	}
	return items[ServiceAccount](ctx, s.c, "/service-accounts", url.Values{"tenant_name": {tenant}})
}

// CreateServiceAccountInput provisions a credential. The returned Token is
// shown once.
type CreateServiceAccountInput struct {
	TenantName string   `json:"tenant_name"`
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes,omitempty"`
	// Roles the account holds from the start.
	Roles []string `json:"roles,omitempty"`
	// FieldPermissions are per-account overrides. Roles are the normal way
	// to grant a permission set; use this for one account that differs.
	FieldPermissions map[string]string `json:"field_permissions,omitempty"`
}

// CreateServiceAccount provisions a service account and returns its one-time token.
func (s *AdminService) CreateServiceAccount(ctx context.Context, in CreateServiceAccountInput) (*ServiceAccount, error) {
	var out ServiceAccount
	if err := s.c.do(ctx, http.MethodPost, "/service-accounts", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RotateServiceAccount issues a new token for a service account.
func (s *AdminService) RotateServiceAccount(ctx context.Context, id string) (*ServiceAccount, error) {
	var out ServiceAccount
	if err := s.c.do(ctx, http.MethodPost, "/service-accounts/"+id+"/rotate", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeServiceAccount deletes a service account.
func (s *AdminService) RevokeServiceAccount(ctx context.Context, id string) error {
	return s.c.do(ctx, http.MethodDelete, "/service-accounts/"+id, nil, nil, nil)
}

// EffectiveAccount reports what an account can actually do: its own scopes
// unioned with its roles', and the merged per-attribute permissions.
//
// ListServiceAccounts and ListRoles report what is STORED, which is what you
// edit. This reports what the enforcement path computes. A non-empty
// UnresolvedRoles is a fault: such an account is denied every attribute.
func (s *AdminService) EffectiveAccount(ctx context.Context, id string) (*EffectiveAccount, error) {
	var out EffectiveAccount
	if err := s.c.do(ctx, http.MethodGet,
		"/service-accounts/"+url.PathEscape(id)+"/effective", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ParkedEnvelope is one outbox envelope that exhausted its dispatch retry
// budget and waits for an operator redrive. It is a committed change no
// consumer has seen: it is off the events feed and has no webhook
// deliveries until it is redriven.
type ParkedEnvelope struct {
	ID            string    `json:"id"`
	EventType     string    `json:"event_type"`
	AggregateType string    `json:"aggregate_type"`
	AggregateID   string    `json:"aggregate_id"`
	Attempts      int       `json:"attempts"`
	LastError     string    `json:"last_error"`
	RecordedAt    time.Time `json:"recorded_at"`
	ParkedAt      time.Time `json:"parked_at"`
}

// ParkedFilter narrows a parked listing or a redrive. Zero values mean "no
// constraint"; ID selects exactly one envelope.
type ParkedFilter struct {
	EventType string
	ID        string
}

func (f ParkedFilter) query() url.Values {
	q := url.Values{}
	if f.EventType != "" {
		q.Set("event_type", f.EventType)
	}
	if f.ID != "" {
		q.Set("id", f.ID)
	}
	return q
}

// ListParkedOutbox pages the tenant's parked outbox envelopes, oldest first.
// Admin scope. Alert on the flexitype_outbox_parked gauge, inspect the
// backlog here, and redrive before the parked retention deletes it.
func (s *AdminService) ListParkedOutbox(ctx context.Context, filter ParkedFilter, opts ...ListOptions) (*Page[ParkedEnvelope], error) {
	return listPage[ParkedEnvelope](ctx, s.c, "/admin/outbox/parked", filter.query(), firstOpts(opts))
}

// RedriveOutbox returns the matching parked envelopes to the retry queue and
// reports how many it moved. Admin scope.
//
// The redrive resets each envelope's attempt count — a fresh retry budget,
// as with the dead-letter redrive — and delivery restarts immediately.
// Consumers must dedupe on the envelope id, as with every redelivery. An
// empty filter redrives the tenant's whole parked backlog.
func (s *AdminService) RedriveOutbox(ctx context.Context, filter ParkedFilter) (int, error) {
	var out struct {
		Redriven int `json:"redriven"`
	}
	if err := s.c.do(ctx, http.MethodPost, "/admin/outbox/redrive", filter.query(), nil, &out); err != nil {
		return 0, err
	}
	return out.Redriven, nil
}

// UpsertRoleInput creates or replaces a role. The write replaces the whole
// role rather than patching it, so the stored record is the full grant.
type UpsertRoleInput struct {
	TenantName       string            `json:"tenant_name"`
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Scopes           []string          `json:"scopes,omitempty"`
	FieldPermissions map[string]string `json:"field_permissions,omitempty"`
}

// UpsertRole creates or replaces a role.
func (s *AdminService) UpsertRole(ctx context.Context, in UpsertRoleInput) (*Role, error) {
	var out Role
	if err := s.c.do(ctx, http.MethodPut, "/roles", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListRoles returns one tenant's roles. tenant is required: the endpoint has
// no cross-tenant listing.
func (s *AdminService) ListRoles(ctx context.Context, tenant string) ([]Role, error) {
	if tenant == "" {
		return nil, fmt.Errorf("flexitype: ListRoles requires a tenant name")
	}
	return items[Role](ctx, s.c, "/roles", url.Values{"tenant_name": {tenant}})
}

// DeleteRole removes a role.
//
// It is refused with ErrorCodeConflict while any account still names the
// role: a role that resolves to nothing grants no field permissions, and an
// empty permission map reads as unrestricted, so deleting a held role would
// hand out every attribute it was hiding. Reassign those accounts first with
// AssignRoles.
func (s *AdminService) DeleteRole(ctx context.Context, tenant, name string) error {
	if tenant == "" {
		return fmt.Errorf("flexitype: DeleteRole requires a tenant name")
	}
	return s.c.do(ctx, http.MethodDelete, "/roles/"+url.PathEscape(name),
		url.Values{"tenant_name": {tenant}}, nil, nil)
}

// AssignRolesInput replaces an account's roles and its own overrides. Both
// lists are replaced, so an empty slice clears them.
type AssignRolesInput struct {
	Roles            []string          `json:"roles"`
	FieldPermissions map[string]string `json:"field_permissions"`
}

// AssignRoles replaces a service account's roles and per-account overrides.
// The server evicts the account's auth-cache entry, so a removed permission
// stops applying at once rather than at the end of the cache TTL.
func (s *AdminService) AssignRoles(ctx context.Context, id string, in AssignRolesInput) error {
	return s.c.do(ctx, http.MethodPut, "/service-accounts/"+url.PathEscape(id)+"/roles", nil, in, nil)
}
