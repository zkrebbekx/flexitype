// Package dependency holds the attribute-dependency usecases, including
// effective-schema resolution for building cascading UIs.
package dependency

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// Interactor implements the dependency usecases.
type Interactor struct {
	uow      uow.UnitOfWork
	typeDefs domaintypedef.Repository
	attrs    domainattribute.Repository
	values   domainvalue.Repository
	deps     domaindependency.Repository
	now      func() time.Time
}

// NewInteractor wires the dependency usecases.
func NewInteractor(u uow.UnitOfWork, typeDefs domaintypedef.Repository, attrs domainattribute.Repository, values domainvalue.Repository, deps domaindependency.Repository) *Interactor {
	return &Interactor{uow: u, typeDefs: typeDefs, attrs: attrs, values: values, deps: deps, now: time.Now}
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

		// Both attributes must live on one hierarchy chain so every entity
		// holding the target also holds (or inherits) the source.
		if err := i.checkSameChain(ctx, tx, source, target); err != nil {
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

	out := &ListOutput{
		Items:    make([]domaindependency.Snapshot, 0, len(items)),
		PageInfo: db.BuildPageInfo(page, len(items), total),
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
	targeting, err := i.deps.ListByTarget(ctx, attrID)
	if err != nil {
		return nil, err
	}

	sourceValues := make(map[valueobjects.AttributeDefinitionID]valueobjects.Value)
	if len(targeting) > 0 {
		entityValues, err := i.values.ListByEntity(ctx, domainvalue.EntityKey{
			TenantID:         def.TenantID(),
			TypeDefinitionID: def.TypeDefinitionID(),
			EntityID:         entityID,
		})
		if err != nil {
			return nil, err
		}
		for _, av := range entityValues {
			sourceValues[av.AttributeDefinitionID()] = av.Value()
		}
	}

	schema, err := domaindependency.ResolveEffective(def, targeting, sourceValues, i.now())
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
