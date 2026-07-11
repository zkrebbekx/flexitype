// Package typedef holds the type-definition usecases.
package typedef

import (
	"context"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// Interactor implements the type-definition usecases. Writes run inside the
// unit of work; reads go straight to the dataloader-backed repository.
type Interactor struct {
	uow      uow.UnitOfWork
	typeDefs domaintypedef.Repository
	now      func() time.Time
}

// NewInteractor wires the type-definition usecases.
func NewInteractor(u uow.UnitOfWork, typeDefs domaintypedef.Repository) *Interactor {
	return &Interactor{uow: u, typeDefs: typeDefs, now: time.Now}
}

// CreateInput holds data for creating a type definition.
type CreateInput struct {
	InternalName string
	DisplayName  string
	Description  string
}

// Create creates a type definition, guarding internal-name uniqueness
// inside the transaction.
func (i *Interactor) Create(ctx context.Context, in CreateInput) (*domaintypedef.Snapshot, error) {
	tenant := uow.TenantFromContext(ctx)

	var snap domaintypedef.Snapshot
	err := i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		repo := i.typeDefs.WithTx(tx)

		existing, err := repo.GetByInternalName(ctx, tenant, in.InternalName)
		if err != nil && !domainerrors.IsNotFound(err) {
			return fmt.Errorf("check internal name: %w", err)
		}
		if existing != nil {
			return domainerrors.NewConflict("internal name already in use", "internal_name", in.InternalName)
		}

		td, evts, err := domaintypedef.New(tenant, in.InternalName, in.DisplayName, in.Description, i.now())
		if err != nil {
			return err
		}
		if err := repo.Save(ctx, td); err != nil {
			return fmt.Errorf("save type definition: %w", err)
		}

		snap = td.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domaintypedef.AggregateType,
			EntityID: td.ID().String(),
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

// UpdateInput holds data for updating a type definition.
type UpdateInput struct {
	ID          string
	DisplayName string
	Description string
}

// Update changes a type definition's display fields.
func (i *Interactor) Update(ctx context.Context, in UpdateInput) (*domaintypedef.Snapshot, error) {
	id, err := valueobjects.ParseTypeDefinitionID(in.ID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	var snap domaintypedef.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		repo := i.typeDefs.WithTx(tx)

		td, err := repo.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		before := td.Snapshot()

		evts, err := td.Update(in.DisplayName, in.Description, i.now())
		if err != nil {
			return err
		}
		if len(evts) == 0 {
			snap = before
			return nil
		}
		if err := repo.Save(ctx, td); err != nil {
			return fmt.Errorf("save type definition: %w", err)
		}

		snap = td.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domaintypedef.AggregateType,
			EntityID: td.ID().String(),
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

// Archive soft-deletes a type definition.
func (i *Interactor) Archive(ctx context.Context, rawID string) (*domaintypedef.Snapshot, error) {
	return i.transition(ctx, rawID, activity.ActionArchived)
}

// Restore reverses an Archive.
func (i *Interactor) Restore(ctx context.Context, rawID string) (*domaintypedef.Snapshot, error) {
	return i.transition(ctx, rawID, activity.ActionRestored)
}

func (i *Interactor) transition(ctx context.Context, rawID string, action activity.Action) (*domaintypedef.Snapshot, error) {
	id, err := valueobjects.ParseTypeDefinitionID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	var snap domaintypedef.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		repo := i.typeDefs.WithTx(tx)

		td, err := repo.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		before := td.Snapshot()

		var evts []events.Event
		switch action {
		case activity.ActionArchived:
			evts, err = td.Archive(i.now())
		default:
			evts, err = td.Restore(i.now())
		}
		if err != nil {
			return err
		}
		if err := repo.Save(ctx, td); err != nil {
			return fmt.Errorf("save type definition: %w", err)
		}

		snap = td.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domaintypedef.AggregateType,
			EntityID: td.ID().String(),
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

// Get loads one type definition by ID.
func (i *Interactor) Get(ctx context.Context, rawID string) (*domaintypedef.Snapshot, error) {
	id, err := valueobjects.ParseTypeDefinitionID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	td, err := i.typeDefs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	snap := td.Snapshot()
	return &snap, nil
}

// GetByInternalName loads one type definition by machine name.
func (i *Interactor) GetByInternalName(ctx context.Context, internalName string) (*domaintypedef.Snapshot, error) {
	td, err := i.typeDefs.GetByInternalName(ctx, uow.TenantFromContext(ctx), internalName)
	if err != nil {
		return nil, err
	}
	snap := td.Snapshot()
	return &snap, nil
}

// ListInput holds filter and pagination arguments for List.
type ListInput struct {
	InternalNames   []string
	IncludeArchived bool
	Page            db.PageArgs
}

// ListOutput is one page of type definitions.
type ListOutput struct {
	Items    []domaintypedef.Snapshot
	PageInfo db.PageInfo
}

// List returns a filtered, paginated set of type definitions.
func (i *Interactor) List(ctx context.Context, in ListInput) (*ListOutput, error) {
	page, err := in.Page.Resolve()
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	items, total, err := i.typeDefs.List(ctx, domaintypedef.Filter{
		TenantID:        uow.TenantFromContext(ctx),
		InternalNames:   in.InternalNames,
		IncludeArchived: in.IncludeArchived,
	}, page)
	if err != nil {
		return nil, err
	}

	out := &ListOutput{
		Items:    make([]domaintypedef.Snapshot, 0, len(items)),
		PageInfo: db.BuildPageInfo(page, len(items), total),
	}
	for _, td := range items {
		out.Items = append(out.Items, td.Snapshot())
	}
	return out, nil
}
