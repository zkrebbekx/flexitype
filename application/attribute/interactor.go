// Package attribute holds the attribute-definition usecases.
package attribute

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// Interactor implements the attribute-definition usecases.
type Interactor struct {
	uow      uow.UnitOfWork
	typeDefs domaintypedef.Repository
	attrs    domainattribute.Repository
	// units resolves unit families so quantity min/max constraint operands
	// normalize to the base unit; nil when unit families are disabled.
	units appunit.Store
	now   func() time.Time
}

// NewInteractor wires the attribute-definition usecases. units (nil-able)
// normalizes quantity constraint operands.
func NewInteractor(u uow.UnitOfWork, typeDefs domaintypedef.Repository, attrs domainattribute.Repository, units appunit.Store) *Interactor {
	return &Interactor{uow: u, typeDefs: typeDefs, attrs: attrs, units: units, now: time.Now}
}

// CreateInput holds data for creating an attribute definition. Constraints
// and DefaultValue arrive as raw JSON and are decoded against the data
// type.
type CreateInput struct {
	TypeDefinitionID string
	InternalName     string
	DisplayName      string
	Description      string
	DataType         string
	Required         bool
	MultiValued      bool
	Unique           bool
	Localizable      bool
	Scopable         bool
	UnitFamilyID     string
	DisplayUnit      string
	Computed         json.RawMessage
	Constraints      json.RawMessage
	DefaultValue     json.RawMessage
	Group            string
	SortOrder        int
	HelpText         string
}

// Create creates an attribute definition under a type definition. The
// parent row is locked so concurrent creates cannot race name uniqueness.
func (i *Interactor) Create(ctx context.Context, in CreateInput) (*domainattribute.Snapshot, error) {
	typeDefID, err := valueobjects.ParseTypeDefinitionID(in.TypeDefinitionID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	dataType, err := valueobjects.ParseDataType(in.DataType)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	constraints, defaultValue, err := decodeRules(in.Constraints, in.DefaultValue)
	if err != nil {
		return nil, err
	}
	if err := i.normalizeQuantityConstraints(ctx, dataType, in.UnitFamilyID, constraints); err != nil {
		return nil, err
	}
	computed, err := decodeComputed(in.Computed)
	if err != nil {
		return nil, err
	}

	var snap domainattribute.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		typeDefs := i.typeDefs.WithTx(tx)
		attrs := i.attrs.WithTx(tx)

		td, err := typeDefs.GetForUpdate(ctx, typeDefID)
		if err != nil {
			return err
		}
		if err := uow.EnsureTenant(ctx, td.TenantID(), domaintypedef.AggregateType, in.TypeDefinitionID); err != nil {
			return err
		}
		if td.IsArchived() {
			return domainerrors.NewArchived(domaintypedef.AggregateType, td.ID().String())
		}

		// Lock the hierarchy root so concurrent attribute creates on
		// different levels of one hierarchy serialize, then enforce
		// no-shadowing across the whole chain (ancestors and descendants —
		// see docs/design/type-inheritance.md).
		root, err := apptypedef.Root(ctx, typeDefs, td)
		if err != nil {
			return err
		}
		if !root.ID().Equals(td.ID()) {
			if _, err := typeDefs.GetForUpdate(ctx, root.ID()); err != nil {
				return err
			}
		}

		hierarchy, err := apptypedef.Chain(ctx, typeDefs, td)
		if err != nil {
			return err
		}
		descendants, err := apptypedef.Descendants(ctx, typeDefs, td)
		if err != nil {
			return err
		}
		hierarchy = append(hierarchy, descendants...)

		// A computed formula must not create a dependency cycle with the
		// type's other computed attributes.
		if computed != nil && computed.Kind == domainattribute.ComputedFormula {
			refs, verr := computed.Validate()
			if verr != nil {
				return verr
			}
			deps, derr := i.computedDeps(ctx, attrs, hierarchy)
			if derr != nil {
				return derr
			}
			if cerr := checkFormulaCycle(in.InternalName, refs, deps); cerr != nil {
				return cerr
			}
		}

		for _, link := range hierarchy {
			existing, err := attrs.GetByInternalName(ctx, link.ID(), in.InternalName)
			if err != nil && !domainerrors.IsNotFound(err) {
				return fmt.Errorf("check internal name: %w", err)
			}
			if existing != nil {
				if link.ID().Equals(td.ID()) {
					return domainerrors.NewConflict("internal name already in use", "internal_name", in.InternalName)
				}
				return domainerrors.NewConflict(
					"internal name would shadow an attribute declared elsewhere in the type hierarchy",
					"internal_name", in.InternalName, "declared_in", link.InternalName())
			}
		}

		attr, evts, err := domainattribute.New(domainattribute.NewInput{
			TenantID:         td.TenantID(),
			TypeDefinitionID: typeDefID,
			InternalName:     in.InternalName,
			DisplayName:      in.DisplayName,
			Description:      in.Description,
			DataType:         dataType,
			Required:         in.Required,
			MultiValued:      in.MultiValued,
			Unique:           in.Unique,
			Localizable:      in.Localizable,
			Scopable:         in.Scopable,
			UnitFamilyID:     in.UnitFamilyID,
			DisplayUnit:      in.DisplayUnit,
			Computed:         computed,
			Constraints:      constraints,
			DefaultValue:     defaultValue,
			Group:            in.Group,
			SortOrder:        in.SortOrder,
			HelpText:         in.HelpText,
		}, i.now())
		if err != nil {
			return err
		}
		if err := attrs.Save(ctx, attr); err != nil {
			return fmt.Errorf("save attribute definition: %w", err)
		}

		snap = attr.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domainattribute.AggregateType,
			EntityID: attr.ID().String(),
			Action:   activity.ActionCreated,
			After:    snap,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// UpdateInput holds data for updating an attribute definition.
type UpdateInput struct {
	ID           string
	DisplayName  string
	Description  string
	Required     bool
	MultiValued  bool
	Unique       bool
	Localizable  bool
	Scopable     bool
	UnitFamilyID string
	DisplayUnit  string
	Computed     json.RawMessage
	Constraints  json.RawMessage
	DefaultValue json.RawMessage
	Group        string
	SortOrder    int
	HelpText     string
}

// Update mutates an attribute definition, bumping its version.
func (i *Interactor) Update(ctx context.Context, in UpdateInput) (*domainattribute.Snapshot, error) {
	id, err := valueobjects.ParseAttributeDefinitionID(in.ID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	constraints, defaultValue, err := decodeRules(in.Constraints, in.DefaultValue)
	if err != nil {
		return nil, err
	}
	computed, err := decodeComputed(in.Computed)
	if err != nil {
		return nil, err
	}

	var snap domainattribute.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		attrs := i.attrs.WithTx(tx)

		attr, err := attrs.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if err := uow.EnsureTenant(ctx, attr.TenantID(), domainattribute.AggregateType, in.ID); err != nil {
			return err
		}
		before := attr.Snapshot()

		if err := i.normalizeQuantityConstraints(ctx, attr.DataType(), in.UnitFamilyID, constraints); err != nil {
			return err
		}

		evts, err := attr.Update(domainattribute.UpdateInput{
			DisplayName:  in.DisplayName,
			Description:  in.Description,
			Required:     in.Required,
			MultiValued:  in.MultiValued,
			Unique:       in.Unique,
			Localizable:  in.Localizable,
			Scopable:     in.Scopable,
			UnitFamilyID: in.UnitFamilyID,
			DisplayUnit:  in.DisplayUnit,
			Computed:     computed,
			Constraints:  constraints,
			DefaultValue: defaultValue,
			Group:        in.Group,
			SortOrder:    in.SortOrder,
			HelpText:     in.HelpText,
		}, i.now())
		if err != nil {
			return err
		}
		if err := attrs.Save(ctx, attr); err != nil {
			return fmt.Errorf("save attribute definition: %w", err)
		}

		snap = attr.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domainattribute.AggregateType,
			EntityID: attr.ID().String(),
			Action:   activity.ActionUpdated,
			Before:   before,
			After:    snap,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// Archive soft-deletes an attribute definition.
func (i *Interactor) Archive(ctx context.Context, rawID string) (*domainattribute.Snapshot, error) {
	return i.transition(ctx, rawID, activity.ActionArchived)
}

// Restore reverses an Archive.
func (i *Interactor) Restore(ctx context.Context, rawID string) (*domainattribute.Snapshot, error) {
	return i.transition(ctx, rawID, activity.ActionRestored)
}

func (i *Interactor) transition(ctx context.Context, rawID string, action activity.Action) (*domainattribute.Snapshot, error) {
	id, err := valueobjects.ParseAttributeDefinitionID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	var snap domainattribute.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		attrs := i.attrs.WithTx(tx)

		attr, err := attrs.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if err := uow.EnsureTenant(ctx, attr.TenantID(), domainattribute.AggregateType, rawID); err != nil {
			return err
		}
		before := attr.Snapshot()

		var evts []events.Event
		switch action {
		case activity.ActionArchived:
			evts, err = attr.Archive(i.now())
		default:
			evts, err = attr.Restore(i.now())
		}
		if err != nil {
			return err
		}
		if err := attrs.Save(ctx, attr); err != nil {
			return fmt.Errorf("save attribute definition: %w", err)
		}

		snap = attr.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domainattribute.AggregateType,
			EntityID: attr.ID().String(),
			Action:   action,
			Before:   before,
			After:    snap,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// ValidateValue dry-runs a raw JSON value against a saved attribute
// definition: parse + type check + constraints, nothing persisted. Powers
// the console's "try a value" tester.
func (i *Interactor) ValidateValue(ctx context.Context, rawID string, rawValue json.RawMessage) error {
	id, err := valueobjects.ParseAttributeDefinitionID(rawID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	def, err := i.attrs.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := uow.EnsureTenant(ctx, def.TenantID(), domainattribute.AggregateType, rawID); err != nil {
		return err
	}
	v, err := valueobjects.ParseValue(def.DataType(), rawValue)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	return def.ValidateValue(v)
}

// Get loads one attribute definition by ID.
func (i *Interactor) Get(ctx context.Context, rawID string) (*domainattribute.Snapshot, error) {
	id, err := valueobjects.ParseAttributeDefinitionID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	attr, err := i.attrs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := uow.EnsureTenant(ctx, attr.TenantID(), domainattribute.AggregateType, rawID); err != nil {
		return nil, err
	}
	snap := attr.Snapshot()
	return &snap, nil
}

// ListByTypeDefinition returns one page of a type definition's attributes.
func (i *Interactor) ListByTypeDefinition(ctx context.Context, rawTypeDefID string, args db.PageArgs) (*ListOutput, error) {
	typeDefID, err := valueobjects.ParseTypeDefinitionID(rawTypeDefID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	page, err := args.Resolve()
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	td, err := i.typeDefs.Get(ctx, typeDefID)
	if err != nil {
		return nil, err
	}
	if err := uow.EnsureTenant(ctx, td.TenantID(), "type_definition", rawTypeDefID); err != nil {
		return nil, err
	}

	items, total, err := i.attrs.ListByTypeDefinition(ctx, typeDefID, page)
	if err != nil {
		return nil, err
	}
	return newListOutput(items, page, total), nil
}

// ListInput holds filter and pagination arguments for List.
type ListInput struct {
	TypeDefinitionID string
	InternalNames    []string
	DataTypes        []string
	IncludeArchived  bool
	Page             db.PageArgs
}

// ListOutput is one page of attribute definitions.
type ListOutput struct {
	Items    []domainattribute.Snapshot
	PageInfo db.PageInfo
}

// List returns a filtered, paginated set of attribute definitions.
func (i *Interactor) List(ctx context.Context, in ListInput) (*ListOutput, error) {
	page, err := in.Page.Resolve()
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	filter := domainattribute.Filter{
		TenantID:        uow.TenantFromContext(ctx),
		InternalNames:   in.InternalNames,
		IncludeArchived: in.IncludeArchived,
	}
	if in.TypeDefinitionID != "" {
		typeDefID, err := valueobjects.ParseTypeDefinitionID(in.TypeDefinitionID)
		if err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
		filter.TypeDefinitionID = typeDefID
	}
	for _, dt := range in.DataTypes {
		parsed, err := valueobjects.ParseDataType(dt)
		if err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
		filter.DataTypes = append(filter.DataTypes, parsed)
	}

	items, total, err := i.attrs.List(ctx, filter, page)
	if err != nil {
		return nil, err
	}
	return newListOutput(items, page, total), nil
}

func newListOutput(items []*domainattribute.Definition, page db.Page, total int) *ListOutput {
	out := &ListOutput{
		Items:    make([]domainattribute.Snapshot, 0, len(items)),
		PageInfo: db.BuildPageInfo(page, len(items), total),
	}
	for _, a := range items {
		out.Items = append(out.Items, a.Snapshot())
	}
	return out
}

// decodeRules parses raw constraint and default JSON.
func decodeRules(rawConstraints, rawDefault json.RawMessage) (domainattribute.Constraints, *valueobjects.Default, error) {
	var constraints domainattribute.Constraints
	if len(rawConstraints) > 0 && string(rawConstraints) != "null" {
		if err := json.Unmarshal(rawConstraints, &constraints); err != nil {
			return nil, nil, domainerrors.NewValidation("invalid constraints", "error", err.Error())
		}
	}
	var defaultValue *valueobjects.Default
	if len(rawDefault) > 0 && string(rawDefault) != "null" {
		var d valueobjects.Default
		if err := json.Unmarshal(rawDefault, &d); err != nil {
			return nil, nil, domainerrors.NewValidation("invalid default value", "error", err.Error())
		}
		defaultValue = &d
	}
	return constraints, defaultValue, nil
}

// normalizeQuantityConstraints rewrites the base magnitude of quantity
// min/max operands in place, folding the operand's `{magnitude, unit}` through
// the attribute's unit family so range checks run in the base dimension. It is
// a no-op for non-quantity attributes or when unit families are disabled. A
// unit outside the family is a validation error.
func (i *Interactor) normalizeQuantityConstraints(ctx context.Context, dt valueobjects.DataType, unitFamilyID string, constraints domainattribute.Constraints) error {
	if dt != valueobjects.DataTypeQuantity {
		return nil
	}
	needs := false
	for _, c := range constraints {
		if c.Kind() == domainattribute.KindMinValue || c.Kind() == domainattribute.KindMaxValue {
			needs = true
			break
		}
	}
	if !needs {
		return nil
	}
	if i.units == nil {
		return domainerrors.NewValidation("unit families are not configured in this deployment")
	}
	if unitFamilyID == "" {
		return domainerrors.NewValidation("quantity attribute with value constraints requires a unit family")
	}
	famID, err := ulid.Parse(unitFamilyID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	family, err := i.units.Get(ctx, uow.TenantFromContext(ctx), famID)
	if err != nil {
		return err
	}
	rebase := func(v valueobjects.Value) (valueobjects.Value, error) {
		q := v.Quantity()
		base, err := family.ToBase(q.Magnitude, q.Unit)
		if err != nil {
			return valueobjects.Value{}, err
		}
		return valueobjects.NewQuantityValue(q.Magnitude, q.Unit, base)
	}
	for idx, c := range constraints {
		switch cc := c.(type) {
		case domainattribute.MinValue:
			nv, err := rebase(cc.Min)
			if err != nil {
				return err
			}
			constraints[idx] = domainattribute.MinValue{Min: nv}
		case domainattribute.MaxValue:
			nv, err := rebase(cc.Max)
			if err != nil {
				return err
			}
			constraints[idx] = domainattribute.MaxValue{Max: nv}
		}
	}
	return nil
}

// computedDeps maps each computed-formula attribute in the hierarchy to the
// attribute names its formula reads, for cycle detection.
func (i *Interactor) computedDeps(ctx context.Context, attrs domainattribute.Repository, hierarchy []*domaintypedef.TypeDefinition) (map[string][]string, error) {
	deps := map[string][]string{}
	for _, link := range hierarchy {
		list, _, err := attrs.ListByTypeDefinition(ctx, link.ID(), db.Page{Limit: 500})
		if err != nil {
			return nil, err
		}
		for _, a := range list {
			c := a.Computed()
			if c == nil || c.Kind != domainattribute.ComputedFormula {
				continue
			}
			refs, verr := c.Validate()
			if verr != nil {
				return nil, verr
			}
			deps[a.InternalName()] = refs
		}
	}
	return deps, nil
}

// decodeComputed parses the optional computed-attribute spec.
func decodeComputed(raw json.RawMessage) (*domainattribute.Computed, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var c domainattribute.Computed
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, domainerrors.NewValidation("invalid computed spec", "error", err.Error())
	}
	return &c, nil
}

// checkFormulaCycle rejects a computed formula whose references would form a
// dependency cycle among the type's computed attributes. deps maps each
// existing computed attribute's internal name to the names its formula
// reads; the candidate (name → refs) is added before the search.
func checkFormulaCycle(name string, refs []string, deps map[string][]string) error {
	graph := make(map[string][]string, len(deps)+1)
	for k, v := range deps {
		graph[k] = v
	}
	graph[name] = refs

	// A cycle exists iff `name` is reachable from itself following edges.
	var visit func(cur string, seen map[string]bool) bool
	visit = func(cur string, seen map[string]bool) bool {
		for _, ref := range graph[cur] {
			if ref == name {
				return true
			}
			if seen[ref] {
				continue
			}
			seen[ref] = true
			if visit(ref, seen) {
				return true
			}
		}
		return false
	}
	if visit(name, map[string]bool{name: true}) {
		return domainerrors.NewValidation("computed formula introduces a dependency cycle", "attribute", name)
	}
	return nil
}
