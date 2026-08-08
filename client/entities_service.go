package client

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strings"
)

// EntitiesService operates at the entity level: browsing, the faceted grid,
// import/export, media, revisions and per-entity reads.
type EntitiesService struct{ c *Client }

// ListEntitiesOptions filters an entity listing; IncludeDescendants folds in
// subtype rows.
type ListEntitiesOptions struct {
	ListOptions
	IncludeDescendants bool
}

// List returns one page of a type's entities (newest-first).
func (s *EntitiesService) List(ctx context.Context, typeID string, opts ...ListEntitiesOptions) (*Page[EntitySummary], error) {
	o := ListEntitiesOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	q := url.Values{}
	if o.IncludeDescendants {
		q.Set("include_descendants", "true")
	}
	return listPage[EntitySummary](ctx, s.c, "/entities/"+typeID, q, o.ListOptions)
}

// All iterates every entity of a type across pages.
func (s *EntitiesService) All(ctx context.Context, typeID string, opts ...ListEntitiesOptions) iter.Seq2[EntitySummary, error] {
	o := ListEntitiesOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	return paginate(func(cursor string) (*Page[EntitySummary], error) {
		o.Cursor = cursor
		return s.List(ctx, typeID, o)
	})
}

// Values returns an entity's live values.
func (s *EntitiesService) Values(ctx context.Context, typeID, entityID string) ([]AttributeValue, error) {
	return items[AttributeValue](ctx, s.c, "/entities/"+typeID+"/"+url.PathEscape(entityID)+"/values", nil)
}

// ValuesPreview returns an entity's values with a draft change-set's staged
// mutations overlaid, so a reviewer sees what publishing would produce
// without touching live data.
//
// The overlay exists on this per-entity endpoint only. ListValuesOptions once
// carried a ChangeSetID that it sent to the paginated /values endpoint, which
// does not read it — so a preview silently returned the LIVE values, and a
// reviewer could approve a diff that was never displayed. The option is gone;
// this is the supported way.
func (s *EntitiesService) ValuesPreview(ctx context.Context, typeID, entityID, changeSetID string) ([]AttributeValue, error) {
	q := url.Values{}
	q.Set("changeset", changeSetID)
	return items[AttributeValue](ctx, s.c,
		"/entities/"+typeID+"/"+url.PathEscape(entityID)+"/values", q)
}

// Relationships returns an entity's links, with definitions and roles resolved.
func (s *EntitiesService) Relationships(ctx context.Context, typeID, entityID string) ([]EntityLink, error) {
	return items[EntityLink](ctx, s.c, "/entities/"+typeID+"/"+url.PathEscape(entityID)+"/relationships", nil)
}

// EffectiveSchema resolves one attribute's dependency-adjusted state for an
// entity (required, restricted, allowed values).
func (s *EntitiesService) EffectiveSchema(ctx context.Context, typeID, entityID, attributeID string) (*EffectiveSchema, error) {
	var out EffectiveSchema
	if err := s.c.do(ctx, http.MethodGet, "/entities/"+typeID+"/"+url.PathEscape(entityID)+"/attributes/"+attributeID+"/effective-schema", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RelationshipRequirements reports an entity's unmet cardinality minimums.
func (s *EntitiesService) RelationshipRequirements(ctx context.Context, typeID, entityID string) ([]RelationshipRequirement, error) {
	return items[RelationshipRequirement](ctx, s.c, "/entities/"+typeID+"/"+url.PathEscape(entityID)+"/relationship-requirements", nil)
}

// Completeness scores one entity against its effective required schema.
func (s *EntitiesService) Completeness(ctx context.Context, typeID, entityID string) (*Completeness, error) {
	var out Completeness
	if err := s.c.do(ctx, http.MethodGet, "/entities/"+typeID+"/"+url.PathEscape(entityID)+"/completeness", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ApplyDefaults seeds the declared defaults the entity does not hold,
// resolving a dynamic default at the moment of the call. An attribute that
// already holds a base-scope value is left alone, and a computed attribute is
// skipped.
func (s *EntitiesService) ApplyDefaults(ctx context.Context, typeID, entityID string) (*AppliedDefaults, error) {
	var out AppliedDefaults
	if err := s.c.do(ctx, http.MethodPost, "/entities/"+typeID+"/"+url.PathEscape(entityID)+"/apply-defaults", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Remove archives an entity's values and unlinks its relationships.
func (s *EntitiesService) Remove(ctx context.Context, typeID, entityID string) error {
	return s.c.do(ctx, http.MethodDelete, "/entities/"+typeID+"/"+url.PathEscape(entityID), nil, nil, nil)
}

// Grid projects the chosen attributes as columns for a page of entities.
func (s *EntitiesService) Grid(ctx context.Context, typeID string, attributes []string, opts ...ListOptions) (*GridResult, error) {
	q := url.Values{}
	if len(attributes) > 0 {
		q.Set("attributes", strings.Join(attributes, ","))
	}
	firstOpts(opts).apply(q)
	var out GridResult
	if err := s.c.do(ctx, http.MethodGet, "/entities/"+typeID+"/grid", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Facets returns value counts for the chosen attributes over the current
// result set (optionally narrowed by an FQL query).
func (s *EntitiesService) Facets(ctx context.Context, typeID string, attributes []string, query string) (*Facets, error) {
	q := url.Values{}
	if len(attributes) > 0 {
		q.Set("attributes", strings.Join(attributes, ","))
	}
	if query != "" {
		q.Set("query", query)
	}
	var out Facets
	if err := s.c.do(ctx, http.MethodGet, "/entities/"+typeID+"/facets", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ImportInput describes a CSV import against one type.
type ImportInput struct {
	// CSV is the raw file content (a header row plus data rows).
	CSV io.Reader
	// KeyColumn names the CSV column holding each row's entity id.
	KeyColumn string
	// Mapping maps a CSV column name to an attribute internal name.
	Mapping map[string]string
	// Mode is "best_effort" (default) or "transactional".
	Mode string
	// DryRun validates every row and writes nothing.
	DryRun bool
}

// Import loads a CSV into a type's entities and returns the run report.
func (s *EntitiesService) Import(ctx context.Context, typeID string, in ImportInput) (*ImportReport, error) {
	mapping, err := json.Marshal(map[string]any{
		"key_column": in.KeyColumn,
		"mapping":    in.Mapping,
		"mode":       in.Mode,
		"dry_run":    in.DryRun,
	})
	if err != nil {
		return nil, err
	}
	var out ImportReport
	if err := s.c.doMultipart(ctx, "/entities/"+typeID+"/import",
		map[string]string{"mapping": string(mapping)}, "file", "import.csv", "text/csv", in.CSV, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ExportOptions restrict which entities and columns export.
type ExportOptions struct {
	// Attributes chooses the columns (internal names); empty exports all.
	Attributes []string
	// Query (FQL) or EntityIDs restrict the rows; both empty exports all.
	Query     string
	EntityIDs []string
}

// Export streams a type's entities as CSV and parses them into columns + rows.
func (s *EntitiesService) Export(ctx context.Context, typeID string, opts ...ExportOptions) (*ExportResult, error) {
	o := ExportOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	q := url.Values{}
	if len(o.Attributes) > 0 {
		q.Set("attributes", strings.Join(o.Attributes, ","))
	}
	if o.Query != "" {
		q.Set("query", o.Query)
	}
	if len(o.EntityIDs) > 0 {
		q.Set("entity_ids", strings.Join(o.EntityIDs, ","))
	}
	raw, _, err := s.c.doRaw(ctx, "/entities/"+typeID+"/export", q)
	if err != nil {
		return nil, err
	}
	records, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil {
		return nil, err
	}
	out := &ExportResult{}
	if len(records) > 0 {
		out.Columns = records[0]
		out.Rows = records[1:]
	}
	return out, nil
}

// UploadMedia uploads a file against a media attribute and returns the stored
// value. mimeType may be empty (the server sniffs it).
func (s *EntitiesService) UploadMedia(ctx context.Context, typeID, entityID, attributeID, filename, mimeType string, file io.Reader) (*AttributeValue, error) {
	var out AttributeValue
	if err := s.c.doMultipart(ctx,
		"/entities/"+typeID+"/"+url.PathEscape(entityID)+"/attributes/"+attributeID+"/media",
		nil, "file", filename, mimeType, file, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AsOf reads an entity's values as they stood at a timestamp (RFC3339).
// at is required; the server has no default point in time.
//
// Use AsOfRevision when you also need the revision's id, seq or label.
func (s *EntitiesService) AsOf(ctx context.Context, typeID, entityID, at string) ([]RevisionValue, error) {
	rev, err := s.AsOfRevision(ctx, typeID, entityID, at)
	if err != nil {
		return nil, err
	}
	return rev.Values, nil
}

// AsOfRevision reads the whole revision an entity stood at, at a timestamp
// (RFC3339). at is required.
func (s *EntitiesService) AsOfRevision(ctx context.Context, typeID, entityID, at string) (*EntityRevision, error) {
	if at == "" {
		// The server answers 422 for a missing at, which reads as though the
		// caller's data is wrong. Say what is actually missing, without a
		// round trip.
		return nil, fmt.Errorf("flexitype: AsOf requires a timestamp (RFC3339)")
	}
	// The endpoint returns one revision object, not an {"items":[...]} list.
	// Decoding it through the list helper found no "items" key, so it
	// returned an empty slice and a nil error for every call.
	var out EntityRevision
	q := url.Values{"at": {at}}
	if err := s.c.do(ctx, http.MethodGet,
		"/entities/"+typeID+"/"+url.PathEscape(entityID)+"/as-of", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Revisions lists an entity's captured revisions.
func (s *EntitiesService) Revisions(ctx context.Context, typeID, entityID string) ([]EntityRevision, error) {
	return items[EntityRevision](ctx, s.c, "/entities/"+typeID+"/"+url.PathEscape(entityID)+"/revisions", nil)
}

// CreateRevision captures an entity's current values as a new revision.
func (s *EntitiesService) CreateRevision(ctx context.Context, typeID, entityID, label string) (*EntityRevision, error) {
	var out EntityRevision
	if err := s.c.do(ctx, http.MethodPost, "/entities/"+typeID+"/"+url.PathEscape(entityID)+"/revisions", nil, map[string]string{"label": label}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DownloadMedia streams a stored media object by its key, returning the bytes
// and content type.
func (c *Client) DownloadMedia(ctx context.Context, objectKey string) ([]byte, string, error) {
	return c.doRaw(ctx, "/media/"+url.PathEscape(objectKey), nil)
}
