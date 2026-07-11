// Package value holds the attribute-value usecases, including the Set flow
// that validates values against the definition, its constraints and every
// matched attribute dependency before writing.
package value

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// Interactor implements the attribute-value usecases.
type Interactor struct {
	uow    uow.UnitOfWork
	attrs  domainattribute.Repository
	values domainvalue.Repository
	deps   domaindependency.Repository
	now    func() time.Time
}

// NewInteractor wires the attribute-value usecases.
func NewInteractor(u uow.UnitOfWork, attrs domainattribute.Repository, values domainvalue.Repository, deps domaindependency.Repository) *Interactor {
	return &Interactor{uow: u, attrs: attrs, values: values, deps: deps, now: time.Now}
}

// SetInput holds data for writing one attribute value. Value is the raw
// JSON scalar, decoded against the attribute's data type.
type SetInput struct {
	AttributeDefinitionID string
	EntityID              string
	Value                 json.RawMessage
}

// Set writes a value for an entity attribute: it locks the definition,
// decodes and validates the value (type, constraints, dependencies,
// uniqueness), then inserts a new value or updates the existing one for
// single-valued attributes.
func (i *Interactor) Set(ctx context.Context, in SetInput) (*domainvalue.Snapshot, error) {
	defID, err := valueobjects.ParseAttributeDefinitionID(in.AttributeDefinitionID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	entityID, err := valueobjects.ParseEntityID(in.EntityID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	if len(in.Value) == 0 || string(in.Value) == "null" {
		return nil, domainerrors.NewValidation("value is required")
	}

	var snap domainvalue.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		attrs := i.attrs.WithTx(tx)
		values := i.values.WithTx(tx)

		// Lock the definition: value validity depends on it, so definition
		// updates and value writes serialize.
		def, err := attrs.GetForUpdate(ctx, defID)
		if err != nil {
			return err
		}

		v, err := valueobjects.ParseValue(def.DataType(), in.Value)
		if err != nil {
			return domainerrors.NewValidation(err.Error())
		}

		if err := i.checkDependencies(ctx, tx, def, entityID, v); err != nil {
			return err
		}
		if def.Unique() {
			count, err := values.CountByDefinitionAndValue(ctx, defID, v, entityID)
			if err != nil {
				return fmt.Errorf("check uniqueness: %w", err)
			}
			if count > 0 {
				return domainerrors.NewConflict("value already used by another entity",
					"attribute", def.InternalName(), "value", v.String())
			}
		}

		existing, err := values.FindByDefinitionAndEntity(ctx, defID, entityID)
		if err != nil {
			return fmt.Errorf("load existing values: %w", err)
		}

		// Single-valued attributes upsert; multi-valued attributes append
		// unless the exact value is already present.
		if !def.MultiValued() && len(existing) > 0 {
			av := existing[0]
			before := av.Snapshot()
			evts, err := av.UpdateValue(def, v, i.now())
			if err != nil {
				return err
			}
			snap = av.Snapshot()
			if len(evts) == 0 {
				return nil
			}
			if err := values.Save(ctx, av); err != nil {
				return fmt.Errorf("save attribute value: %w", err)
			}
			c.CollectEvents(evts...)
			c.RecordChange(activity.Change{
				Entity:   domainvalue.AggregateType,
				EntityID: av.ID().String(),
				Action:   activity.ActionUpdated,
				Before:   before,
				After:    snap,
			})
			return nil
		}
		for _, av := range existing {
			if av.Value().Equal(v) {
				snap = av.Snapshot()
				return nil
			}
		}

		av, evts, err := domainvalue.New(def, entityID, v, i.now())
		if err != nil {
			return err
		}
		if err := values.Save(ctx, av); err != nil {
			return fmt.Errorf("save attribute value: %w", err)
		}

		snap = av.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domainvalue.AggregateType,
			EntityID: av.ID().String(),
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

// checkDependencies resolves the effective schema for the target attribute
// given the entity's current source values and validates v against it.
func (i *Interactor) checkDependencies(
	ctx context.Context,
	tx db.Transactor,
	def *domainattribute.Definition,
	entityID valueobjects.EntityID,
	v valueobjects.Value,
) error {
	deps := i.deps.WithTx(tx)
	values := i.values.WithTx(tx)

	targeting, err := deps.ListByTarget(ctx, def.ID())
	if err != nil {
		return fmt.Errorf("load dependencies: %w", err)
	}
	if len(targeting) == 0 {
		return nil
	}

	entityValues, err := values.ListByEntity(ctx, domainvalue.EntityKey{
		TenantID:         def.TenantID(),
		TypeDefinitionID: def.TypeDefinitionID(),
		EntityID:         entityID,
	})
	if err != nil {
		return fmt.Errorf("load entity values: %w", err)
	}
	sourceValues := make(map[valueobjects.AttributeDefinitionID]valueobjects.Value, len(entityValues))
	for _, av := range entityValues {
		sourceValues[av.AttributeDefinitionID()] = av.Value()
	}

	schema, err := domaindependency.ResolveEffective(def, targeting, sourceValues, i.now())
	if err != nil {
		return fmt.Errorf("resolve effective schema: %w", err)
	}
	return schema.Check(v)
}

// Remove archives a stored value.
func (i *Interactor) Remove(ctx context.Context, rawID string) (*domainvalue.Snapshot, error) {
	id, err := valueobjects.ParseAttributeValueID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	var snap domainvalue.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		values := i.values.WithTx(tx)

		av, err := values.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		before := av.Snapshot()

		evts, err := av.Remove(i.now())
		if err != nil {
			return err
		}
		if err := values.Save(ctx, av); err != nil {
			return fmt.Errorf("save attribute value: %w", err)
		}

		snap = av.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domainvalue.AggregateType,
			EntityID: av.ID().String(),
			Action:   activity.ActionRemoved,
			Before:   before,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// Get loads one stored value by ID.
func (i *Interactor) Get(ctx context.Context, rawID string) (*domainvalue.Snapshot, error) {
	id, err := valueobjects.ParseAttributeValueID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	av, err := i.values.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	snap := av.Snapshot()
	return &snap, nil
}

// ListByEntity loads every live value of one entity — the hydration hot
// path; concurrent calls for different entities batch into one query.
func (i *Interactor) ListByEntity(ctx context.Context, rawTypeDefID, rawEntityID string) ([]domainvalue.Snapshot, error) {
	typeDefID, err := valueobjects.ParseTypeDefinitionID(rawTypeDefID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	entityID, err := valueobjects.ParseEntityID(rawEntityID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	items, err := i.values.ListByEntity(ctx, domainvalue.EntityKey{
		TenantID:         uow.TenantFromContext(ctx),
		TypeDefinitionID: typeDefID,
		EntityID:         entityID,
	})
	if err != nil {
		return nil, err
	}
	snaps := make([]domainvalue.Snapshot, 0, len(items))
	for _, av := range items {
		snaps = append(snaps, av.Snapshot())
	}
	return snaps, nil
}

// EntitySummaryOutput is one entity-browser row.
type EntitySummaryOutput struct {
	EntityID      string    `json:"entity_id"`
	ValueCount    int       `json:"value_count"`
	LastUpdatedAt time.Time `json:"last_updated_at"`
}

// EntityListOutput is one page of the entity browser.
type EntityListOutput struct {
	Items    []EntitySummaryOutput
	PageInfo db.PageInfo
}

// ListEntities pages the distinct entities holding live values of a type
// definition — the observability entry point for the admin console.
func (i *Interactor) ListEntities(ctx context.Context, rawTypeDefID string, args db.PageArgs) (*EntityListOutput, error) {
	typeDefID, err := valueobjects.ParseTypeDefinitionID(rawTypeDefID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	page, err := args.Resolve()
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	items, total, err := i.values.ListEntities(ctx, uow.TenantFromContext(ctx), typeDefID, page)
	if err != nil {
		return nil, err
	}

	out := &EntityListOutput{
		Items:    make([]EntitySummaryOutput, 0, len(items)),
		PageInfo: db.BuildPageInfo(page, len(items), total),
	}
	for _, e := range items {
		out.Items = append(out.Items, EntitySummaryOutput{
			EntityID:      e.EntityID.String(),
			ValueCount:    e.ValueCount,
			LastUpdatedAt: e.LastUpdatedAt,
		})
	}
	return out, nil
}

// ListInput holds filter and pagination arguments for List.
type ListInput struct {
	TypeDefinitionID      string
	AttributeDefinitionID string
	EntityID              string
	IncludeArchived       bool
	Page                  db.PageArgs
}

// ListOutput is one page of stored values.
type ListOutput struct {
	Items    []domainvalue.Snapshot
	PageInfo db.PageInfo
}

// List returns a filtered, paginated set of stored values.
func (i *Interactor) List(ctx context.Context, in ListInput) (*ListOutput, error) {
	page, err := in.Page.Resolve()
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	filter := domainvalue.Filter{
		TenantID:        uow.TenantFromContext(ctx),
		IncludeArchived: in.IncludeArchived,
	}
	if in.TypeDefinitionID != "" {
		if filter.TypeDefinitionID, err = valueobjects.ParseTypeDefinitionID(in.TypeDefinitionID); err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
	}
	if in.AttributeDefinitionID != "" {
		if filter.AttributeDefinitionID, err = valueobjects.ParseAttributeDefinitionID(in.AttributeDefinitionID); err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
	}
	if in.EntityID != "" {
		if filter.EntityID, err = valueobjects.ParseEntityID(in.EntityID); err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
	}

	items, total, err := i.values.List(ctx, filter, page)
	if err != nil {
		return nil, err
	}

	out := &ListOutput{
		Items:    make([]domainvalue.Snapshot, 0, len(items)),
		PageInfo: db.BuildPageInfo(page, len(items), total),
	}
	for _, av := range items {
		out.Items = append(out.Items, av.Snapshot())
	}
	return out, nil
}
