// Package typedef holds the type-definition usecases.
package typedef

import (
	"context"
	"fmt"
	"sort"
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

// Interactor implements the type-definition usecases. Writes run inside the
// unit of work; reads go straight to the dataloader-backed repository.
type Interactor struct {
	uow      uow.UnitOfWork
	typeDefs domaintypedef.Repository
	attrs    domainattribute.Repository
	now      func() time.Time
}

// NewInteractor wires the type-definition usecases.
func NewInteractor(u uow.UnitOfWork, typeDefs domaintypedef.Repository, attrs domainattribute.Repository) *Interactor {
	return &Interactor{uow: u, typeDefs: typeDefs, attrs: attrs, now: uow.UTCNow}
}

// CreateInput holds data for creating a type definition.
type CreateInput struct {
	InternalName string
	DisplayName  string
	Description  string
	// ExtendsID makes the new type a subtype of an existing one; immutable
	// after creation.
	ExtendsID string
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

		var extends *domaintypedef.TypeDefinition
		if in.ExtendsID != "" {
			extendsID, perr := valueobjects.ParseTypeDefinitionID(in.ExtendsID)
			if perr != nil {
				return domainerrors.NewValidation("extends: " + perr.Error())
			}
			if extends, err = repo.Get(ctx, extendsID); err != nil {
				return err
			}
			// The chain walk both validates depth and proves the parent's
			// lineage is acyclic before we hang a new subtype off it.
			if _, err := Ancestors(ctx, repo, extends); err != nil {
				return err
			}
		}

		td, evts, err := domaintypedef.New(domaintypedef.NewInput{
			TenantID:     tenant,
			InternalName: in.InternalName,
			DisplayName:  in.DisplayName,
			Description:  in.Description,
			Extends:      extends,
		}, i.now())
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
		if err := uow.EnsureTenant(ctx, td.TenantID(), domaintypedef.AggregateType, in.ID); err != nil {
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
		if err := uow.EnsureTenant(ctx, td.TenantID(), domaintypedef.AggregateType, rawID); err != nil {
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
	if err := uow.EnsureTenant(ctx, td.TenantID(), domaintypedef.AggregateType, rawID); err != nil {
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

// EffectiveAttribute pairs an attribute with the type that declares it —
// the shape the console renders "Declared here" vs "Inherited from X"
// from.
type EffectiveAttribute struct {
	Attribute  domainattribute.Snapshot `json:"attribute"`
	DeclaredIn domaintypedef.Snapshot   `json:"declared_in"`
}

// EffectiveAttributes resolves a type's full inherited attribute set: own
// attributes first, then each ancestor's, root last. Per-type loads batch
// through the windowed dataloader into one query.
func (i *Interactor) EffectiveAttributes(ctx context.Context, rawID string) ([]EffectiveAttribute, error) {
	id, err := valueobjects.ParseTypeDefinitionID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	t, err := i.typeDefs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := uow.EnsureTenant(ctx, t.TenantID(), domaintypedef.AggregateType, rawID); err != nil {
		return nil, err
	}
	chain, err := Chain(ctx, i.typeDefs, t)
	if err != nil {
		return nil, err
	}
	access := uow.AccessFromContext(ctx)

	var out []EffectiveAttribute
	for _, link := range chain {
		attrs, _, err := i.attrs.ListByTypeDefinition(ctx, link.ID(), db.Page{Limit: 500})
		if err != nil {
			return nil, err
		}
		for _, a := range attrs {
			// Field-level access control: hide attributes the principal may
			// not read (admins and unauthenticated development see all).
			if !access.CanRead(a.InternalName()) {
				continue
			}
			out = append(out, EffectiveAttribute{Attribute: a.Snapshot(), DeclaredIn: link.Snapshot()})
		}
	}
	// Present grouped and ordered: inherited attributes merge into the same
	// named group as own ones, and sort_order controls order within a group
	// (ungrouped attributes sort first). The sort is stable, so attributes
	// with equal (group, sort_order) — e.g. all ungrouped ones — keep their
	// original declaration/inheritance order.
	sort.SliceStable(out, func(x, y int) bool {
		a, b := out[x].Attribute, out[y].Attribute
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		return a.SortOrder < b.SortOrder
	})
	return out, nil
}

// Children returns a type's direct subtypes.
func (i *Interactor) Children(ctx context.Context, rawID string) ([]domaintypedef.Snapshot, error) {
	id, err := valueobjects.ParseTypeDefinitionID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	parent, err := i.typeDefs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := uow.EnsureTenant(ctx, parent.TenantID(), domaintypedef.AggregateType, rawID); err != nil {
		return nil, err
	}
	children, err := i.typeDefs.ListChildren(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]domaintypedef.Snapshot, 0, len(children))
	for _, c := range children {
		out = append(out, c.Snapshot())
	}
	return out, nil
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

	items, info := db.KeysetPage(page, items, db.KeysetTotal(page, total), func(t *domaintypedef.TypeDefinition) string {
		return db.EncodeKeyset(t.ID().String())
	})
	out := &ListOutput{
		Items:    make([]domaintypedef.Snapshot, 0, len(items)),
		PageInfo: info,
	}
	for _, td := range items {
		out.Items = append(out.Items, td.Snapshot())
	}
	return out, nil
}
