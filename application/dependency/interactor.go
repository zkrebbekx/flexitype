// Package dependency holds the attribute-dependency usecases, including
// effective-schema resolution for building cascading UIs.
package dependency

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/appctx"
	"github.com/zkrebbekx/flexitype/application/fieldacl"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// Interactor implements the dependency usecases.
type Interactor struct {
	uow      uow.UnitOfWork
	typeDefs domaintypedef.Repository
	attrs    domainattribute.Repository
	values   appctx.ValueReader
	deps     domaindependency.Repository
	// units resolves the unit family a quantity attribute pins, so quantity
	// operands inside a rule are rebased the way attribute constraints
	// already are. Nil disables quantity support, and a rule carrying a
	// quantity operand is then rejected rather than stored un-rebased.
	units unitStore
	now   func() time.Time
}

// unitStore is the subset of the unit-family port this interactor needs.
type unitStore interface {
	Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (appunit.Family, error)
}

// NewInteractor wires the dependency usecases.
func NewInteractor(u uow.UnitOfWork, typeDefs domaintypedef.Repository, attrs domainattribute.Repository, values appctx.ValueReader, deps domaindependency.Repository, units unitStore) *Interactor {
	return &Interactor{uow: u, typeDefs: typeDefs, attrs: attrs, values: values, deps: deps, units: units, now: uow.UTCNow}
}

// normalizeQuantityOperands rebases every quantity operand a rule carries: the
// conditions against the SOURCE attribute's unit family, and the effect's
// allowed values and nested min/max constraints against the TARGET's.
//
// Attribute constraints and static defaults were rebased; the identical
// operands inside a dependency were not, and ParseValue stores whatever base
// the caller supplied. So "if weight > 5 kg then require hazard_class",
// written through the API without computing a base, stored a bound whose base
// was 0 — and stored values ARE rebased at write time, so the rule fired for
// every weight. With a wrong base it never fired. Conditional validation on
// quantities was silently wrong in both directions, with no error at
// definition time or at evaluation time.
func (i *Interactor) normalizeQuantityOperands(
	ctx context.Context,
	source, target *domainattribute.Definition,
	conditions []domaindependency.Condition,
	effect *domaindependency.Effect,
) error {
	rebaseWith := func(def *domainattribute.Definition) (func(valueobjects.Value) (valueobjects.Value, error), error) {
		if def.DataType() != valueobjects.DataTypeQuantity {
			// Nothing to rebase against; a quantity operand on a
			// non-quantity attribute is already a type error downstream.
			return func(v valueobjects.Value) (valueobjects.Value, error) { return v, nil }, nil
		}
		if i.units == nil {
			return nil, domainerrors.NewValidation("unit families are not configured in this deployment")
		}
		if def.UnitFamilyID() == "" {
			return nil, domainerrors.NewValidation(
				"quantity attribute in a dependency requires a unit family", "attribute", def.InternalName())
		}
		famID, err := ulid.Parse(def.UnitFamilyID())
		if err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
		family, err := i.units.Get(ctx, uow.TenantFromContext(ctx), famID)
		if err != nil {
			return nil, err
		}
		return func(v valueobjects.Value) (valueobjects.Value, error) {
			return appunit.Rebase(family, v)
		}, nil
	}

	rebaseSource, err := rebaseWith(source)
	if err != nil {
		return err
	}
	for idx := range conditions {
		c := &conditions[idx]
		for _, ref := range []**valueobjects.Value{&c.Value, &c.Min, &c.Max} {
			if *ref == nil {
				continue
			}
			nv, err := rebaseSource(**ref)
			if err != nil {
				return err
			}
			*ref = &nv
		}
		for j, v := range c.Values {
			nv, err := rebaseSource(v)
			if err != nil {
				return err
			}
			c.Values[j] = nv
		}
	}

	if effect == nil {
		return nil
	}
	rebaseTarget, err := rebaseWith(target)
	if err != nil {
		return err
	}
	for j, v := range effect.AllowedValues {
		nv, err := rebaseTarget(v)
		if err != nil {
			return err
		}
		effect.AllowedValues[j] = nv
	}
	for idx, c := range effect.Constraints {
		switch cc := c.(type) {
		case domainattribute.MinValue:
			nv, err := rebaseTarget(cc.Min)
			if err != nil {
				return err
			}
			effect.Constraints[idx] = domainattribute.MinValue{Min: nv}
		case domainattribute.MaxValue:
			nv, err := rebaseTarget(cc.Max)
			if err != nil {
				return err
			}
			effect.Constraints[idx] = domainattribute.MaxValue{Max: nv}
		case domainattribute.OneOf:
			// The attribute-level normaliser rebases a one_of's members; the
			// effect loop had no arm for it, so an effect naming an allowed
			// quantity compared a caller's base magnitude against an
			// unconverted member and refused the EXACT allowed value —
			// blaming the writer for a rule the schema author wrote.
			members := make([]valueobjects.Value, len(cc.Values))
			for k, v := range cc.Values {
				nv, verr := rebaseTarget(v)
				if verr != nil {
					return verr
				}
				members[k] = nv
			}
			effect.Constraints[idx] = domainattribute.OneOf{Values: members}

		}
	}
	return nil
}

// CreateInput holds data for creating a dependency. Conditions and Effect
// arrive as raw JSON in the documented condition/effect schema.
type CreateInput struct {
	SourceAttributeID string
	TargetAttributeID string
	Conditions        json.RawMessage
	Effect            json.RawMessage
	Description       string
}

// Create creates a dependency between two attributes of one type
// definition.
func (i *Interactor) Create(ctx context.Context, in CreateInput) (*domaindependency.Snapshot, error) {
	sourceID, err := valueobjects.ParseAttributeDefinitionID(in.SourceAttributeID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	targetID, err := valueobjects.ParseAttributeDefinitionID(in.TargetAttributeID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	conditions, effect, err := decodeRule(in.Conditions, in.Effect)
	if err != nil {
		return nil, err
	}

	var snap domaindependency.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		attrs := i.attrs.WithTx(tx)
		deps := i.deps.WithTx(tx)

		source, err := attrs.Get(ctx, sourceID)
		if err != nil {
			return err
		}
		if err := uow.EnsureTenant(ctx, source.TenantID(), "attribute_definition", in.SourceAttributeID); err != nil {
			return err
		}
		target, err := attrs.GetForUpdate(ctx, targetID)
		if err != nil {
			return err
		}
		if err := uow.EnsureTenant(ctx, target.TenantID(), "attribute_definition", in.TargetAttributeID); err != nil {
			return err
		}
		if err := assertRuleAccess(ctx, source, target); err != nil {
			return err
		}

		// Both attributes must live on one hierarchy chain so every entity
		// holding the target also holds (or inherits) the source.
		if err := i.checkSameChain(ctx, tx, source, target); err != nil {
			return err
		}
		if err := i.normalizeQuantityOperands(ctx, source, target, conditions, &effect); err != nil {
			return err
		}

		d, evts, err := domaindependency.New(domaindependency.NewInput{
			TenantID:    source.TenantID(),
			Source:      source,
			Target:      target,
			Conditions:  conditions,
			Effect:      effect,
			Description: in.Description,
		}, i.now())
		if err != nil {
			return err
		}
		if err := deps.Save(ctx, d); err != nil {
			return fmt.Errorf("save dependency: %w", err)
		}

		snap = d.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domaindependency.AggregateType,
			EntityID: d.ID().String(),
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

// UpdateInput holds data for updating a dependency's rule.
type UpdateInput struct {
	ID          string
	Conditions  json.RawMessage
	Effect      json.RawMessage
	Description string
}

// Update replaces a dependency's conditions and effect.
func (i *Interactor) Update(ctx context.Context, in UpdateInput) (*domaindependency.Snapshot, error) {
	id, err := valueobjects.ParseDependencyID(in.ID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	conditions, effect, err := decodeRule(in.Conditions, in.Effect)
	if err != nil {
		return nil, err
	}

	var snap domaindependency.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		attrs := i.attrs.WithTx(tx)
		deps := i.deps.WithTx(tx)

		d, err := deps.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if err := uow.EnsureTenant(ctx, d.TenantID(), domaindependency.AggregateType, in.ID); err != nil {
			return err
		}
		before := d.Snapshot()

		source, err := attrs.Get(ctx, d.SourceAttributeID())
		if err != nil {
			return err
		}
		target, err := attrs.Get(ctx, d.TargetAttributeID())
		if err != nil {
			return err
		}
		if err := assertRuleAccess(ctx, source, target); err != nil {
			return err
		}

		if err := i.normalizeQuantityOperands(ctx, source, target, conditions, &effect); err != nil {
			return err
		}

		evts, err := d.Update(source, target, domaindependency.UpdateInput{
			Conditions:  conditions,
			Effect:      effect,
			Description: in.Description,
		}, i.now())
		if err != nil {
			return err
		}
		if err := deps.Save(ctx, d); err != nil {
			return fmt.Errorf("save dependency: %w", err)
		}

		snap = d.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domaindependency.AggregateType,
			EntityID: d.ID().String(),
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

// Archive soft-deletes a dependency.
func (i *Interactor) Archive(ctx context.Context, rawID string) (*domaindependency.Snapshot, error) {
	id, err := valueobjects.ParseDependencyID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	var snap domaindependency.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		deps := i.deps.WithTx(tx)

		d, err := deps.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if err := uow.EnsureTenant(ctx, d.TenantID(), domaindependency.AggregateType, rawID); err != nil {
			return err
		}
		// Archiving a rule changes what the TARGET attribute accepts, so it
		// needs the same write right as authoring one. A target the caller
		// cannot even read reports the dependency as absent rather than
		// confirming what it points at.
		target, err := i.attrs.WithTx(tx).Get(ctx, d.TargetAttributeID())
		if err != nil {
			return err
		}
		access := uow.AccessFromContext(ctx)
		if !access.CanRead(target.InternalName()) {
			return domainerrors.NewNotFound(domaindependency.AggregateType, rawID)
		}
		if !access.CanWrite(target.InternalName()) {
			return domainerrors.NewForbidden(
				"archiving a dependency requires write permission on its target attribute",
				"attribute", target.InternalName())
		}
		before := d.Snapshot()

		evts, err := d.Archive(i.now())
		if err != nil {
			return err
		}
		if err := deps.Save(ctx, d); err != nil {
			return fmt.Errorf("save dependency: %w", err)
		}

		snap = d.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domaindependency.AggregateType,
			EntityID: d.ID().String(),
			Action:   activity.ActionArchived,
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

// Get loads one dependency by ID.
func (i *Interactor) Get(ctx context.Context, rawID string) (*domaindependency.Snapshot, error) {
	id, err := valueobjects.ParseDependencyID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	d, err := i.deps.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := uow.EnsureTenant(ctx, d.TenantID(), domaindependency.AggregateType, rawID); err != nil {
		return nil, err
	}
	snap := d.Snapshot()
	return &snap, nil
}

// ListInput holds filter and pagination arguments for List.
type ListInput struct {
	SourceAttributeID string
	TargetAttributeID string
	IncludeArchived   bool
	Page              db.PageArgs
}

// ListOutput is one page of dependencies.
type ListOutput struct {
	Items    []domaindependency.Snapshot
	PageInfo db.PageInfo
}

// List returns a filtered, paginated set of dependencies.
func (i *Interactor) List(ctx context.Context, in ListInput) (*ListOutput, error) {
	page, err := in.Page.Resolve()
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	filter := domaindependency.Filter{
		TenantID:        uow.TenantFromContext(ctx),
		IncludeArchived: in.IncludeArchived,
	}
	if in.SourceAttributeID != "" {
		if filter.SourceAttributeID, err = valueobjects.ParseAttributeDefinitionID(in.SourceAttributeID); err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
	}
	if in.TargetAttributeID != "" {
		if filter.TargetAttributeID, err = valueobjects.ParseAttributeDefinitionID(in.TargetAttributeID); err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
	}

	items, total, err := i.deps.List(ctx, filter, page)
	if err != nil {
		return nil, err
	}

	items, info := db.KeysetPage(page, items, db.KeysetTotal(page, total), func(d *domaindependency.Dependency) string {
		return db.EncodeKeyset(d.ID().String())
	})
	out := &ListOutput{
		Items:    make([]domaindependency.Snapshot, 0, len(items)),
		PageInfo: info,
	}
	for _, d := range items {
		out.Items = append(out.Items, d.Snapshot())
	}
	return out, nil
}

// EffectiveSchemaOutput is the resolved rule set for a target attribute
// given an entity's current values — what a UI needs to render a cascading
// picklist.
type EffectiveSchemaOutput struct {
	AttributeDefinitionID string               `json:"attribute_definition_id"`
	EntityID              string               `json:"entity_id"`
	Required              bool                 `json:"required"`
	Restricted            bool                 `json:"restricted"`
	AllowedValues         []valueobjects.Value `json:"allowed_values,omitempty"`
}

// EffectiveSchema resolves the dependency-adjusted schema for one target
// attribute and entity.
func (i *Interactor) EffectiveSchema(ctx context.Context, rawAttrID, rawEntityID string) (*EffectiveSchemaOutput, error) {
	attrID, err := valueobjects.ParseAttributeDefinitionID(rawAttrID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	entityID, err := valueobjects.ParseEntityID(rawEntityID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	def, err := i.attrs.Get(ctx, attrID)
	if err != nil {
		return nil, err
	}
	if err := uow.EnsureTenant(ctx, def.TenantID(), "attribute_definition", rawAttrID); err != nil {
		return nil, err
	}
	// A target the caller may not read is reported as not found, exactly as
	// an unknown ID is: the resolved Required/Restricted/AllowedValues of a
	// restricted attribute are facts about it the caller may not have.
	if !uow.AccessFromContext(ctx).CanRead(def.InternalName()) {
		return nil, domainerrors.NewNotFound("attribute_definition", rawAttrID)
	}
	targeting, err := i.deps.ListByTarget(ctx, attrID)
	if err != nil {
		return nil, err
	}

	sourceValues := make(map[valueobjects.AttributeDefinitionID][]valueobjects.Value)
	if len(targeting) > 0 {
		// Read the entity's whole value set, not the slice anchored to this
		// attribute's DECLARING type. For an entity of a subtype the declaring
		// type is the parent, so this endpoint reported the un-narrowed schema
		// for every subtype entity — the cascading-picklist case it exists to
		// serve. ListByEntities keys on (tenant, entity) with no type filter,
		// which is what GraphQL already used.
		entityValues, err := i.values.ListByEntities(ctx, def.TenantID(), []valueobjects.EntityID{entityID})
		if err != nil {
			return nil, err
		}
		// Resolve against the caller's READABLE values only. This endpoint
		// used to read the raw repository, so the one bit it returns was a
		// function of values the field ACL redacts everywhere else — a rule
		// keyed on a restricted source made it a binary-search oracle over
		// that source (~20 requests per exact value). A rule whose source the
		// caller cannot see now simply does not fire, which matches what the
		// same caller is told the entity holds.
		entityValues, err = i.readableOnly(ctx, entityValues)
		if err != nil {
			return nil, err
		}
		for _, av := range entityValues {
			sourceValues[av.AttributeDefinitionID()] = append(sourceValues[av.AttributeDefinitionID()], av.Value())
		}
	}

	// Tenant-local: see completeness.go — a dynamic condition compares
	// calendar days, not instants.
	schema, err := domaindependency.ResolveEffectiveWithContext(def, targeting, sourceValues, uow.ContextValuesFromContext(ctx), uow.LocalNow(ctx))
	if err != nil {
		return nil, err
	}
	return &EffectiveSchemaOutput{
		AttributeDefinitionID: attrID.String(),
		EntityID:              entityID.String(),
		Required:              schema.Required,
		Restricted:            schema.Restricted,
		AllowedValues:         schema.AllowedValues,
	}, nil
}

// readableOnly drops the values of attributes the principal may not read,
// resolving attribute identity by ID the way every other redacting surface
// does (application/fieldacl). Rule resolution then sees exactly the value
// set the caller is told the entity holds.
func (i *Interactor) readableOnly(ctx context.Context, values []*domainvalue.AttributeValue) ([]*domainvalue.AttributeValue, error) {
	if len(values) == 0 || uow.AccessFromContext(ctx).Admin {
		return values, nil
	}
	ids := make([]valueobjects.AttributeDefinitionID, 0, len(values))
	for _, av := range values {
		ids = append(ids, av.AttributeDefinitionID())
	}
	readable, err := fieldacl.New(i.attrs).Readable(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := values[:0]
	for _, av := range values {
		if readable[av.AttributeDefinitionID().String()] {
			out = append(out, av)
		}
	}
	return out, nil
}

// assertRuleAccess is the field-ACL gate for authoring a dependency: the
// caller must be able to READ the source (a rule keyed on an attribute is a
// comparison against its values — authoring one over an invisible source is
// the binary-search oracle the FQL binder refuses) and WRITE the target
// (the rule changes what the target accepts).
//
// An unreadable attribute is reported as not found, exactly as an unknown ID
// is, so the refusal does not confirm that a restricted attribute exists. A
// readable-but-unwritable target is a plain permission refusal: the caller
// already knows the attribute exists.
func assertRuleAccess(ctx context.Context, source, target *domainattribute.Definition) error {
	access := uow.AccessFromContext(ctx)
	if !access.CanRead(source.InternalName()) {
		return domainerrors.NewNotFound("attribute_definition", source.ID().String())
	}
	if !access.CanRead(target.InternalName()) {
		return domainerrors.NewNotFound("attribute_definition", target.ID().String())
	}
	if !access.CanWrite(target.InternalName()) {
		return domainerrors.NewForbidden(
			"a dependency requires write permission on its target attribute",
			"attribute", target.InternalName())
	}
	return nil
}

// checkSameChain verifies the two attributes' declaring types share one
// extends chain (equal, or one an ancestor of the other).
func (i *Interactor) checkSameChain(ctx context.Context, tx db.Transactor, source, target *domainattribute.Definition) error {
	if source.TypeDefinitionID().Equals(target.TypeDefinitionID()) {
		return nil
	}
	typeDefs := i.typeDefs.WithTx(tx)

	sourceType, err := typeDefs.Get(ctx, source.TypeDefinitionID())
	if err != nil {
		return err
	}
	ok, err := apptypedef.IsAncestorOrSelf(ctx, typeDefs, sourceType, target.TypeDefinitionID())
	if err != nil {
		return err
	}
	if !ok {
		targetType, terr := typeDefs.Get(ctx, target.TypeDefinitionID())
		if terr != nil {
			return terr
		}
		if ok, err = apptypedef.IsAncestorOrSelf(ctx, typeDefs, targetType, source.TypeDefinitionID()); err != nil {
			return err
		}
	}
	if !ok {
		return domainerrors.NewValidation(
			"source and target attributes must belong to the same type hierarchy")
	}
	return nil
}

// decodeRule parses raw condition and effect JSON.
func decodeRule(rawConditions, rawEffect json.RawMessage) ([]domaindependency.Condition, domaindependency.Effect, error) {
	var conditions []domaindependency.Condition
	if len(rawConditions) > 0 && string(rawConditions) != "null" {
		if err := json.Unmarshal(rawConditions, &conditions); err != nil {
			return nil, domaindependency.Effect{}, domainerrors.NewValidation("invalid conditions", "error", err.Error())
		}
	}
	var effect domaindependency.Effect
	if len(rawEffect) > 0 && string(rawEffect) != "null" {
		if err := json.Unmarshal(rawEffect, &effect); err != nil {
			return nil, domaindependency.Effect{}, domainerrors.NewValidation("invalid effect", "error", err.Error())
		}
	}
	return conditions, effect, nil
}
