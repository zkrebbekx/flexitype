package schema

import (
	"context"
	"encoding/json"
	"fmt"

	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// CloneInput names the source type and the new type's identity.
type CloneInput struct {
	SourceTypeID string
	InternalName string
	DisplayName  string
}

// CloneResult is the created type plus what was copied.
type CloneResult struct {
	Type         *domaintypedef.Snapshot `json:"type"`
	Attributes   int                     `json:"attributes"`
	Dependencies int                     `json:"dependencies"`
}

// Clone creates an independent new type from an existing one, copying its
// declared attributes (every field, including quantity unit family, computed
// spec and constraints) and the dependencies whose endpoints both belong to
// the source type. It does NOT copy values, and it does NOT copy the source's
// hierarchy position — the clone is a fresh root. Internal-name collisions are
// rejected by the underlying create paths.
func (i *Interactor) Clone(ctx context.Context, in CloneInput) (*CloneResult, error) {
	if in.InternalName == "" {
		return nil, domainerrors.NewValidation("clone requires a new internal name")
	}
	source, err := i.typeDefs.Get(ctx, in.SourceTypeID)
	if err != nil {
		return nil, err
	}

	displayName := in.DisplayName
	if displayName == "" {
		displayName = source.DisplayName
	}
	// A fresh root: extends is intentionally left empty (hierarchy not copied).
	created, err := i.typeDefs.Create(ctx, apptypedef.CreateInput{
		InternalName: in.InternalName,
		DisplayName:  displayName,
		Description:  source.Description,
	})
	if err != nil {
		return nil, err
	}

	// Copy declared attributes; remember source-attr-id → new-attr-id so
	// intra-type dependencies can be rewired onto the clone.
	newAttrIDBySourceID := map[string]string{}
	attrCount := 0
	cursor := (*string)(nil)
	for {
		out, err := i.attrs.ListByTypeDefinition(ctx, source.ID.String(), db.PageArgs{Limit: limitPtr(200), Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, a := range out.Items {
			var computed json.RawMessage
			if a.Computed != nil {
				if raw, err := json.Marshal(a.Computed); err == nil {
					computed = raw
				}
			}
			var constraints json.RawMessage
			if raw, err := json.Marshal(a.Constraints); err == nil && string(raw) != "null" {
				constraints = raw
			}
			var defaultValue json.RawMessage
			if a.DefaultValue != nil {
				if raw, err := json.Marshal(a.DefaultValue); err == nil {
					defaultValue = raw
				}
			}
			// Same tenant, so the source's unit-family id is valid verbatim.
			newAttr, err := i.attrs.Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: created.ID.String(),
				InternalName:     a.InternalName,
				DisplayName:      a.DisplayName,
				Description:      a.Description,
				DataType:         a.DataType.String(),
				Required:         a.Required,
				MultiValued:      a.MultiValued,
				Unique:           a.Unique,
				Localizable:      a.Localizable,
				Scopable:         a.Scopable,
				UnitFamilyID:     a.UnitFamilyID,
				DisplayUnit:      a.DisplayUnit,
				Computed:         computed,
				Constraints:      constraints,
				DefaultValue:     defaultValue,
				Group:            a.Group,
				SortOrder:        a.SortOrder,
				HelpText:         a.HelpText,
			})
			if err != nil {
				return nil, fmt.Errorf("clone attribute %q: %w", a.InternalName, err)
			}
			newAttrIDBySourceID[a.ID.String()] = newAttr.ID.String()
			attrCount++
		}
		if !out.PageInfo.HasNextPage {
			break
		}
		cursor = out.PageInfo.NextCursor
	}

	// Copy dependencies whose source and target attributes both belong to the
	// cloned type, rewired onto the new attribute ids.
	depCount := 0
	depCursor := (*string)(nil)
	for {
		out, err := i.deps.List(ctx, appdependency.ListInput{Page: db.PageArgs{Limit: limitPtr(200), Cursor: depCursor}})
		if err != nil {
			return nil, err
		}
		for _, d := range out.Items {
			srcNew, sOK := newAttrIDBySourceID[d.SourceAttributeID.String()]
			tgtNew, tOK := newAttrIDBySourceID[d.TargetAttributeID.String()]
			if !sOK || !tOK {
				continue // cross-type dependency; not part of this type's clone
			}
			var conditions json.RawMessage
			if raw, err := json.Marshal(d.Conditions); err == nil {
				conditions = raw
			}
			var effect json.RawMessage
			if raw, err := json.Marshal(d.Effect); err == nil {
				effect = raw
			}
			if _, err := i.deps.Create(ctx, appdependency.CreateInput{
				SourceAttributeID: srcNew,
				TargetAttributeID: tgtNew,
				Conditions:        conditions,
				Effect:            effect,
				Description:       d.Description,
			}); err != nil {
				return nil, fmt.Errorf("clone dependency: %w", err)
			}
			depCount++
		}
		if !out.PageInfo.HasNextPage {
			break
		}
		depCursor = out.PageInfo.NextCursor
	}

	return &CloneResult{Type: created, Attributes: attrCount, Dependencies: depCount}, nil
}
