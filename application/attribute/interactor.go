// Package attribute holds the attribute-definition usecases.
package attribute

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// Interactor implements the attribute-definition usecases.
type Interactor struct {
	uow      uow.UnitOfWork
	typeDefs domaintypedef.Repository
	attrs    domainattribute.Repository
	now      func() time.Time
}

// NewInteractor wires the attribute-definition usecases.
func NewInteractor(u uow.UnitOfWork, typeDefs domaintypedef.Repository, attrs domainattribute.Repository) *Interactor {
	return &Interactor{uow: u, typeDefs: typeDefs, attrs: attrs, now: time.Now}
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
	Constraints      json.RawMessage
	DefaultValue     json.RawMessage
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

	var snap domainattribute.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		typeDefs := i.typeDefs.WithTx(tx)
		attrs := i.attrs.WithTx(tx)

		td, err := typeDefs.GetForUpdate(ctx, typeDefID)
		if err != nil {
			return err
		}
		if td.IsArchived() {
			return domainerrors.NewArchived(domaintypedef.AggregateType, td.ID().String())
		}

		existing, err := attrs.GetByInternalName(ctx, typeDefID, in.InternalName)
		if err != nil && !domainerrors.IsNotFound(err) {
			return fmt.Errorf("check internal name: %w", err)
		}
		if existing != nil {
			return domainerrors.NewConflict("internal name already in use", "internal_name", in.InternalName)
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
			Constraints:      constraints,
			DefaultValue:     defaultValue,
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
	Constraints  json.RawMessage
	DefaultValue json.RawMessage
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

	var snap domainattribute.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		attrs := i.attrs.WithTx(tx)

		attr, err := attrs.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		before := attr.Snapshot()

		evts, err := attr.Update(domainattribute.UpdateInput{
			DisplayName:  in.DisplayName,
			Description:  in.Description,
			Required:     in.Required,
			MultiValued:  in.MultiValued,
			Unique:       in.Unique,
			Constraints:  constraints,
			DefaultValue: defaultValue,
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
