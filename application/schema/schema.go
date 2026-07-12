// Package schema exports and imports a tenant's schema — its type
// definitions, attribute definitions, relationship definitions and
// dependencies — as one JSON bundle keyed entirely by internal name (never
// by ID), so a bundle is portable across instances. Import is idempotent:
// it creates only what is missing (matched by internal name), so re-running
// a bundle is safe and a partial import can be completed by re-running.
package schema

import (
	"context"
	"encoding/json"
	"fmt"

	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// BundleVersion is the current bundle schema version.
const BundleVersion = 1

// Bundle is a portable, name-keyed snapshot of a tenant's schema.
type Bundle struct {
	Version                 int                      `json:"version"`
	UnitFamilies            []UnitFamily             `json:"unit_families,omitempty"`
	Types                   []Type                   `json:"types"`
	Attributes              []Attribute              `json:"attributes"`
	RelationshipDefinitions []RelationshipDefinition `json:"relationship_definitions"`
	Dependencies            []Dependency             `json:"dependencies"`
}

// UnitFamily is a quantity attribute's unit family, referenced from
// attributes by Name so a bundle stays ID-free and portable.
type UnitFamily struct {
	Name     string             `json:"name"`
	BaseUnit string             `json:"base_unit"`
	Units    map[string]float64 `json:"units"`
}

// Type is a type definition; Extends names the parent type (empty for a
// root).
type Type struct {
	InternalName string `json:"internal_name"`
	DisplayName  string `json:"display_name"`
	Description  string `json:"description,omitempty"`
	Extends      string `json:"extends,omitempty"`
}

// Attribute is an attribute definition anchored to its declaring type by
// name. UnitFamily names a bundle unit family (for quantity attributes);
// Computed carries the computed-attribute spec verbatim.
type Attribute struct {
	Type         string          `json:"type"`
	InternalName string          `json:"internal_name"`
	DisplayName  string          `json:"display_name"`
	Description  string          `json:"description,omitempty"`
	DataType     string          `json:"data_type"`
	Required     bool            `json:"required"`
	MultiValued  bool            `json:"multi_valued"`
	Unique       bool            `json:"unique"`
	Localizable  bool            `json:"localizable,omitempty"`
	Scopable     bool            `json:"scopable,omitempty"`
	UnitFamily   string          `json:"unit_family,omitempty"`
	DisplayUnit  string          `json:"display_unit,omitempty"`
	Computed     json.RawMessage `json:"computed,omitempty"`
	Constraints  json.RawMessage `json:"constraints,omitempty"`
	DefaultValue json.RawMessage `json:"default_value,omitempty"`
	Group        string          `json:"group,omitempty"`
	SortOrder    int             `json:"sort_order,omitempty"`
	HelpText     string          `json:"help_text,omitempty"`
}

// RelationshipDefinition references its endpoint types by name.
type RelationshipDefinition struct {
	InternalName        string `json:"internal_name"`
	DisplayName         string `json:"display_name"`
	Description         string `json:"description,omitempty"`
	Kind                string `json:"kind"`
	ParentType          string `json:"parent_type"`
	ChildType           string `json:"child_type"`
	ParentLabel         string `json:"parent_label,omitempty"`
	ChildLabel          string `json:"child_label,omitempty"`
	ParentVersionPolicy string `json:"parent_version_policy"`
	ChildVersionPolicy  string `json:"child_version_policy"`
	MinChildren         *int   `json:"min_children,omitempty"`
	MaxChildren         *int   `json:"max_children,omitempty"`
	MinParents          *int   `json:"min_parents,omitempty"`
	MaxParents          *int   `json:"max_parents,omitempty"`
}

// Dependency references its two attributes by type + attribute name.
type Dependency struct {
	SourceType      string          `json:"source_type"`
	SourceAttribute string          `json:"source_attribute"`
	TargetType      string          `json:"target_type"`
	TargetAttribute string          `json:"target_attribute"`
	Conditions      json.RawMessage `json:"conditions"`
	Effect          json.RawMessage `json:"effect"`
	Description     string          `json:"description,omitempty"`
}

// Interactor orchestrates the schema usecases over the aggregate
// interactors, reusing their validated create paths on import.
type Interactor struct {
	typeDefs *apptypedef.Interactor
	attrs    *appattribute.Interactor
	rels     *apprelationship.Interactor
	deps     *appdependency.Interactor
	// units carries quantity unit families through export/import and clone;
	// nil when unit families are disabled.
	units *appunit.Interactor
}

// NewInteractor wires the schema usecases. units (nil-able) makes quantity
// unit families portable in bundles.
func NewInteractor(typeDefs *apptypedef.Interactor, attrs *appattribute.Interactor, rels *apprelationship.Interactor, deps *appdependency.Interactor, units *appunit.Interactor) *Interactor {
	return &Interactor{typeDefs: typeDefs, attrs: attrs, rels: rels, deps: deps, units: units}
}

func limitPtr(n int) *int { return &n }

// Export gathers the caller's tenant schema into a bundle.
func (i *Interactor) Export(ctx context.Context) (*Bundle, error) {
	bundle := &Bundle{Version: BundleVersion}

	// Unit families first — quantity attributes reference them by name.
	unitNameByID := map[string]string{}
	if i.units != nil {
		families, err := i.units.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, f := range families {
			unitNameByID[f.ID.String()] = f.Name
			bundle.UnitFamilies = append(bundle.UnitFamilies, UnitFamily{
				Name: f.Name, BaseUnit: f.BaseUnit, Units: f.Units,
			})
		}
	}

	// Types (entity kind only — relationship attribute-set companions are
	// recreated implicitly when their relationship definition imports).
	typeByID := map[string]string{} // type id -> internal name
	var types []domaintypedef.Snapshot
	cursor := (*string)(nil)
	for {
		out, err := i.typeDefs.List(ctx, apptypedef.ListInput{Page: db.PageArgs{Limit: limitPtr(200), Cursor: cursor}})
		if err != nil {
			return nil, err
		}
		types = append(types, out.Items...)
		if !out.PageInfo.HasNextPage {
			break
		}
		cursor = out.PageInfo.NextCursor
	}
	for _, t := range types {
		typeByID[t.ID.String()] = t.InternalName
	}
	for _, t := range types {
		if t.Kind != domaintypedef.KindEntity {
			continue
		}
		bt := Type{InternalName: t.InternalName, DisplayName: t.DisplayName, Description: t.Description}
		if t.ExtendsID != nil {
			bt.Extends = typeByID[t.ExtendsID.String()]
		}
		bundle.Types = append(bundle.Types, bt)

		// Attributes owned by this type.
		attrCursor := (*string)(nil)
		for {
			ao, err := i.attrs.ListByTypeDefinition(ctx, t.ID.String(), db.PageArgs{Limit: limitPtr(200), Cursor: attrCursor})
			if err != nil {
				return nil, err
			}
			for _, a := range ao.Items {
				ba := Attribute{
					Type:         t.InternalName,
					InternalName: a.InternalName,
					DisplayName:  a.DisplayName,
					Description:  a.Description,
					DataType:     a.DataType.String(),
					Required:     a.Required,
					MultiValued:  a.MultiValued,
					Unique:       a.Unique,
					Localizable:  a.Localizable,
					Scopable:     a.Scopable,
					DisplayUnit:  a.DisplayUnit,
					Group:        a.Group,
					SortOrder:    a.SortOrder,
					HelpText:     a.HelpText,
				}
				if a.UnitFamilyID != "" {
					ba.UnitFamily = unitNameByID[a.UnitFamilyID]
				}
				if a.Computed != nil {
					if raw, err := json.Marshal(a.Computed); err == nil {
						ba.Computed = raw
					}
				}
				if raw, err := json.Marshal(a.Constraints); err == nil && string(raw) != "null" {
					ba.Constraints = raw
				}
				if a.DefaultValue != nil {
					if raw, err := json.Marshal(a.DefaultValue); err == nil {
						ba.DefaultValue = raw
					}
				}
				bundle.Attributes = append(bundle.Attributes, ba)
			}
			if !ao.PageInfo.HasNextPage {
				break
			}
			attrCursor = ao.PageInfo.NextCursor
		}
	}

	// Relationship definitions.
	relCursor := (*string)(nil)
	for {
		ro, err := i.rels.ListDefinitions(ctx, apprelationship.DefinitionListInput{Page: db.PageArgs{Limit: limitPtr(200), Cursor: relCursor}})
		if err != nil {
			return nil, err
		}
		for _, r := range ro.Items {
			bundle.RelationshipDefinitions = append(bundle.RelationshipDefinitions, RelationshipDefinition{
				InternalName:        r.InternalName,
				DisplayName:         r.DisplayName,
				Description:         r.Description,
				Kind:                string(r.Kind),
				ParentType:          typeByID[r.ParentTypeID.String()],
				ChildType:           typeByID[r.ChildTypeID.String()],
				ParentLabel:         r.ParentLabel,
				ChildLabel:          r.ChildLabel,
				ParentVersionPolicy: string(r.ParentVersionPolicy),
				ChildVersionPolicy:  string(r.ChildVersionPolicy),
				MinChildren:         r.MinChildren,
				MaxChildren:         r.MaxChildren,
				MinParents:          r.MinParents,
				MaxParents:          r.MaxParents,
			})
		}
		if !ro.PageInfo.HasNextPage {
			break
		}
		relCursor = ro.PageInfo.NextCursor
	}

	// Dependencies — resolve each attribute id to type.attr by name.
	attrRef, err := i.attributeRefs(ctx)
	if err != nil {
		return nil, err
	}
	depCursor := (*string)(nil)
	for {
		do, err := i.deps.List(ctx, appdependency.ListInput{Page: db.PageArgs{Limit: limitPtr(200), Cursor: depCursor}})
		if err != nil {
			return nil, err
		}
		for _, d := range do.Items {
			src, srcOK := attrRef[d.SourceAttributeID.String()]
			tgt, tgtOK := attrRef[d.TargetAttributeID.String()]
			if !srcOK || !tgtOK {
				continue // attribute archived or missing; skip dangling dep
			}
			bd := Dependency{
				SourceType: src.typeName, SourceAttribute: src.attrName,
				TargetType: tgt.typeName, TargetAttribute: tgt.attrName,
				Description: d.Description,
			}
			if raw, err := json.Marshal(d.Conditions); err == nil {
				bd.Conditions = raw
			}
			if raw, err := json.Marshal(d.Effect); err == nil {
				bd.Effect = raw
			}
			bundle.Dependencies = append(bundle.Dependencies, bd)
		}
		if !do.PageInfo.HasNextPage {
			break
		}
		depCursor = do.PageInfo.NextCursor
	}

	return bundle, nil
}

type attrLoc struct{ typeName, attrName string }

// attributeRefs maps every attribute id to its (type name, attribute name).
func (i *Interactor) attributeRefs(ctx context.Context) (map[string]attrLoc, error) {
	refs := map[string]attrLoc{}
	typeCursor := (*string)(nil)
	for {
		to, err := i.typeDefs.List(ctx, apptypedef.ListInput{Page: db.PageArgs{Limit: limitPtr(200), Cursor: typeCursor}})
		if err != nil {
			return nil, err
		}
		for _, t := range to.Items {
			attrCursor := (*string)(nil)
			for {
				ao, err := i.attrs.ListByTypeDefinition(ctx, t.ID.String(), db.PageArgs{Limit: limitPtr(200), Cursor: attrCursor})
				if err != nil {
					return nil, err
				}
				for _, a := range ao.Items {
					refs[a.ID.String()] = attrLoc{typeName: t.InternalName, attrName: a.InternalName}
				}
				if !ao.PageInfo.HasNextPage {
					break
				}
				attrCursor = ao.PageInfo.NextCursor
			}
		}
		if !to.PageInfo.HasNextPage {
			break
		}
		typeCursor = to.PageInfo.NextCursor
	}
	return refs, nil
}

// ImportResult reports what an import created versus skipped (already
// present), per object kind.
type ImportResult struct {
	Types                   KindCount `json:"types"`
	Attributes              KindCount `json:"attributes"`
	RelationshipDefinitions KindCount `json:"relationship_definitions"`
	Dependencies            KindCount `json:"dependencies"`
}

// KindCount tallies created and skipped objects of one kind.
type KindCount struct {
	Created int `json:"created"`
	Skipped int `json:"skipped"`
}

// Import applies a bundle idempotently: it creates objects missing by
// internal name and skips those already present, in dependency order
// (types, attributes, relationship definitions, dependencies). It is not a
// single transaction — re-running completes a partial import.
func (i *Interactor) Import(ctx context.Context, bundle *Bundle) (*ImportResult, error) {
	if bundle == nil {
		return nil, domainerrors.NewValidation("bundle is required")
	}
	if bundle.Version != BundleVersion {
		return nil, domainerrors.NewValidation("unsupported bundle version", "version", bundle.Version, "supported", BundleVersion)
	}
	res := &ImportResult{}

	// 0. Unit families — quantity attributes resolve their family by name.
	unitIDByName, err := i.importUnitFamilies(ctx, bundle.UnitFamilies)
	if err != nil {
		return nil, err
	}

	// 1. Types, parents before children.
	typeIDByName, err := i.currentTypeIDs(ctx)
	if err != nil {
		return nil, err
	}
	ordered, err := topoSortTypes(bundle.Types)
	if err != nil {
		return nil, err
	}
	for _, t := range ordered {
		if _, exists := typeIDByName[t.InternalName]; exists {
			res.Types.Skipped++
			continue
		}
		in := apptypedef.CreateInput{InternalName: t.InternalName, DisplayName: t.DisplayName, Description: t.Description}
		if t.Extends != "" {
			parentID, ok := typeIDByName[t.Extends]
			if !ok {
				return nil, domainerrors.NewValidation("type extends an unknown type", "type", t.InternalName, "extends", t.Extends)
			}
			in.ExtendsID = parentID
		}
		snap, err := i.typeDefs.Create(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("import type %q: %w", t.InternalName, err)
		}
		typeIDByName[t.InternalName] = snap.ID.String()
		res.Types.Created++
	}

	// 2. Attributes.
	attrIDs, err := i.currentAttributeIDs(ctx, typeIDByName)
	if err != nil {
		return nil, err
	}
	for _, a := range bundle.Attributes {
		typeID, ok := typeIDByName[a.Type]
		if !ok {
			return nil, domainerrors.NewValidation("attribute references an unknown type", "attribute", a.InternalName, "type", a.Type)
		}
		if _, exists := attrIDs[a.Type+"."+a.InternalName]; exists {
			res.Attributes.Skipped++
			continue
		}
		unitFamilyID := ""
		if a.UnitFamily != "" {
			id, ok := unitIDByName[a.UnitFamily]
			if !ok {
				return nil, domainerrors.NewValidation("attribute references an unknown unit family",
					"attribute", a.InternalName, "unit_family", a.UnitFamily)
			}
			unitFamilyID = id
		}
		snap, err := i.attrs.Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID,
			InternalName:     a.InternalName,
			DisplayName:      a.DisplayName,
			Description:      a.Description,
			DataType:         a.DataType,
			Required:         a.Required,
			MultiValued:      a.MultiValued,
			Unique:           a.Unique,
			Localizable:      a.Localizable,
			Scopable:         a.Scopable,
			UnitFamilyID:     unitFamilyID,
			DisplayUnit:      a.DisplayUnit,
			Computed:         a.Computed,
			Constraints:      a.Constraints,
			DefaultValue:     a.DefaultValue,
			Group:            a.Group,
			SortOrder:        a.SortOrder,
			HelpText:         a.HelpText,
		})
		if err != nil {
			return nil, fmt.Errorf("import attribute %q.%q: %w", a.Type, a.InternalName, err)
		}
		attrIDs[a.Type+"."+a.InternalName] = snap.ID.String()
		res.Attributes.Created++
	}

	// 3. Relationship definitions.
	relNames, err := i.currentRelationshipNames(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range bundle.RelationshipDefinitions {
		if relNames[r.InternalName] {
			res.RelationshipDefinitions.Skipped++
			continue
		}
		parentID, pOK := typeIDByName[r.ParentType]
		childID, cOK := typeIDByName[r.ChildType]
		if !pOK || !cOK {
			return nil, domainerrors.NewValidation("relationship references an unknown type", "relationship", r.InternalName)
		}
		if _, err := i.rels.CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName:        r.InternalName,
			DisplayName:         r.DisplayName,
			Description:         r.Description,
			Kind:                r.Kind,
			ParentTypeID:        parentID,
			ChildTypeID:         childID,
			ParentLabel:         r.ParentLabel,
			ChildLabel:          r.ChildLabel,
			ParentVersionPolicy: r.ParentVersionPolicy,
			ChildVersionPolicy:  r.ChildVersionPolicy,
			MinChildren:         r.MinChildren,
			MaxChildren:         r.MaxChildren,
			MinParents:          r.MinParents,
			MaxParents:          r.MaxParents,
		}); err != nil {
			return nil, fmt.Errorf("import relationship %q: %w", r.InternalName, err)
		}
		relNames[r.InternalName] = true
		res.RelationshipDefinitions.Created++
	}

	// 4. Dependencies (matched by their source+target attribute pair).
	existingDeps, err := i.currentDependencyPairs(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range bundle.Dependencies {
		srcID, sOK := attrIDs[d.SourceType+"."+d.SourceAttribute]
		tgtID, tOK := attrIDs[d.TargetType+"."+d.TargetAttribute]
		if !sOK || !tOK {
			return nil, domainerrors.NewValidation("dependency references an unknown attribute", "source", d.SourceType+"."+d.SourceAttribute, "target", d.TargetType+"."+d.TargetAttribute)
		}
		pair := srcID + "->" + tgtID
		if existingDeps[pair] {
			res.Dependencies.Skipped++
			continue
		}
		if _, err := i.deps.Create(ctx, appdependency.CreateInput{
			SourceAttributeID: srcID,
			TargetAttributeID: tgtID,
			Conditions:        d.Conditions,
			Effect:            d.Effect,
			Description:       d.Description,
		}); err != nil {
			return nil, fmt.Errorf("import dependency %s->%s: %w", d.SourceType+"."+d.SourceAttribute, d.TargetType+"."+d.TargetAttribute, err)
		}
		existingDeps[pair] = true
		res.Dependencies.Created++
	}

	return res, nil
}

// importUnitFamilies creates each bundle unit family that is not already
// present (matched by name), returning name→id for every family the tenant
// now has. A bundle carrying families when unit support is disabled is a
// validation error.
func (i *Interactor) importUnitFamilies(ctx context.Context, families []UnitFamily) (map[string]string, error) {
	idByName := map[string]string{}
	if i.units == nil {
		if len(families) > 0 {
			return nil, domainerrors.NewValidation("bundle declares unit families but unit support is disabled in this deployment")
		}
		return idByName, nil
	}
	existing, err := i.units.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range existing {
		idByName[f.Name] = f.ID.String()
	}
	for _, f := range families {
		if _, ok := idByName[f.Name]; ok {
			continue
		}
		created, err := i.units.Create(ctx, appunit.CreateInput{
			Name: f.Name, BaseUnit: f.BaseUnit, Units: f.Units,
		})
		if err != nil {
			return nil, fmt.Errorf("import unit family %q: %w", f.Name, err)
		}
		idByName[f.Name] = created.ID.String()
	}
	return idByName, nil
}

func (i *Interactor) currentTypeIDs(ctx context.Context) (map[string]string, error) {
	ids := map[string]string{}
	cursor := (*string)(nil)
	for {
		out, err := i.typeDefs.List(ctx, apptypedef.ListInput{Page: db.PageArgs{Limit: limitPtr(200), Cursor: cursor}})
		if err != nil {
			return nil, err
		}
		for _, t := range out.Items {
			ids[t.InternalName] = t.ID.String()
		}
		if !out.PageInfo.HasNextPage {
			break
		}
		cursor = out.PageInfo.NextCursor
	}
	return ids, nil
}

func (i *Interactor) currentAttributeIDs(ctx context.Context, typeIDByName map[string]string) (map[string]string, error) {
	ids := map[string]string{}
	for name, typeID := range typeIDByName {
		cursor := (*string)(nil)
		for {
			out, err := i.attrs.ListByTypeDefinition(ctx, typeID, db.PageArgs{Limit: limitPtr(200), Cursor: cursor})
			if err != nil {
				return nil, err
			}
			for _, a := range out.Items {
				ids[name+"."+a.InternalName] = a.ID.String()
			}
			if !out.PageInfo.HasNextPage {
				break
			}
			cursor = out.PageInfo.NextCursor
		}
	}
	return ids, nil
}

func (i *Interactor) currentRelationshipNames(ctx context.Context) (map[string]bool, error) {
	names := map[string]bool{}
	cursor := (*string)(nil)
	for {
		out, err := i.rels.ListDefinitions(ctx, apprelationship.DefinitionListInput{Page: db.PageArgs{Limit: limitPtr(200), Cursor: cursor}})
		if err != nil {
			return nil, err
		}
		for _, r := range out.Items {
			names[r.InternalName] = true
		}
		if !out.PageInfo.HasNextPage {
			break
		}
		cursor = out.PageInfo.NextCursor
	}
	return names, nil
}

func (i *Interactor) currentDependencyPairs(ctx context.Context) (map[string]bool, error) {
	pairs := map[string]bool{}
	cursor := (*string)(nil)
	for {
		out, err := i.deps.List(ctx, appdependency.ListInput{Page: db.PageArgs{Limit: limitPtr(200), Cursor: cursor}})
		if err != nil {
			return nil, err
		}
		for _, d := range out.Items {
			pairs[d.SourceAttributeID.String()+"->"+d.TargetAttributeID.String()] = true
		}
		if !out.PageInfo.HasNextPage {
			break
		}
		cursor = out.PageInfo.NextCursor
	}
	return pairs, nil
}

// topoSortTypes orders types so a parent always precedes its subtypes.
func topoSortTypes(types []Type) ([]Type, error) {
	byName := make(map[string]Type, len(types))
	for _, t := range types {
		byName[t.InternalName] = t
	}
	var ordered []Type
	visited := map[string]int{} // 0 unseen, 1 in-progress, 2 done
	var visit func(name string) error
	visit = func(name string) error {
		switch visited[name] {
		case 2:
			return nil
		case 1:
			return domainerrors.NewValidation("type inheritance cycle detected", "type", name)
		}
		t, ok := byName[name]
		if !ok {
			return nil // extends an existing (already-imported) type
		}
		visited[name] = 1
		if t.Extends != "" {
			if err := visit(t.Extends); err != nil {
				return err
			}
		}
		visited[name] = 2
		ordered = append(ordered, t)
		return nil
	}
	for _, t := range types {
		if err := visit(t.InternalName); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}
