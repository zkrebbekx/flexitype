package client

import (
	"context"
	"iter"
	"net/http"
	"net/url"
)

// TypesService operates on type definitions.
type TypesService struct{ c *Client }

// CreateTypeInput creates a type definition. ExtendsID makes it a subtype.
type CreateTypeInput struct {
	InternalName string `json:"internal_name"`
	DisplayName  string `json:"display_name"`
	Description  string `json:"description,omitempty"`
	ExtendsID    string `json:"extends_id,omitempty"`
}

// UpdateTypeInput mutates a type's display fields.
type UpdateTypeInput struct {
	DisplayName string `json:"display_name"`
	Description string `json:"description,omitempty"`
}

// ListTypesOptions filters a type listing.
type ListTypesOptions struct {
	ListOptions
	InternalNames   []string
	IncludeArchived bool
}

func (o ListTypesOptions) values() url.Values {
	q := url.Values{}
	for _, n := range o.InternalNames {
		q.Add("internal_name", n)
	}
	if o.IncludeArchived {
		q.Set("include_archived", "true")
	}
	return q
}

// List returns one page of type definitions.
func (s *TypesService) List(ctx context.Context, opts ...ListTypesOptions) (*Page[TypeDefinition], error) {
	o := ListTypesOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	return listPage[TypeDefinition](ctx, s.c, "/type-definitions", o.values(), o.ListOptions)
}

// All iterates every type definition across pages.
func (s *TypesService) All(ctx context.Context, opts ...ListTypesOptions) iter.Seq2[TypeDefinition, error] {
	o := ListTypesOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	return paginate(func(cursor string) (*Page[TypeDefinition], error) {
		o.Cursor = cursor
		return s.List(ctx, o)
	})
}

// Get loads one type definition.
func (s *TypesService) Get(ctx context.Context, id string) (*TypeDefinition, error) {
	var out TypeDefinition
	return &out, s.c.do(ctx, http.MethodGet, "/type-definitions/"+id, nil, nil, &out)
}

// Create creates a type definition.
func (s *TypesService) Create(ctx context.Context, in CreateTypeInput) (*TypeDefinition, error) {
	var out TypeDefinition
	return &out, s.c.do(ctx, http.MethodPost, "/type-definitions", nil, in, &out)
}

// Update mutates a type definition.
func (s *TypesService) Update(ctx context.Context, id string, in UpdateTypeInput) (*TypeDefinition, error) {
	var out TypeDefinition
	return &out, s.c.do(ctx, http.MethodPatch, "/type-definitions/"+id, nil, in, &out)
}

// Archive soft-deletes a type definition.
func (s *TypesService) Archive(ctx context.Context, id string) (*TypeDefinition, error) {
	var out TypeDefinition
	return &out, s.c.do(ctx, http.MethodPost, "/type-definitions/"+id+"/archive", nil, nil, &out)
}

// Restore reverses an archive.
func (s *TypesService) Restore(ctx context.Context, id string) (*TypeDefinition, error) {
	var out TypeDefinition
	return &out, s.c.do(ctx, http.MethodPost, "/type-definitions/"+id+"/restore", nil, nil, &out)
}

// CloneResult reports a clone's outcome.
type CloneResult struct {
	Type         TypeDefinition `json:"type"`
	Attributes   int            `json:"attributes"`
	Dependencies int            `json:"dependencies"`
}

// Clone copies a type's attributes and intra-type dependencies into a fresh
// root type (not its values, not its hierarchy position).
func (s *TypesService) Clone(ctx context.Context, id, newInternalName, newDisplayName string) (*CloneResult, error) {
	body := map[string]string{"internal_name": newInternalName, "display_name": newDisplayName}
	var out CloneResult
	return &out, s.c.do(ctx, http.MethodPost, "/type-definitions/"+id+"/clone", nil, body, &out)
}

// Attributes lists a type's own (declared) attributes.
func (s *TypesService) Attributes(ctx context.Context, id string, opts ...ListOptions) (*Page[AttributeDefinition], error) {
	return listPage[AttributeDefinition](ctx, s.c, "/type-definitions/"+id+"/attributes", url.Values{}, firstOpts(opts))
}

// EffectiveAttributes returns the type's full effective schema (inherited
// attributes included), each tagged with the type that declares it.
func (s *TypesService) EffectiveAttributes(ctx context.Context, id string) ([]EffectiveAttribute, error) {
	return items[EffectiveAttribute](ctx, s.c, "/type-definitions/"+id+"/effective-attributes", nil)
}

// Children returns a type's direct subtypes.
func (s *TypesService) Children(ctx context.Context, id string) ([]TypeDefinition, error) {
	return items[TypeDefinition](ctx, s.c, "/type-definitions/"+id+"/children", nil)
}

// Completeness aggregates completeness across a type's entities.
func (s *TypesService) Completeness(ctx context.Context, id string) (*TypeCompleteness, error) {
	var out TypeCompleteness
	return &out, s.c.do(ctx, http.MethodGet, "/type-definitions/"+id+"/completeness", nil, nil, &out)
}

// MatchRules lists the duplicate-detection rules for a type.
func (s *TypesService) MatchRules(ctx context.Context, id string) ([]MatchRule, error) {
	return items[MatchRule](ctx, s.c, "/type-definitions/"+id+"/match-rules", nil)
}

// CreateMatchRuleInput adds a duplicate-detection rule.
type CreateMatchRuleInput struct {
	AttributeDefinitionID string  `json:"attribute_definition_id"`
	Strategy              string  `json:"strategy"`
	Threshold             float64 `json:"threshold,omitempty"`
}

// CreateMatchRule adds a duplicate-detection rule to a type.
func (s *TypesService) CreateMatchRule(ctx context.Context, id string, in CreateMatchRuleInput) (*MatchRule, error) {
	var out MatchRule
	return &out, s.c.do(ctx, http.MethodPost, "/type-definitions/"+id+"/match-rules", nil, in, &out)
}
