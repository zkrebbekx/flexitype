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
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/formula"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// Interactor implements the attribute-definition usecases.
type Interactor struct {
	uow      uow.UnitOfWork
	typeDefs domaintypedef.Repository
	attrs    domainattribute.Repository
	values   domainvalue.Repository
	// units resolves unit families so quantity min/max constraint operands
	// normalize to the base unit; nil when unit families are disabled.
	units appunit.Store
	// relDefs resolves the relationship a computed ROLLUP traverses, so a
	// rollup naming a relationship that does not exist is refused here rather
	// than aggregating nothing for ever. Nil-safe: with no repository the
	// check is skipped rather than failing every rollup.
	relDefs domainrelationship.DefinitionRepository
	now     func() time.Time
}

// NewInteractor wires the attribute-definition usecases. units (nil-able)
// normalizes quantity constraint operands.
func NewInteractor(u uow.UnitOfWork, typeDefs domaintypedef.Repository, attrs domainattribute.Repository, values domainvalue.Repository, units appunit.Store, relDefs domainrelationship.DefinitionRepository) *Interactor {
	return &Interactor{
		uow: u, typeDefs: typeDefs, attrs: attrs, values: values,
		units: units, relDefs: relDefs, now: uow.UTCNow,
	}
}

// assertStructuralChangeIsSafe refuses a flag flip that would orphan or
// invalidate data already stored.
//
// The structural flags used to be replaced with no check at all. Each flip
// left rows the new schema cannot express or reach, and none of them reported
// anything: the rows kept hydrating and kept matching FQL, so the schema and
// the data disagreed silently. Version pinning does not cover this — it
// records the constraint version a value was validated against, and these
// flags are structural rather than per-value.
func (i *Interactor) assertStructuralChangeIsSafe(
	ctx context.Context, values domainvalue.Repository, before domainattribute.Snapshot, in UpdateInput,
	computed *domainattribute.Computed,
) error {
	losingMulti := before.MultiValued && !in.MultiValued
	gainingUnique := !before.Unique && in.Unique
	losingLocale := before.Localizable && !in.Localizable
	losingChannel := before.Scopable && !in.Scopable
	gainingComputed := before.Computed == nil && computed != nil
	// The unit family is structural too, and it was not guarded at all.
	// Stored quantities keep a magnitude in the OLD family's base unit, so
	// flipping mass -> length made every stored value read in the new
	// family's base (2000 g read as 2000 m), and the stored display unit is
	// not a member of the new family — so those rows could never be rewritten
	// through the API either.
	//
	// CLEARING the family counts as changing it. The guard used to require a
	// non-empty new family, so it never saw a clear — and a REST PUT that
	// omits the omitempty unit_family_id field sends the empty string, which
	// makes an ordinary "rename the attribute" round trip unpin the family.
	// The next write then failed with "quantity attribute has no unit
	// family" while the stored value still read "2 kg": readable forever,
	// writable never.
	changingFamily := before.UnitFamilyID != "" && before.UnitFamilyID != in.UnitFamilyID
	if !losingMulti && !gainingUnique && !losingLocale && !losingChannel &&
		!gainingComputed && !changingFamily {
		return nil
	}

	shape, err := values.AttributeDataShape(ctx, before.TenantID, before.ID)
	if err != nil {
		return fmt.Errorf("inspect stored values: %w", err)
	}
	switch {
	case losingMulti && shape.EntitiesWithMany > 0:
		// A single-valued write updates the first row and never reaches the
		// rest, so those rows would still read and still match FQL with no
		// write able to touch them again.
		return domainerrors.NewConflict(
			"cannot make the attribute single-valued: entities already hold more than one value; "+
				"remove the extra values first",
			"attribute", before.InternalName, "entities", shape.EntitiesWithMany)
	case gainingUnique && shape.DuplicateValues > 0:
		// No backfill runs, so the existing duplicates would persist while
		// new writers of those same values were refused.
		return domainerrors.NewConflict(
			"cannot make the attribute unique: stored values already contain duplicates",
			"attribute", before.InternalName, "duplicate_values", shape.DuplicateValues)
	case (losingLocale || losingChannel) && shape.ScopedValues > 0:
		// Scope is rejected on write once the flag is off, so scoped rows
		// could never be updated or removed through the API again.
		return domainerrors.NewConflict(
			"cannot turn off localizable/scopable: stored values carry a locale or channel; "+
				"remove the scoped values first",
			"attribute", before.InternalName, "scoped_values", shape.ScopedValues)
	case changingFamily && shape.LiveValues > 0:
		if in.UnitFamilyID == "" {
			return domainerrors.NewConflict(
				"cannot clear the unit family: stored quantities hold a magnitude in its base unit, and a "+
					"quantity attribute with no family cannot be written at all; remove the values first, "+
					"or resend the unit family you meant to keep",
				"attribute", before.InternalName, "values", shape.LiveValues,
				"unit_family", before.UnitFamilyID)
		}
		return domainerrors.NewConflict(
			"cannot change the unit family: stored quantities hold a magnitude in the current family's "+
				"base unit, so they would read as the new family's base and could not be rewritten; "+
				"remove the values first",
			"attribute", before.InternalName, "values", shape.LiveValues,
			"from", before.UnitFamilyID, "to", in.UnitFamilyID)
	case gainingComputed && shape.LiveValues > 0:
		// The next recompute would overwrite or clear whatever a person
		// wrote there.
		return domainerrors.NewConflict(
			"cannot make the attribute computed: it already holds written values, "+
				"which the first recompute would overwrite or clear",
			"attribute", before.InternalName, "values", shape.LiveValues)
	}
	return nil
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
	if err := i.normalizeQuantityConstraints(ctx, dataType, in.UnitFamilyID, constraints, defaultValue); err != nil {
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

		// The uniqueness namespace is the WHOLE hierarchy, not just this
		// type's chain and its own descendants. The FQL binder flattens the
		// queried root's chain and every descendant into one name→attribute
		// map, so two sibling subtypes declaring the same internal name gave
		// the binder a collision it resolved by list order — the last one
		// listed won, silently, and which one that was could change with an
		// unrelated edit. Walking down from the root covers siblings, cousins
		// and everything else the binder will flatten together.
		hierarchy, err := apptypedef.Chain(ctx, typeDefs, td)
		if err != nil {
			return err
		}
		descendants, err := apptypedef.Descendants(ctx, typeDefs, root)
		if err != nil {
			return err
		}
		hierarchy = appendUnseenTypes(hierarchy, descendants)

		// A computed formula must not create a dependency cycle with the
		// type's other computed attributes.
		// A rollup names a relationship and, for an aggregate, an attribute on
		// the entities it reaches. Both are resolved HERE.
		//
		// A rollup naming a relationship that does not exist finds no
		// counterparts, aggregates nothing and clears its value — silently,
		// for ever. That is the exact failure the feature was withheld for a
		// release to avoid, so a typo is a validation error at definition
		// time instead.
		if computed != nil && computed.Kind == domainattribute.ComputedRollup {
			if rerr := i.assertRollupResolves(ctx, td, computed); rerr != nil {
				return rerr
			}
		}

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
			scalarRefs, numericRefs, serr := formulaRefKinds(computed.Formula)
			if serr != nil {
				return serr
			}
			if serr := assertFormulaSourcesAreScalar(ctx, attrs, hierarchy, refs, scalarRefs, numericRefs); serr != nil {
				return serr
			}
		}

		// The reverse guard runs on CREATE too. It used to run on update
		// only, so a schema author could write `total = line_total * 2`
		// before line_total existed — accepted, because the reference is
		// unresolved — and then create line_total as multi-valued or
		// localizable. A bare name needs exactly one base-scope value, so the
		// formula was permanently undefined: an attribute the schema
		// advertises that never holds a value, with no error at any point.
		if in.MultiValued || in.Localizable || in.Scopable {
			deps, derr := i.computedRefs(ctx, attrs, hierarchy)
			if derr != nil {
				return derr
			}
			if rerr := refuseFormulaReaders(in.InternalName, deps, in.MultiValued,
				in.Localizable || in.Scopable); rerr != nil {
				return rerr
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
					"internal name is already declared elsewhere in the type hierarchy; "+
						"one name means one attribute across the whole tree, because a query "+
						"over the root flattens it into a single namespace",
					"internal_name", in.InternalName, "declared_in", link.InternalName())
			}
		}

		// Cap attributes per type. Every effective-schema walk resolves a
		// type's attributes in one read; without a cap, a type could hold
		// more than the walk returns and the tail became invisible to
		// validation, FQL, import/export, cycle detection and completeness
		// while remaining writable through its own endpoint.
		if _, total, terr := attrs.ListByTypeDefinition(ctx, typeDefID, db.Page{Limit: 1, WantTotal: true}); terr != nil {
			return fmt.Errorf("count type attributes: %w", terr)
		} else if total >= domainattribute.MaxPerType {
			return domainerrors.NewValidation(
				"type already declares the maximum number of attributes",
				"type_definition_id", typeDefID.String(),
				"attributes", total, "limit", domainattribute.MaxPerType)
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

	// Version, when set, is the version the CALLER read. The update is
	// written only if the stored version still matches, and a conflict is
	// reported otherwise.
	//
	// It is a pointer so that omitting it keeps last-write-wins, the same
	// contract a saved-view patch offers. This request is a FULL REPLACE of
	// the editable record, so without it two operators editing one attribute
	// each write the whole record and the later write silently erases the
	// earlier one — including fields that operator never looked at.
	Version *int
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
		typeDefs := i.typeDefs.WithTx(tx)
		attrs := i.attrs.WithTx(tx)

		attr, err := attrs.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if err := uow.EnsureTenant(ctx, attr.TenantID(), domainattribute.AggregateType, in.ID); err != nil {
			return err
		}
		before := attr.Snapshot()

		// COMPARE-AND-SWAP against the version the caller read, before any
		// other check. A conflict means the record they are replacing is not
		// the record they loaded, so every validation below would be judging
		// the wrong baseline.
		if in.Version != nil && *in.Version != before.Version {
			return domainerrors.NewConflict(
				"the attribute definition was modified by someone else; reload it and retry",
				"attribute_definition_id", in.ID,
				"expected_version", *in.Version,
				"actual_version", before.Version)
		}

		if serr := i.assertStructuralChangeIsSafe(ctx, i.values.WithTx(tx), before, in, computed); serr != nil {
			return serr
		}
		// The reverse of the formula-source guard: an attribute a formula
		// reads cannot BECOME multi-valued, localizable or scopable, or that
		// formula would start collapsing members and skipping scoped values
		// with no write to the formula itself.
		if serr := i.assertNoFormulaReadsScopedSource(ctx, typeDefs, attrs, before, in); serr != nil {
			return serr
		}

		// A rollup names a relationship and, for an aggregate, an attribute on
		// the entities it reaches. Both are resolved HERE, on update as well
		// as create: a rollup edited to name a relationship that does not
		// exist finds no counterparts, aggregates nothing and clears its
		// value — silently, for ever.
		if computed != nil && computed.Kind == domainattribute.ComputedRollup {
			owner, terr := typeDefs.Get(ctx, attr.TypeDefinitionID())
			if terr != nil {
				return terr
			}
			if rerr := i.assertRollupResolves(ctx, owner, computed); rerr != nil {
				return rerr
			}
		}

		// A computed formula must not introduce a dependency cycle with the
		// type's other computed attributes — enforced on update as well as
		// create, otherwise a formula could be edited into a self-reference
		// and wedge the recompute loop.
		if computed != nil && computed.Kind == domainattribute.ComputedFormula {
			refs, verr := computed.Validate()
			if verr != nil {
				return verr
			}
			td, terr := typeDefs.Get(ctx, attr.TypeDefinitionID())
			if terr != nil {
				return terr
			}
			// Lock the hierarchy root before reading the other formulas, the
			// way Create does. Locking only the edited attribute's row let two
			// concurrent edits — A set to "b + 1" and B set to "a + 1" — each
			// validate against the other's pre-edit formula and both commit,
			// so the pair formed a cycle that no write ever reported. The
			// materializer's cyclic guard then skipped both permanently, and
			// an operator saw two stale computed attributes with valid-looking
			// formulas and nothing naming the cause.
			root, rerr := apptypedef.Root(ctx, typeDefs, td)
			if rerr != nil {
				return rerr
			}
			if _, lerr := typeDefs.GetForUpdate(ctx, root.ID()); lerr != nil {
				return lerr
			}
			hierarchy, herr := apptypedef.Chain(ctx, typeDefs, td)
			if herr != nil {
				return herr
			}
			descendants, derr := apptypedef.Descendants(ctx, typeDefs, td)
			if derr != nil {
				return derr
			}
			hierarchy = append(hierarchy, descendants...)
			deps, derr := i.computedDeps(ctx, attrs, hierarchy)
			if derr != nil {
				return derr
			}
			scalarRefs, numericRefs, srerr := formulaRefKinds(computed.Formula)
			if srerr != nil {
				return srerr
			}
			if serr := assertFormulaSourcesAreScalar(ctx, attrs, hierarchy, refs, scalarRefs, numericRefs); serr != nil {
				return serr
			}
			if cerr := checkFormulaCycle(attr.InternalName(), refs, deps); cerr != nil {
				return cerr
			}
		}

		if err := i.normalizeQuantityConstraints(ctx, attr.DataType(), in.UnitFamilyID, constraints, defaultValue); err != nil {
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
			// Restoring re-introduces the name into the hierarchy, so the
			// same uniqueness sweep Create runs has to run here. Create's
			// guard uses GetByInternalName, which EXCLUDES archived rows —
			// so archiving `release_code` on subtype A and creating it on
			// sibling B both succeeded, and restoring A's left two live
			// attributes with one name, resolved by list order.
			if uerr := i.assertNameFreeInHierarchy(ctx, tx, attr); uerr != nil {
				return uerr
			}
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

// assertNameFreeInHierarchy refuses a restore whose internal name is already
// live on another type in the same hierarchy.
func (i *Interactor) assertNameFreeInHierarchy(ctx context.Context, tx db.Transactor, attr *domainattribute.Definition) error {
	typeDefs := i.typeDefs.WithTx(tx)
	attrs := i.attrs.WithTx(tx)
	td, err := typeDefs.Get(ctx, attr.TypeDefinitionID())
	if err != nil {
		return err
	}
	root, err := apptypedef.Root(ctx, typeDefs, td)
	if err != nil {
		return err
	}
	hierarchy, err := apptypedef.Chain(ctx, typeDefs, td)
	if err != nil {
		return err
	}
	descendants, err := apptypedef.Descendants(ctx, typeDefs, root)
	if err != nil {
		return err
	}
	hierarchy = appendUnseenTypes(hierarchy, descendants)
	for _, link := range hierarchy {
		existing, gerr := attrs.GetByInternalName(ctx, link.ID(), attr.InternalName())
		if gerr != nil && !domainerrors.IsNotFound(gerr) {
			return fmt.Errorf("check internal name: %w", gerr)
		}
		if gerr == nil && !existing.ID().Equals(attr.ID()) {
			return domainerrors.NewConflict(
				"another live attribute in this hierarchy already uses this internal name; "+
					"rename or archive it before restoring",
				"internal_name", attr.InternalName(), "type_definition_id", link.ID().String())
		}
	}
	return nil
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
	items, info := db.KeysetPage(page, items, db.KeysetTotal(page, total), func(a *domainattribute.Definition) string {
		return db.EncodeKeyset(a.ID().String())
	})
	out := &ListOutput{
		Items:    make([]domainattribute.Snapshot, 0, len(items)),
		PageInfo: info,
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
func (i *Interactor) normalizeQuantityConstraints(ctx context.Context, dt valueobjects.DataType, unitFamilyID string, constraints domainattribute.Constraints, def *valueobjects.Default) error {
	if dt != valueobjects.DataTypeQuantity {
		return nil
	}
	needs := def != nil && def.Static != nil
	for _, c := range constraints {
		switch c.Kind() {
		case domainattribute.KindMinValue, domainattribute.KindMaxValue, domainattribute.KindOneOf:
			needs = true
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
		if v.DataType() != valueobjects.DataTypeQuantity {
			return v, nil
		}
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
		case domainattribute.OneOf:
			vals := make([]valueobjects.Value, len(cc.Values))
			for j, v := range cc.Values {
				nv, err := rebase(v)
				if err != nil {
					return err
				}
				vals[j] = nv
			}
			constraints[idx] = domainattribute.OneOf{Values: vals}
		}
	}
	// A static default is validated against the (now base-normalised)
	// constraints, so it must be rebased to base too.
	if def != nil && def.Static != nil {
		nv, err := rebase(*def.Static)
		if err != nil {
			return err
		}
		def.Static = &nv
	}
	return nil
}

// computedDeps maps each computed-formula attribute in the hierarchy to the
// attribute names its formula reads, for cycle detection.
func (i *Interactor) computedDeps(ctx context.Context, attrs domainattribute.Repository, hierarchy []*domaintypedef.TypeDefinition) (map[string][]string, error) {
	full, err := i.computedRefs(ctx, attrs, hierarchy)
	if err != nil {
		return nil, err
	}
	deps := make(map[string][]string, len(full))
	for name, r := range full {
		deps[name] = r.all
	}
	return deps, nil
}

// formulaRefs is one formula's references, split by how they are read.
type formulaRefs struct {
	all  []string
	bare map[string]bool
}

// computedRefs collects every computed attribute's references in the
// hierarchy, recording which are read bare.
func (i *Interactor) computedRefs(ctx context.Context, attrs domainattribute.Repository, hierarchy []*domaintypedef.TypeDefinition) (map[string]formulaRefs, error) {
	out := map[string]formulaRefs{}
	for _, link := range hierarchy {
		list, err := domainattribute.ListAllForType(ctx, attrs, link.ID())
		if err != nil {
			return nil, err
		}
		for _, a := range list {
			c := a.Computed()
			if c == nil || c.Kind != domainattribute.ComputedFormula {
				continue
			}
			// A STORED formula that no longer parses is skipped, not
			// raised. This sweep walks every computed attribute in the
			// hierarchy to cycle-check the one being written, so raising here
			// failed the create or update of an UNRELATED attribute with an
			// "invalid formula" naming a formula the caller did not write —
			// an operator with a schema they could not edit. The materializer
			// reports the unparseable formula through the background-error
			// observer, which is where a pre-existing data problem belongs.
			refs, verr := c.Validate()
			if verr != nil {
				continue
			}
			scalar, serr := formulaScalarRefs(c.Formula)
			if serr != nil {
				continue
			}
			bare := make(map[string]bool, len(scalar))
			for _, r := range scalar {
				bare[r] = true
			}
			out[a.InternalName()] = formulaRefs{all: refs, bare: bare}
		}
	}
	return out, nil
}

// assertNoFormulaReadsScopedSource refuses making an attribute multi-valued,
// localizable or scopable while a formula reads it.
//
// Without it the guard on the formula side is one-way: the formula is written
// against a scalar source and stays valid on paper, while the source becomes
// something evaluation cannot represent. The failure would then appear on an
// attribute nobody edited.
func (i *Interactor) assertNoFormulaReadsScopedSource(
	ctx context.Context, typeDefs domaintypedef.Repository, attrs domainattribute.Repository,
	before domainattribute.Snapshot, in UpdateInput,
) error {
	gaining := (!before.MultiValued && in.MultiValued) ||
		(!before.Localizable && in.Localizable) ||
		(!before.Scopable && in.Scopable)
	if !gaining {
		return nil
	}
	td, err := typeDefs.Get(ctx, before.TypeDefinitionID)
	if err != nil {
		return err
	}
	hierarchy, err := apptypedef.Chain(ctx, typeDefs, td)
	if err != nil {
		return err
	}
	descendants, err := apptypedef.Descendants(ctx, typeDefs, td)
	if err != nil {
		return err
	}
	hierarchy = appendUnseenTypes(hierarchy, descendants)
	deps, err := i.computedRefs(ctx, attrs, hierarchy)
	if err != nil {
		return err
	}
	gainingMulti := !before.MultiValued && in.MultiValued
	gainingScope := (!before.Localizable && in.Localizable) || (!before.Scopable && in.Scopable)
	return refuseFormulaReaders(before.InternalName, deps, gainingMulti, gainingScope)
}

// refuseFormulaReaders refuses the structural change when a formula reads the
// attribute in a way the change would break.
//
// The two halves are INDEPENDENT. The bare-read relaxation applies to the
// multi-valued transition alone: a formula that aggregates the name —
// sum(x) — is asking for every member, so gaining members is the intended
// case. Gaining a locale or a channel is refused for ANY reader, aggregate
// included, because evaluation reads the base scope and would drop every
// scoped member.
//
// One `continue` used to implement the relaxation for both, so setting
// multi_valued and localizable in ONE request passed the guard entirely — and
// the materializer then folded only the base-scope members, reporting a
// subtotal as the total.
func refuseFormulaReaders(name string, deps map[string]formulaRefs, gainingMulti, gainingScope bool) error {
	for formulaName, refs := range deps {
		for _, ref := range refs.all {
			if ref != name {
				continue
			}
			if gainingScope {
				return domainerrors.NewConflict(
					"cannot make the attribute localizable or scopable: a computed formula reads it, "+
						"and evaluation reads the base scope only, so the scoped values would be ignored",
					"attribute", name, "formula", formulaName)
			}
			if gainingMulti && !refs.bare[ref] {
				continue
			}
			return domainerrors.NewConflict(
				"cannot make the attribute multi-valued: a computed formula reads it bare, "+
					"and a bare name must resolve to one value. Wrap it in an aggregate — "+
					"sum, count, avg, min or max",
				"attribute", name, "formula", formulaName)
		}
	}
	return nil
}

// formulaScalarRefs parses a formula and returns the names it reads bare.
func formulaScalarRefs(src string) ([]string, error) {
	expr, err := formula.Parse(src)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	return expr.ScalarRefs(), nil
}

// formulaRefKinds parses a formula and returns the names it reads bare and
// the names whose values must coerce to numbers.
func formulaRefKinds(src string) (scalar, numeric []string, err error) {
	expr, perr := formula.Parse(src)
	if perr != nil {
		return nil, nil, domainerrors.NewValidation(perr.Error())
	}
	return expr.ScalarRefs(), expr.NumericRefs(), nil
}

// assertFormulaSourcesAreScalar refuses a formula that reads a multi-valued
// attribute BARE, or a localizable or scopable one at all.
//
// A bare name must resolve to one value. A multi-valued source read bare
// collapsed to whichever member the repository returned last, so adding a
// member changed the answer with nothing to explain it — while the computed
// attribute stayed populated, queryable in FQL and counted toward
// completeness. Reading it inside an aggregate is fine, and is the point:
// `sum(line_totals)` says which member it wants, namely all of them.
//
// Localizable and scopable sources stay refused outright. Evaluation reads
// the base scope, and folding values that mean different things in different
// locales is not an aggregate anyone asked for; a schema author who wants one
// can compute per-locale attributes explicitly.
func assertFormulaSourcesAreScalar(ctx context.Context, attrs domainattribute.Repository,
	hierarchy []*domaintypedef.TypeDefinition, refs, scalarRefs, numericRefs []string) error {
	if len(refs) == 0 {
		return nil
	}
	access := uow.AccessFromContext(ctx)
	wanted := make(map[string]bool, len(refs))
	for _, r := range refs {
		wanted[r] = true
	}
	bare := make(map[string]bool, len(scalarRefs))
	for _, r := range scalarRefs {
		bare[r] = true
	}
	numeric := make(map[string]bool, len(numericRefs))
	for _, r := range numericRefs {
		numeric[r] = true
	}
	resolved := make(map[string]bool, len(refs))
	for _, link := range hierarchy {
		list, err := domainattribute.ListAllForType(ctx, attrs, link.ID())
		if err != nil {
			return err
		}
		for _, a := range list {
			if !wanted[a.InternalName()] {
				continue
			}
			resolved[a.InternalName()] = true
			// A computed attribute is materialized under system access and is
			// born unrestricted, so a formula over a source the caller cannot
			// read would republish that source's exact values to the caller —
			// a full field-ACL read bypass. The refusal is worded exactly like
			// the unresolved-reference refusal below, and it runs BEFORE the
			// shape checks, so neither the outcome nor the error metadata
			// confirms that a restricted name exists. CanRead is always true
			// for an admin, so only field-restricted principals are affected.
			if !access.CanRead(a.InternalName()) {
				return errUnknownFormulaSource(a.InternalName())
			}
			switch {
			case a.MultiValued() && bare[a.InternalName()]:
				return domainerrors.NewValidation(
					"a formula cannot read a multi-valued attribute directly: a bare name must resolve to "+
						"one value. Wrap it in an aggregate — sum, count, avg, min or max",
					"attribute", a.InternalName())
			case a.Localizable() || a.Scopable():
				return domainerrors.NewValidation(
					"a formula cannot read a localizable or scopable attribute: evaluation reads the base "+
						"scope only, so the scoped values would be ignored",
					"attribute", a.InternalName())
			case numeric[a.InternalName()] && !isNumericDataType(a.DataType()):
				return domainerrors.NewValidation(
					"a formula can only read or fold a numeric attribute: bool, integer, float, "+
						"decimal or quantity. "+
						"count() folds members of any type",
					"attribute", a.InternalName(), "data_type", a.DataType().String())
			}
		}
	}
	// A field-restricted principal must not learn which names exist. If an
	// unreadable reference were refused while an unresolved one were accepted,
	// the difference would be an existence oracle over restricted attribute
	// names, so BOTH are refused with one error. The cost is that forward
	// references — a formula written before its source attribute exists — are
	// no longer available to field-restricted principals. Unrestricted
	// principals and admins resolve to Admin access and keep them.
	if !access.Admin {
		for _, r := range refs {
			if !resolved[r] {
				return errUnknownFormulaSource(r)
			}
		}
	}
	return nil
}

// errUnknownFormulaSource is the ONE refusal a field-restricted principal sees
// for a formula reference that is unresolved or unreadable. The two cases must
// stay indistinguishable — in code, message and metadata — or the difference
// becomes an existence oracle over restricted attribute names.
func errUnknownFormulaSource(name string) error {
	return domainerrors.NewValidation(
		"the formula references an unknown attribute in this type hierarchy",
		"attribute", name)
}

// isNumericDataType reports whether values of the type coerce to numbers.
//
// Only these four reach the evaluator. A formula over any other type produced
// nothing at all for a bare read — and 0 for count(), because counting an
// empty numeric list is legitimately zero. That 0 was queryable in FQL,
// exported and counted toward completeness while the attribute held values,
// so a non-numeric source is now refused where it has to coerce, and counted
// as members where it does not.
func isNumericDataType(dt valueobjects.DataType) bool {
	switch dt {
	case valueobjects.DataTypeBool, valueobjects.DataTypeInteger,
		valueobjects.DataTypeFloat, valueobjects.DataTypeDecimal:
		return true
	case valueobjects.DataTypeQuantity:
		// A quantity evaluates as its magnitude in the family's BASE unit,
		// which is what makes `pack_price / pack_size` a price per base unit
		// whatever unit the pack was entered in.
		//
		// The evaluator already converted one (see toRat); only this check
		// refused it, so a cost-per-kilogram — the ordinary reason to divide
		// by a weight — could not be modelled at all.
		return true
	default:
		return false
	}
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

// appendUnseenTypes appends the types not already in the list, keyed by id.
// The chain and the root's descendant set overlap on the declaring type and
// its ancestors' other branches.
func appendUnseenTypes(into []*domaintypedef.TypeDefinition, more []*domaintypedef.TypeDefinition) []*domaintypedef.TypeDefinition {
	seen := make(map[string]bool, len(into))
	for _, t := range into {
		seen[t.ID().String()] = true
	}
	for _, t := range more {
		if seen[t.ID().String()] {
			continue
		}
		seen[t.ID().String()] = true
		into = append(into, t)
	}
	return into
}

// assertRollupResolves refuses a rollup whose relationship or target cannot be
// resolved.
//
// Three ways a rollup can be written and never produce anything:
//
//   - the relationship does not exist;
//   - the direction points the wrong way, so this type is never on the side
//     the traversal starts from;
//   - the aggregated attribute does not exist on the type the relationship
//     reaches.
//
// Each of those aggregates an empty set for ever, which looks identical to
// "no data yet". Reporting them here is the difference between a schema
// author fixing a typo and an operator asking why a total is blank.
func (i *Interactor) assertRollupResolves(
	ctx context.Context,
	td *domaintypedef.TypeDefinition,
	computed *domainattribute.Computed,
) error {
	spec := computed.Rollup
	access := uow.AccessFromContext(ctx)

	// The nil check comes AFTER the field-permission check below, not before
	// it. An interactor built without a relationship repository cannot resolve
	// a rollup at all, and returning early here would have skipped the
	// permission check too — a guard that fails open when a dependency is
	// missing is the shape of guard that stops working quietly.
	if i.relDefs == nil {
		if !access.CanRead(spec.Target) {
			return errUnresolvedRollup(spec)
		}
		return nil
	}

	// A field-restricted principal must not learn the shape of what it cannot
	// read, so every way this rollup can fail to resolve collapses into ONE
	// refusal for such a caller. Four distinguishable outcomes — no such
	// relationship, no such target, target not numeric, accepted — let it
	// enumerate the counterpart type's attribute names and their types.
	//
	// The formula path does exactly this, for exactly this reason.
	refuse := func(err error) error {
		if access.Admin {
			return err
		}
		return errUnresolvedRollup(spec)
	}

	def, err := i.relDefs.GetByInternalName(ctx, uow.TenantFromContext(ctx), spec.Relationship)
	if err != nil || def == nil {
		if err != nil && !domainerrors.IsNotFound(err) {
			return err
		}
		return refuse(domainerrors.NewValidation(
			"rollup relationship "+spec.Relationship+" does not exist",
			"relationship", spec.Relationship))
	}
	snapshot := def.Snapshot()

	// Which side this type sits on decides which direction can traverse.
	self := td.ID()
	isParent := snapshot.ParentTypeID == self
	isChild := snapshot.ChildTypeID == self
	switch spec.Direction {
	case "child":
		if !isParent {
			return refuse(domainerrors.NewValidation(
				"a child rollup over "+spec.Relationship+" starts from its parent type, which this is not",
				"direction", spec.Direction))
		}
	case "parent":
		if !isChild {
			return refuse(domainerrors.NewValidation(
				"a parent rollup over "+spec.Relationship+" starts from its child type, which this is not",
				"direction", spec.Direction))
		}
	default: // linked
		if !isParent && !isChild {
			return refuse(domainerrors.NewValidation(
				"this type is not an endpoint of "+spec.Relationship,
				"relationship", spec.Relationship))
		}
	}

	// count needs no target; every other aggregate reads one on the far side.
	if spec.Aggregate == domainattribute.RollupCount {
		return nil
	}
	counterpart := snapshot.ChildTypeID
	if spec.Direction == "parent" || (spec.Direction == "linked" && isChild) {
		counterpart = snapshot.ParentTypeID
	}
	target, err := i.attrs.GetByInternalName(ctx, counterpart, spec.Target)
	if err != nil && !domainerrors.IsNotFound(err) {
		return err
	}
	if target == nil {
		return refuse(domainerrors.NewValidation(
			"rollup target "+spec.Target+" does not exist on the type "+spec.Relationship+" reaches",
			"target", spec.Target))
	}
	// THE ROLLUP READS THIS ATTRIBUTE, so the caller defining it must be able
	// to read it too.
	//
	// A rollup is materialized under system access and its result is stored as
	// an ordinary attribute, born with no restriction of its own. So a
	// principal denied `cost` could define sum(child(has_line).cost) and read
	// the total; min and max are worse, republishing an exact hidden value
	// verbatim. That is a full read bypass, and it is the same hole #509
	// closed on the formula path — reopened here when rollups arrived.
	//
	// Refused with the SAME error as an unresolved rollup, and before the
	// shape check below, so neither the outcome nor its metadata confirms the
	// attribute exists. CanRead is always true for an admin.
	if !access.CanRead(spec.Target) {
		return refuse(domainerrors.NewValidation(
			"rollup target "+spec.Target+" does not exist on the type "+spec.Relationship+" reaches",
			"target", spec.Target))
	}
	if !isAggregatable(target.Snapshot().DataType) {
		return refuse(domainerrors.NewValidation(
			"rollup target "+spec.Target+" is not numeric, so it cannot be aggregated",
			"target", spec.Target))
	}
	return nil
}

// errUnresolvedRollup is the single refusal a field-restricted principal gets
// for every way a rollup fails to resolve, so the outcome carries no
// information about what exists on the far side.
func errUnresolvedRollup(spec *domainattribute.Rollup) error {
	return domainerrors.NewValidation(
		"this rollup does not resolve: check the relationship, the direction and the target",
		"relationship", spec.Relationship)
}

// isAggregatable reports whether sum, min and max mean anything for a type.
func isAggregatable(dt valueobjects.DataType) bool {
	switch dt {
	case valueobjects.DataTypeInteger, valueobjects.DataTypeFloat,
		valueobjects.DataTypeDecimal, valueobjects.DataTypeQuantity:
		return true
	default:
		return false
	}
}
