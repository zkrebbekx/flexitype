package client

import (
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/url"
)

// AttributesService operates on attribute definitions.
type AttributesService struct{ c *Client }

// CreateAttributeInput creates an attribute definition. Constraints, Computed
// and DefaultValue are raw JSON so any shape the server accepts passes through;
// build them with the Constraint / ComputedSpec / DefaultValue types or your
// own encoding.
type CreateAttributeInput struct {
	TypeDefinitionID string          `json:"type_definition_id"`
	InternalName     string          `json:"internal_name"`
	DisplayName      string          `json:"display_name"`
	Description      string          `json:"description,omitempty"`
	DataType         string          `json:"data_type"`
	Required         bool            `json:"required,omitempty"`
	MultiValued      bool            `json:"multi_valued,omitempty"`
	Unique           bool            `json:"unique,omitempty"`
	Localizable      bool            `json:"localizable,omitempty"`
	Scopable         bool            `json:"scopable,omitempty"`
	UnitFamilyID     string          `json:"unit_family_id,omitempty"`
	DisplayUnit      string          `json:"display_unit,omitempty"`
	Computed         json.RawMessage `json:"computed,omitempty"`
	Constraints      json.RawMessage `json:"constraints,omitempty"`
	DefaultValue     json.RawMessage `json:"default_value,omitempty"`
	Group            string          `json:"group,omitempty"`
	SortOrder        int             `json:"sort_order,omitempty"`
	HelpText         string          `json:"help_text,omitempty"`
}

// UpdateAttributeInput mutates an attribute definition.
type UpdateAttributeInput struct {
	DisplayName  string          `json:"display_name"`
	Description  string          `json:"description,omitempty"`
	Required     bool            `json:"required,omitempty"`
	MultiValued  bool            `json:"multi_valued,omitempty"`
	Unique       bool            `json:"unique,omitempty"`
	Localizable  bool            `json:"localizable,omitempty"`
	Scopable     bool            `json:"scopable,omitempty"`
	UnitFamilyID string          `json:"unit_family_id,omitempty"`
	DisplayUnit  string          `json:"display_unit,omitempty"`
	Computed     json.RawMessage `json:"computed,omitempty"`
	Constraints  json.RawMessage `json:"constraints,omitempty"`
	DefaultValue json.RawMessage `json:"default_value,omitempty"`
	Group        string          `json:"group,omitempty"`
	SortOrder    int             `json:"sort_order,omitempty"`
	HelpText     string          `json:"help_text,omitempty"`
}

// ListAttributesOptions filters an attribute listing.
type ListAttributesOptions struct {
	ListOptions
	TypeDefinitionID string
	InternalNames    []string
	DataTypes        []string
	IncludeArchived  bool
}

func (o ListAttributesOptions) values() url.Values {
	q := url.Values{}
	if o.TypeDefinitionID != "" {
		q.Set("type_definition_id", o.TypeDefinitionID)
	}
	for _, n := range o.InternalNames {
		q.Add("internal_name", n)
	}
	for _, d := range o.DataTypes {
		q.Add("data_type", d)
	}
	if o.IncludeArchived {
		q.Set("include_archived", "true")
	}
	return q
}

// List returns one page of attribute definitions.
func (s *AttributesService) List(ctx context.Context, opts ...ListAttributesOptions) (*Page[AttributeDefinition], error) {
	o := ListAttributesOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	return listPage[AttributeDefinition](ctx, s.c, "/attributes", o.values(), o.ListOptions)
}

// All iterates every attribute across pages.
func (s *AttributesService) All(ctx context.Context, opts ...ListAttributesOptions) iter.Seq2[AttributeDefinition, error] {
	o := ListAttributesOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	return paginate(func(cursor string) (*Page[AttributeDefinition], error) {
		o.Cursor = cursor
		return s.List(ctx, o)
	})
}

// Get loads one attribute definition.
func (s *AttributesService) Get(ctx context.Context, id string) (*AttributeDefinition, error) {
	var out AttributeDefinition
	return &out, s.c.do(ctx, http.MethodGet, "/attributes/"+id, nil, nil, &out)
}

// Create creates an attribute definition.
func (s *AttributesService) Create(ctx context.Context, in CreateAttributeInput) (*AttributeDefinition, error) {
	var out AttributeDefinition
	return &out, s.c.do(ctx, http.MethodPost, "/attributes", nil, in, &out)
}

// Update mutates an attribute definition.
func (s *AttributesService) Update(ctx context.Context, id string, in UpdateAttributeInput) (*AttributeDefinition, error) {
	var out AttributeDefinition
	return &out, s.c.do(ctx, http.MethodPatch, "/attributes/"+id, nil, in, &out)
}

// Archive soft-deletes an attribute definition.
func (s *AttributesService) Archive(ctx context.Context, id string) (*AttributeDefinition, error) {
	var out AttributeDefinition
	return &out, s.c.do(ctx, http.MethodPost, "/attributes/"+id+"/archive", nil, nil, &out)
}

// Restore reverses an archive.
func (s *AttributesService) Restore(ctx context.Context, id string) (*AttributeDefinition, error) {
	var out AttributeDefinition
	return &out, s.c.do(ctx, http.MethodPost, "/attributes/"+id+"/restore", nil, nil, &out)
}

// ValidateValue dry-runs a raw JSON value against an attribute — parse, type
// check and constraints, nothing persisted. It returns nil when valid and a
// *APIError (code VALIDATION) otherwise.
func (s *AttributesService) ValidateValue(ctx context.Context, id string, value json.RawMessage) error {
	return s.c.do(ctx, http.MethodPost, "/attributes/"+id+"/validate-value", nil, map[string]any{"value": value}, nil)
}
