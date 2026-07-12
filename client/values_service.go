package client

import (
	"context"
	"encoding/json"
	"net/http"
)

// ValuesService operates on attribute values.
type ValuesService struct{ c *Client }

// SetValueInput sets one value. Value is the raw JSON payload; its shape
// follows the attribute's data type (a decimal is a JSON string "9.99", a
// quantity is {"magnitude","unit"}, a bool is true). Locale/Channel apply to
// localizable/scopable attributes.
type SetValueInput struct {
	AttributeDefinitionID string          `json:"attribute_definition_id"`
	EntityID              string          `json:"entity_id"`
	TypeDefinitionID      string          `json:"type_definition_id,omitempty"`
	Locale                string          `json:"locale,omitempty"`
	Channel               string          `json:"channel,omitempty"`
	Value                 json.RawMessage `json:"value"`
}

// Set writes one value (create or update; single-valued attributes upsert).
func (s *ValuesService) Set(ctx context.Context, in SetValueInput) (*AttributeValue, error) {
	var out AttributeValue
	return &out, s.c.do(ctx, http.MethodPost, "/values", nil, in, &out)
}

// SetBatch writes many values atomically in one unit of work.
func (s *ValuesService) SetBatch(ctx context.Context, in []SetValueInput) ([]AttributeValue, error) {
	var out struct {
		Items []AttributeValue `json:"items"`
	}
	if err := s.c.do(ctx, http.MethodPost, "/values/batch", nil, map[string]any{"items": in}, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// Get loads one value by id.
func (s *ValuesService) Get(ctx context.Context, id string) (*AttributeValue, error) {
	var out AttributeValue
	return &out, s.c.do(ctx, http.MethodGet, "/values/"+id, nil, nil, &out)
}

// Remove archives one value by id.
func (s *ValuesService) Remove(ctx context.Context, id string) error {
	return s.c.do(ctx, http.MethodDelete, "/values/"+id, nil, nil, nil)
}
