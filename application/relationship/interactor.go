// Package relationship holds the relationship usecases: defining
// relationship types between entity types (with their own attribute sets
// and inheritance) and linking entities under them.
package relationship

import (
	"context"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// maxInheritanceDepth bounds extends-chain walks; deeper chains indicate a
// modelling mistake (or a cycle that slipped past creation-time checks).
const maxInheritanceDepth = 10

// Interactor implements the relationship usecases.
type Interactor struct {
	uow      uow.UnitOfWork
	typeDefs domaintypedef.Repository
	defs     domainrelationship.DefinitionRepository
	links    domainrelationship.Repository
	now      func() time.Time
}

// NewInteractor wires the relationship usecases.
func NewInteractor(u uow.UnitOfWork, typeDefs domaintypedef.Repository, defs domainrelationship.DefinitionRepository, links domainrelationship.Repository) *Interactor {
	return &Interactor{uow: u, typeDefs: typeDefs, defs: defs, links: links, now: time.Now}
}

// CreateDefinitionInput holds data for creating a relationship definition.
type CreateDefinitionInput struct {
	InternalName string
	DisplayName  string
	Description  string
	// Kind is directed (default) or symmetric.
	Kind                string
	ParentTypeID        string
	ChildTypeID         string
	ParentLabel         string
	ChildLabel          string
	ExtendsID           string
	ParentVersionPolicy string
	ChildVersionPolicy  string
}

// CreateDefinition creates a relationship definition together with its
// hidden attribute-set type, in one transaction.
func (i *Interactor) CreateDefinition(ctx context.Context, in CreateDefinitionInput) (*domainrelationship.DefinitionSnapshot, error) {
	parentTypeID, err := valueobjects.ParseTypeDefinitionID(in.ParentTypeID)
	if err != nil {
		return nil, domainerrors.NewValidation("parent type: " + err.Error())
	}
	childTypeID, err := valueobjects.ParseTypeDefinitionID(in.ChildTypeID)
	if err != nil {
		return nil, domainerrors.NewValidation("child type: " + err.Error())
	}
	tenant := uow.TenantFromContext(ctx)

	var snap domainrelationship.DefinitionSnapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		typeDefs := i.typeDefs.WithTx(tx)
		defs := i.defs.WithTx(tx)

		existing, err := defs.GetByInternalName(ctx, tenant, in.InternalName)
		if err != nil && !domainerrors.IsNotFound(err) {
			return fmt.Errorf("check internal name: %w", err)
		}
		if existing != nil {
			return domainerrors.NewConflict("internal name already in use", "internal_name", in.InternalName)
		}

		parentType, err := typeDefs.Get(ctx, parentTypeID)
		if err != nil {
			return err
		}
		childType, err := typeDefs.Get(ctx, childTypeID)
		if err != nil {
			return err
		}

		var extends *domainrelationship.Definition
		if in.ExtendsID != "" {
			extendsID, perr := valueobjects.ParseRelationshipDefinitionID(in.ExtendsID)
			if perr != nil {
				return domainerrors.NewValidation("extends: " + perr.Error())
			}
			if extends, err = defs.Get(ctx, extendsID); err != nil {
				return err
			}
		}

		// The hidden companion type that carries this definition's
		// attributes; link values anchor to it with the relationship ID as
		// the entity.
		attrSet, setEvents, err := domaintypedef.NewAttributeSet(
			tenant, "rel_"+in.InternalName+"_attrs", in.DisplayName+" (link attributes)", i.now())
		if err != nil {
			return err
		}
		if err := typeDefs.Save(ctx, attrSet); err != nil {
			return fmt.Errorf("save attribute set: %w", err)
		}

		def, defEvents, err := domainrelationship.NewDefinition(domainrelationship.NewDefinitionInput{
			TenantID:            tenant,
			InternalName:        in.InternalName,
			DisplayName:         in.DisplayName,
			Description:         in.Description,
			Kind:                domainrelationship.Kind(in.Kind),
			ParentType:          parentType,
			ChildType:           childType,
			ParentLabel:         in.ParentLabel,
			ChildLabel:          in.ChildLabel,
			AttributeSet:        attrSet,
			Extends:             extends,
			ParentVersionPolicy: domainrelationship.VersionPolicy(in.ParentVersionPolicy),
			ChildVersionPolicy:  domainrelationship.VersionPolicy(in.ChildVersionPolicy),
		}, i.now())
		if err != nil {
			return err
		}
		if err := defs.Save(ctx, def); err != nil {
			return fmt.Errorf("save relationship definition: %w", err)
		}

		snap = def.Snapshot()
		c.CollectEvents(setEvents...)
		c.CollectEvents(defEvents...)
		c.RecordChange(activity.Change{
			Entity:   domainrelationship.DefinitionAggregateType,
			EntityID: def.ID().String(),
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

// UpdateDefinitionInput holds data for updating a relationship definition.
type UpdateDefinitionInput struct {
	ID                  string
	DisplayName         string
	Description         string
	ParentLabel         string
	ChildLabel          string
	ParentVersionPolicy string
	ChildVersionPolicy  string
}

// UpdateDefinition mutates a relationship definition.
func (i *Interactor) UpdateDefinition(ctx context.Context, in UpdateDefinitionInput) (*domainrelationship.DefinitionSnapshot, error) {
	id, err := valueobjects.ParseRelationshipDefinitionID(in.ID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	var snap domainrelationship.DefinitionSnapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		defs := i.defs.WithTx(tx)

		def, err := defs.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		before := def.Snapshot()

		evts, err := def.Update(domainrelationship.UpdateDefinitionInput{
			DisplayName:         in.DisplayName,
			Description:         in.Description,
			ParentLabel:         in.ParentLabel,
			ChildLabel:          in.ChildLabel,
			ParentVersionPolicy: domainrelationship.VersionPolicy(in.ParentVersionPolicy),
			ChildVersionPolicy:  domainrelationship.VersionPolicy(in.ChildVersionPolicy),
		}, i.now())
		if err != nil {
			return err
		}
		snap = def.Snapshot()
		if len(evts) == 0 {
			return nil
		}
		if err := defs.Save(ctx, def); err != nil {
			return fmt.Errorf("save relationship definition: %w", err)
		}

		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domainrelationship.DefinitionAggregateType,
			EntityID: def.ID().String(),
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

// ArchiveDefinition soft-deletes a relationship definition.
func (i *Interactor) ArchiveDefinition(ctx context.Context, rawID string) (*domainrelationship.DefinitionSnapshot, error) {
	return i.transitionDefinition(ctx, rawID, activity.ActionArchived)
}

// RestoreDefinition reverses an ArchiveDefinition.
func (i *Interactor) RestoreDefinition(ctx context.Context, rawID string) (*domainrelationship.DefinitionSnapshot, error) {
	return i.transitionDefinition(ctx, rawID, activity.ActionRestored)
}

func (i *Interactor) transitionDefinition(ctx context.Context, rawID string, action activity.Action) (*domainrelationship.DefinitionSnapshot, error) {
	id, err := valueobjects.ParseRelationshipDefinitionID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	var snap domainrelationship.DefinitionSnapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		defs := i.defs.WithTx(tx)

		def, err := defs.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		before := def.Snapshot()

		var evts []events.Event
		switch action {
		case activity.ActionArchived:
			evts, err = def.Archive(i.now())
		default:
			evts, err = def.Restore(i.now())
		}
		if err != nil {
			return err
		}
		if err := defs.Save(ctx, def); err != nil {
			return fmt.Errorf("save relationship definition: %w", err)
		}

		snap = def.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domainrelationship.DefinitionAggregateType,
			EntityID: def.ID().String(),
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

// GetDefinition loads one relationship definition by ID.
func (i *Interactor) GetDefinition(ctx context.Context, rawID string) (*domainrelationship.DefinitionSnapshot, error) {
	id, err := valueobjects.ParseRelationshipDefinitionID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	def, err := i.defs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	snap := def.Snapshot()
	return &snap, nil
}

// DefinitionListInput holds filter and pagination arguments.
type DefinitionListInput struct {
	TypeDefinitionID string
	IncludeArchived  bool
	Page             db.PageArgs
}

// DefinitionListOutput is one page of relationship definitions.
type DefinitionListOutput struct {
	Items    []domainrelationship.DefinitionSnapshot
	PageInfo db.PageInfo
}

// ListDefinitions returns a filtered, paginated set of relationship
// definitions.
func (i *Interactor) ListDefinitions(ctx context.Context, in DefinitionListInput) (*DefinitionListOutput, error) {
	page, err := in.Page.Resolve()
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	filter := domainrelationship.DefinitionFilter{
		TenantID:        uow.TenantFromContext(ctx),
		IncludeArchived: in.IncludeArchived,
	}
	if in.TypeDefinitionID != "" {
		typeID, perr := valueobjects.ParseTypeDefinitionID(in.TypeDefinitionID)
		if perr != nil {
			return nil, domainerrors.NewValidation(perr.Error())
		}
		// Endpoints are polymorphic: a subtype participates in every
		// relationship its ancestors do, so match the whole chain.
		t, terr := i.typeDefs.Get(ctx, typeID)
		if terr != nil {
			return nil, terr
		}
		chain, terr := apptypedef.Chain(ctx, i.typeDefs, t)
		if terr != nil {
			return nil, terr
		}
		for _, link := range chain {
			filter.TypeDefinitionIDs = append(filter.TypeDefinitionIDs, link.ID())
		}
	}

	items, total, err := i.defs.List(ctx, filter, page)
	if err != nil {
		return nil, err
	}

	out := &DefinitionListOutput{
		Items:    make([]domainrelationship.DefinitionSnapshot, 0, len(items)),
		PageInfo: db.BuildPageInfo(page, len(items), total),
	}
	for _, d := range items {
		out.Items = append(out.Items, d.Snapshot())
	}
	return out, nil
}

// AttributeSets resolves a definition's effective attribute-set chain: its
// own attribute set first, then each inherited ancestor's, base-most last.
// Consumers fetch attributes for every returned type to render the full
// link schema.
func (i *Interactor) AttributeSets(ctx context.Context, rawID string) ([]string, error) {
	id, err := valueobjects.ParseRelationshipDefinitionID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	var sets []string
	seen := make(map[string]bool)
	for depth := 0; depth < maxInheritanceDepth; depth++ {
		def, err := i.defs.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if seen[def.ID().String()] {
			return nil, domainerrors.NewValidation("relationship definition inheritance forms a cycle",
				"definition", def.ID().String())
		}
		seen[def.ID().String()] = true
		sets = append(sets, def.AttributeSetID().String())

		if def.ExtendsID() == nil {
			return sets, nil
		}
		id = *def.ExtendsID()
	}
	return nil, domainerrors.NewValidation("relationship definition inheritance exceeds the supported depth")
}

// LinkInput holds data for linking two entities.
type LinkInput struct {
	DefinitionID  string
	ParentEntity  string
	ChildEntity   string
	ParentVersion *int
	ChildVersion  *int
}

// Link creates a relationship between two entities, enforcing the
// definition's version policies and one-live-link-per-pair.
func (i *Interactor) Link(ctx context.Context, in LinkInput) (*domainrelationship.Snapshot, error) {
	defID, err := valueobjects.ParseRelationshipDefinitionID(in.DefinitionID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	parent, err := valueobjects.ParseEntityID(in.ParentEntity)
	if err != nil {
		return nil, domainerrors.NewValidation("parent: " + err.Error())
	}
	child, err := valueobjects.ParseEntityID(in.ChildEntity)
	if err != nil {
		return nil, domainerrors.NewValidation("child: " + err.Error())
	}

	var snap domainrelationship.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		defs := i.defs.WithTx(tx)
		links := i.links.WithTx(tx)

		// Lock the definition: link validity depends on its policies.
		def, err := defs.GetForUpdate(ctx, defID)
		if err != nil {
			return err
		}

		if def.IsSymmetric() && child.String() < parent.String() {
			// Match the domain's canonical order so the one-live-link
			// guard holds for the unordered pair.
			parent, child = child, parent
		}

		existing, err := links.FindLive(ctx, defID, parent, child)
		if err != nil {
			return err
		}
		if existing != nil {
			return domainerrors.NewConflict("these entities are already linked under this definition",
				"relationship_id", existing.ID().String())
		}

		rel, evts, err := domainrelationship.Link(domainrelationship.LinkInput{
			Definition:    def,
			ParentEntity:  parent,
			ChildEntity:   child,
			ParentVersion: in.ParentVersion,
			ChildVersion:  in.ChildVersion,
		}, i.now())
		if err != nil {
			return err
		}
		if err := links.Save(ctx, rel); err != nil {
			return fmt.Errorf("save relationship: %w", err)
		}

		snap = rel.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domainrelationship.AggregateType,
			EntityID: rel.ID().String(),
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

// Unlink archives a relationship.
func (i *Interactor) Unlink(ctx context.Context, rawID string) (*domainrelationship.Snapshot, error) {
	id, err := valueobjects.ParseRelationshipID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	var snap domainrelationship.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		links := i.links.WithTx(tx)

		rel, err := links.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		before := rel.Snapshot()

		evts, err := rel.Unlink(i.now())
		if err != nil {
			return err
		}
		if err := links.Save(ctx, rel); err != nil {
			return fmt.Errorf("save relationship: %w", err)
		}

		snap = rel.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domainrelationship.AggregateType,
			EntityID: rel.ID().String(),
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

// Get loads one relationship by ID.
func (i *Interactor) Get(ctx context.Context, rawID string) (*domainrelationship.Snapshot, error) {
	id, err := valueobjects.ParseRelationshipID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	rel, err := i.links.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	snap := rel.Snapshot()
	return &snap, nil
}

// EntityLink pairs a link with its definition for inspector rendering.
type EntityLink struct {
	Relationship domainrelationship.Snapshot           `json:"relationship"`
	Definition   domainrelationship.DefinitionSnapshot `json:"definition"`
	// Role is "parent" or "child": which side the queried entity occupies.
	Role string `json:"role"`
}

// ListByEntity loads every live link touching one entity, with definitions
// resolved (definition loads batch through the dataloader).
func (i *Interactor) ListByEntity(ctx context.Context, rawEntityID string) ([]EntityLink, error) {
	entityID, err := valueobjects.ParseEntityID(rawEntityID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	tenant := uow.TenantFromContext(ctx)

	rels, err := i.links.ListByEntity(ctx, domainrelationship.EntityLinksKey{TenantID: tenant, EntityID: entityID})
	if err != nil {
		return nil, err
	}

	out := make([]EntityLink, 0, len(rels))
	for _, rel := range rels {
		def, err := i.defs.Get(ctx, rel.DefinitionID())
		if err != nil {
			return nil, err
		}
		role := "child"
		if rel.ParentEntityID() == entityID {
			role = "parent"
		}
		out = append(out, EntityLink{Relationship: rel.Snapshot(), Definition: def.Snapshot(), Role: role})
	}
	return out, nil
}

// ListInput holds filter and pagination arguments for List.
type ListInput struct {
	DefinitionID    string
	ParentEntityID  string
	ChildEntityID   string
	IncludeArchived bool
	Page            db.PageArgs
}

// ListOutput is one page of relationships.
type ListOutput struct {
	Items    []domainrelationship.Snapshot
	PageInfo db.PageInfo
}

// List returns a filtered, paginated set of relationships.
func (i *Interactor) List(ctx context.Context, in ListInput) (*ListOutput, error) {
	page, err := in.Page.Resolve()
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	filter := domainrelationship.Filter{
		TenantID:        uow.TenantFromContext(ctx),
		IncludeArchived: in.IncludeArchived,
	}
	if in.DefinitionID != "" {
		if filter.DefinitionID, err = valueobjects.ParseRelationshipDefinitionID(in.DefinitionID); err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
	}
	if in.ParentEntityID != "" {
		if filter.ParentEntityID, err = valueobjects.ParseEntityID(in.ParentEntityID); err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
	}
	if in.ChildEntityID != "" {
		if filter.ChildEntityID, err = valueobjects.ParseEntityID(in.ChildEntityID); err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
	}

	items, total, err := i.links.List(ctx, filter, page)
	if err != nil {
		return nil, err
	}

	out := &ListOutput{
		Items:    make([]domainrelationship.Snapshot, 0, len(items)),
		PageInfo: db.BuildPageInfo(page, len(items), total),
	}
	for _, rel := range items {
		out.Items = append(out.Items, rel.Snapshot())
	}
	return out, nil
}
