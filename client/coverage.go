package client

// This file carries the REST capabilities that had no client method.
//
// Eight operations were reachable only over raw net/http, including both
// right-to-erasure endpoints that docs/erasure.md documents with curl
// recipes. A team that standardises on this SDK had to hand-roll erasure —
// the one operation with a statutory deadline attached — with its own auth,
// error decoding and retry handling, ending up with two client styles for
// one service.
//
// TestClientCoversEveryRoute in the root module asserts that every
// /api/v1 route has a method here or elsewhere in this package, so a new
// route cannot land without one.

import (
	"context"
	"iter"
	"net/http"
	"net/url"
	"strconv"
)

// --- values listing ----------------------------------------------------------

// ListValuesOptions filters a value listing. Every field is optional; with
// none set the call pages the tenant's whole value set.
type ListValuesOptions struct {
	ListOptions
	// TypeDefinitionID narrows to one type's entities.
	TypeDefinitionID string
	// AttributeDefinitionID narrows to one attribute across entities — the
	// "who has a value for this field" query.
	AttributeDefinitionID string
	// EntityID narrows to one entity.
	EntityID string
	// IncludeArchived folds in soft-deleted values.
	IncludeArchived bool
}

func (o ListValuesOptions) query() url.Values {
	q := url.Values{}
	if o.TypeDefinitionID != "" {
		q.Set("type_definition_id", o.TypeDefinitionID)
	}
	if o.AttributeDefinitionID != "" {
		q.Set("attribute_definition_id", o.AttributeDefinitionID)
	}
	if o.EntityID != "" {
		q.Set("entity_id", o.EntityID)
	}
	if o.IncludeArchived {
		q.Set("include_archived", "true")
	}
	return q
}

// List returns one page of values, filtered by opts.
func (s *ValuesService) List(ctx context.Context, opts ...ListValuesOptions) (*Page[AttributeValue], error) {
	o := ListValuesOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	return listPage[AttributeValue](ctx, s.c, "/values", o.query(), o.ListOptions)
}

// All iterates every matching value across pages.
func (s *ValuesService) All(ctx context.Context, opts ...ListValuesOptions) iter.Seq2[AttributeValue, error] {
	o := ListValuesOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	return paginate(func(cursor string) (*Page[AttributeValue], error) {
		o.Cursor = cursor
		return s.List(ctx, o)
	})
}

// --- relationship definitions ------------------------------------------------

// Update mutates a relationship definition.
func (s *RelationshipDefinitionsService) Update(ctx context.Context, id string, in CreateRelationshipDefinitionInput) (*RelationshipDefinition, error) {
	var out RelationshipDefinition
	if err := s.c.do(ctx, http.MethodPatch, "/relationship-definitions/"+id, nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AttributeSets returns the attribute-set ids a relationship definition
// carries on its links.
func (s *RelationshipDefinitionsService) AttributeSets(ctx context.Context, id string) ([]string, error) {
	var out struct {
		AttributeSetIDs []string `json:"attribute_set_ids"`
	}
	if err := s.c.do(ctx, http.MethodGet,
		"/relationship-definitions/"+id+"/attribute-sets", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.AttributeSetIDs, nil
}

// --- schema templates --------------------------------------------------------

// Template loads one curated schema template, including its bundle.
func (s *SchemaService) Template(ctx context.Context, name string) (*SchemaTemplate, error) {
	var out SchemaTemplate
	if err := s.c.do(ctx, http.MethodGet, "/schema/templates/"+url.PathEscape(name), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- maintenance -------------------------------------------------------------

// RecomputeComputed re-materializes every computed attribute for the caller's
// tenant and returns how many entities were rewritten. Admin scope.
//
// Run it after changing a formula, or to recover from a failed recompute.
func (c *Client) RecomputeComputed(ctx context.Context) (int, error) {
	var out struct {
		Recomputed int `json:"recomputed"`
	}
	if err := c.do(ctx, http.MethodPost, "/computed/recompute", nil, nil, &out); err != nil {
		return 0, err
	}
	return out.Recomputed, nil
}

// --- erasure -----------------------------------------------------------------

// PurgeReport counts what a purge permanently removed. MediaBlobsFailed and
// UnpurgedBlobKeys are non-zero when blob storage refused a delete, so an
// operator can reconcile the remaining objects by hand.
type PurgeReport struct {
	// EntityID is set for a per-entity purge and empty for a tenant purge.
	EntityID          string   `json:"entity_id,omitempty"`
	ValuesPurged      int      `json:"values_purged"`
	RevisionsPurged   int      `json:"revisions_purged"`
	RelationshipsGone int      `json:"relationships_gone"`
	SearchDocsPurged  int      `json:"search_docs_purged"`
	MediaBlobsPurged  int      `json:"media_blobs_purged"`
	MediaBlobsFailed  int      `json:"media_blobs_failed"`
	UnpurgedBlobKeys  []string `json:"unpurged_blob_keys,omitempty"`
}

// Purge permanently hard-deletes one entity's data: values, revisions, links,
// search documents and media blobs. Admin scope.
//
// This is the right-to-erasure primitive and it is irreversible — the rows are
// gone, not archived, and no revision retains the erased content. Check
// MediaBlobsFailed on the report: a blob store that refused a delete leaves
// objects behind, and only the report names them.
func (s *EntitiesService) Purge(ctx context.Context, typeID, entityID string) (*PurgeReport, error) {
	var out PurgeReport
	if err := s.c.do(ctx, http.MethodPost,
		"/entities/"+typeID+"/"+url.PathEscape(entityID)+"/purge", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PurgeTenant permanently hard-deletes the calling tenant's whole entity
// dataset. Admin scope. Irreversible, and scoped to the token's tenant — there
// is no cross-tenant form.
func (c *Client) PurgeTenant(ctx context.Context) (*PurgeReport, error) {
	var out PurgeReport
	if err := c.do(ctx, http.MethodPost, "/admin/purge", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- event feed cursor -------------------------------------------------------

// EventPage is one page of the event feed plus the cursor to resume from.
//
// The feed's next_cursor used to be discarded by the list helper, so a
// consumer could read the first page and had no supported way to ask for the
// next one.
type EventPage struct {
	Events []FeedEvent `json:"items"`
	// NextCursor is the position to pass as After for the following page.
	//
	// It is an int64 because the feed's cursor is a sequence number, and the
	// server always emits it — declaring it a string made ListPage fail to
	// decode on EVERY call, including against an empty feed.
	NextCursor int64 `json:"next_cursor"`
}

// ListPage returns one page of the event feed, after the given position, and
// the cursor for the next page. limit 0 uses the server default.
//
// after is the feed SEQUENCE to resume from — 0 for the beginning. It was
// typed as a string, which did not match the API and could not be produced
// from the page this method returns.
func (s *EventsService) ListPage(ctx context.Context, after int64, types []string, limit int) (*EventPage, error) {
	q := url.Values{}
	if after > 0 {
		q.Set("after", strconv.FormatInt(after, 10))
	}
	if len(types) > 0 {
		q["types"] = []string{joinComma(types)}
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out EventPage
	if err := s.c.do(ctx, http.MethodGet, "/events", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func joinComma(in []string) string {
	out := ""
	for i, s := range in {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
