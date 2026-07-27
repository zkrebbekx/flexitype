// Package fieldacl applies the per-attribute access policy to every surface
// that returns or accepts attribute values.
//
// The value API redacts inline, because it holds the attribute repository it
// needs. The other surfaces — revisions, the activity log, the events feed,
// media download and duplicate detection — return values that were captured
// or serialized elsewhere, so they resolve the attribute identity here and
// then drop or mask what the principal may not read.
//
// A masked value is replaced rather than removed wherever the surrounding
// record must stay well-formed. The audit skeleton of an activity entry and
// the envelope of a feed event both remain visible; only the value fields
// inside them are masked, and a Redacted marker records that a mask was
// applied. A caller therefore cannot mistake a masked record for one that
// never carried a value.
package fieldacl

import (
	"context"
	"encoding/json"

	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// RedactedMarker is the JSON field added to a payload whose value fields were
// masked. Consumers test it to tell a masked record from an absent value.
const RedactedMarker = "redacted"

// valueFields are the payload keys that carry an attribute value. They cover
// the value events (Set.value, Updated.old_value/new_value, Removed.value)
// and the value snapshots the activity log stores.
var valueFields = []string{"value", "old_value", "new_value"}

// Resolver answers read and write questions about attributes identified by
// ID rather than by internal name. It batches the definition lookups it
// needs, so redacting a page costs one round trip.
type Resolver struct {
	attrs domainattribute.Repository
}

// New wires a resolver over the request's attribute repository.
func New(attrs domainattribute.Repository) *Resolver {
	return &Resolver{attrs: attrs}
}

// Names returns the internal name of each attribute definition, keyed by ID
// string. Unknown IDs are absent from the result.
func (r *Resolver) Names(ctx context.Context, ids []valueobjects.AttributeDefinitionID) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	defs, err := r.attrs.GetMany(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(defs))
	for _, d := range defs {
		out[d.ID().String()] = d.InternalName()
	}
	return out, nil
}

// Readable reports, per attribute definition ID string, whether the principal
// on ctx may read that attribute's values. An ID the repository does not know
// is reported as unreadable: an unresolvable attribute is not one the policy
// can be shown to permit.
func (r *Resolver) Readable(ctx context.Context, ids []valueobjects.AttributeDefinitionID) (map[string]bool, error) {
	access := uow.AccessFromContext(ctx)
	out := make(map[string]bool, len(ids))
	if access.Admin {
		for _, id := range ids {
			out[id.String()] = true
		}
		return out, nil
	}
	names, err := r.Names(ctx, dedupe(ids))
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		name, ok := names[id.String()]
		out[id.String()] = ok && access.CanRead(name)
	}
	return out, nil
}

// Writable reports, per attribute definition ID string, whether the principal
// on ctx may write that attribute's values. An ID the repository does not know
// is reported as unwritable, for the same reason Readable reports it as
// unreadable.
func (r *Resolver) Writable(ctx context.Context, ids []valueobjects.AttributeDefinitionID) (map[string]bool, error) {
	access := uow.AccessFromContext(ctx)
	out := make(map[string]bool, len(ids))
	if access.Admin {
		for _, id := range ids {
			out[id.String()] = true
		}
		return out, nil
	}
	names, err := r.Names(ctx, dedupe(ids))
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		name, ok := names[id.String()]
		out[id.String()] = ok && access.CanWrite(name)
	}
	return out, nil
}

// CanRead reports whether the principal may read one attribute's values.
func (r *Resolver) CanRead(ctx context.Context, id valueobjects.AttributeDefinitionID) (bool, error) {
	m, err := r.Readable(ctx, []valueobjects.AttributeDefinitionID{id})
	if err != nil {
		return false, err
	}
	return m[id.String()], nil
}

// CanWrite reports whether the principal may write one attribute's values.
func (r *Resolver) CanWrite(ctx context.Context, id valueobjects.AttributeDefinitionID) (bool, error) {
	m, err := r.Writable(ctx, []valueobjects.AttributeDefinitionID{id})
	if err != nil {
		return false, err
	}
	return m[id.String()], nil
}

// AnyReadable reports whether the principal may read at least one of the
// attributes. It answers the media-download question: a blob is reachable
// when some value referencing it is readable.
func (r *Resolver) AnyReadable(ctx context.Context, ids []valueobjects.AttributeDefinitionID) (bool, error) {
	if len(ids) == 0 {
		return false, nil
	}
	m, err := r.Readable(ctx, ids)
	if err != nil {
		return false, err
	}
	for _, ok := range m {
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// PayloadAttribute returns the attribute definition ID a JSON payload names,
// or false when the payload carries no attribute identity. It is how the feed
// and the activity log find the attribute behind a serialized record without
// depending on the concrete event or snapshot type.
func PayloadAttribute(raw json.RawMessage) (valueobjects.AttributeDefinitionID, bool) {
	if len(raw) == 0 {
		return valueobjects.AttributeDefinitionID{}, false
	}
	var probe struct {
		AttributeDefinitionID string `json:"attribute_definition_id"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || probe.AttributeDefinitionID == "" {
		return valueobjects.AttributeDefinitionID{}, false
	}
	id, err := valueobjects.ParseAttributeDefinitionID(probe.AttributeDefinitionID)
	if err != nil {
		return valueobjects.AttributeDefinitionID{}, false
	}
	return id, true
}

// MaskPayload replaces every value field of a JSON payload with null and adds
// the redaction marker. The rest of the payload — identifiers, scope,
// timestamps — is preserved, so a consumer still sees that the change
// happened and to which attribute.
//
// It returns the payload unchanged when the payload is not a JSON object, so
// an unexpected shape fails closed on the caller's side rather than here:
// callers only mask payloads whose attribute they resolved, and resolving
// requires the object form.
func MaskPayload(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw, nil
	}
	masked := false
	for _, f := range valueFields {
		if _, ok := obj[f]; ok {
			obj[f] = json.RawMessage("null")
			masked = true
		}
	}
	if !masked {
		return raw, nil
	}
	obj[RedactedMarker] = json.RawMessage("true")
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// dedupe collapses repeated IDs so a page of many values over few attributes
// costs one lookup per distinct attribute.
func dedupe(ids []valueobjects.AttributeDefinitionID) []valueobjects.AttributeDefinitionID {
	seen := make(map[string]bool, len(ids))
	out := make([]valueobjects.AttributeDefinitionID, 0, len(ids))
	for _, id := range ids {
		if seen[id.String()] {
			continue
		}
		seen[id.String()] = true
		out = append(out, id)
	}
	return out
}
