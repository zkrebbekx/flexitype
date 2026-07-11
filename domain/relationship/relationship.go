package relationship

import (
	"time"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// Relationship is one link between a parent entity and a child entity under
// a relationship definition. Each side either tracks the latest version of
// its endpoint's type definition (nil pin) or pins a specific version.
type Relationship struct {
	id            valueobjects.RelationshipID
	tenantID      valueobjects.TenantID
	definitionID  valueobjects.RelationshipDefinitionID
	parentEntity  valueobjects.EntityID
	childEntity   valueobjects.EntityID
	parentVersion *int
	childVersion  *int
	createdAt     time.Time
	updatedAt     time.Time
	archivedAt    *time.Time
}

// LinkInput carries construction parameters for a Relationship.
type LinkInput struct {
	Definition    *Definition
	ParentEntity  valueobjects.EntityID
	ChildEntity   valueobjects.EntityID
	ParentVersion *int // nil follows the latest parent type version
	ChildVersion  *int // nil follows the latest child type version
}

// Link creates a relationship instance, enforcing the definition's version
// policies: a pinned side must pin, a latest side must not.
func Link(in LinkInput, now time.Time) (*Relationship, []events.Event, error) {
	if in.Definition == nil {
		return nil, nil, domainerrors.NewValidation("relationship definition is required")
	}
	if in.Definition.IsArchived() {
		return nil, nil, domainerrors.NewArchived(DefinitionAggregateType, in.Definition.ID().String())
	}
	if in.ParentEntity.IsZero() || in.ChildEntity.IsZero() {
		return nil, nil, domainerrors.NewValidation("parent and child entity IDs are required")
	}
	if in.ParentEntity == in.ChildEntity && in.Definition.ParentTypeID().Equals(in.Definition.ChildTypeID()) {
		return nil, nil, domainerrors.NewValidation("an entity cannot relate to itself")
	}
	if err := checkPin("parent", in.Definition.ParentVersionPolicy(), in.ParentVersion); err != nil {
		return nil, nil, err
	}
	if err := checkPin("child", in.Definition.ChildVersionPolicy(), in.ChildVersion); err != nil {
		return nil, nil, err
	}

	r := &Relationship{
		id:            valueobjects.NewRelationshipID(),
		tenantID:      in.Definition.TenantID(),
		definitionID:  in.Definition.ID(),
		parentEntity:  in.ParentEntity,
		childEntity:   in.ChildEntity,
		parentVersion: in.ParentVersion,
		childVersion:  in.ChildVersion,
		createdAt:     now,
		updatedAt:     now,
	}
	return r, []events.Event{Linked{
		RelationshipID:           r.id,
		TenantID:                 r.tenantID,
		RelationshipDefinitionID: r.definitionID,
		ParentEntityID:           r.parentEntity,
		ChildEntityID:            r.childEntity,
		ParentVersion:            r.parentVersion,
		ChildVersion:             r.childVersion,
		OccurredAt:               now,
	}}, nil
}

func checkPin(side string, policy VersionPolicy, pin *int) error {
	switch policy {
	case PolicyPinned:
		if pin == nil {
			return domainerrors.NewValidation(side+" side must pin a type version",
				"policy", string(PolicyPinned))
		}
		if *pin < 1 {
			return domainerrors.NewValidation(side + " version pin must be at least 1")
		}
	case PolicyLatest:
		if pin != nil {
			return domainerrors.NewValidation(side+" side follows the latest version and cannot pin",
				"policy", string(PolicyLatest))
		}
	}
	return nil
}

// RePin changes the version pins on a pinned-policy link.
func (r *Relationship) RePin(def *Definition, parentVersion, childVersion *int, now time.Time) ([]events.Event, error) {
	if r.IsArchived() {
		return nil, domainerrors.NewArchived(AggregateType, r.id.String())
	}
	if def == nil || !def.ID().Equals(r.definitionID) {
		return nil, domainerrors.NewValidation("definition does not match the relationship")
	}
	if err := checkPin("parent", def.ParentVersionPolicy(), parentVersion); err != nil {
		return nil, err
	}
	if err := checkPin("child", def.ChildVersionPolicy(), childVersion); err != nil {
		return nil, err
	}
	if intPtrEq(r.parentVersion, parentVersion) && intPtrEq(r.childVersion, childVersion) {
		return nil, nil
	}

	r.parentVersion = parentVersion
	r.childVersion = childVersion
	r.updatedAt = now

	return []events.Event{RePinned{
		RelationshipID:           r.id,
		TenantID:                 r.tenantID,
		RelationshipDefinitionID: r.definitionID,
		ParentVersion:            r.parentVersion,
		ChildVersion:             r.childVersion,
		OccurredAt:               now,
	}}, nil
}

// Unlink archives the relationship (soft delete).
func (r *Relationship) Unlink(now time.Time) ([]events.Event, error) {
	if r.IsArchived() {
		return nil, domainerrors.NewArchived(AggregateType, r.id.String())
	}
	r.archivedAt = &now
	r.updatedAt = now

	return []events.Event{Unlinked{
		RelationshipID:           r.id,
		TenantID:                 r.tenantID,
		RelationshipDefinitionID: r.definitionID,
		ParentEntityID:           r.parentEntity,
		ChildEntityID:            r.childEntity,
		OccurredAt:               now,
	}}, nil
}

func intPtrEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// ID returns the aggregate identifier. It doubles as the entity ID for the
// definition's attribute-set values.
func (r *Relationship) ID() valueobjects.RelationshipID { return r.id }

// TenantID returns the owning tenant.
func (r *Relationship) TenantID() valueobjects.TenantID { return r.tenantID }

// DefinitionID returns the relationship definition.
func (r *Relationship) DefinitionID() valueobjects.RelationshipDefinitionID { return r.definitionID }

// ParentEntityID returns the parent-side entity.
func (r *Relationship) ParentEntityID() valueobjects.EntityID { return r.parentEntity }

// ChildEntityID returns the child-side entity.
func (r *Relationship) ChildEntityID() valueobjects.EntityID { return r.childEntity }

// ParentVersion returns the pinned parent type version, or nil for latest.
func (r *Relationship) ParentVersion() *int { return r.parentVersion }

// ChildVersion returns the pinned child type version, or nil for latest.
func (r *Relationship) ChildVersion() *int { return r.childVersion }

// CreatedAt returns the creation instant.
func (r *Relationship) CreatedAt() time.Time { return r.createdAt }

// UpdatedAt returns the last mutation instant.
func (r *Relationship) UpdatedAt() time.Time { return r.updatedAt }

// ArchivedAt returns the archive instant, if unlinked.
func (r *Relationship) ArchivedAt() *time.Time { return r.archivedAt }

// IsArchived reports whether the relationship was unlinked.
func (r *Relationship) IsArchived() bool { return r.archivedAt != nil }

// Snapshot is the exported projection of a Relationship.
type Snapshot struct {
	ID             valueobjects.RelationshipID           `json:"id"`
	TenantID       valueobjects.TenantID                 `json:"tenant_id"`
	DefinitionID   valueobjects.RelationshipDefinitionID `json:"relationship_definition_id"`
	ParentEntityID valueobjects.EntityID                 `json:"parent_entity_id"`
	ChildEntityID  valueobjects.EntityID                 `json:"child_entity_id"`
	ParentVersion  *int                                  `json:"parent_type_version,omitempty"`
	ChildVersion   *int                                  `json:"child_type_version,omitempty"`
	CreatedAt      time.Time                             `json:"created_at"`
	UpdatedAt      time.Time                             `json:"updated_at"`
	ArchivedAt     *time.Time                            `json:"archived_at,omitempty"`
}

// Snapshot projects the aggregate's current state.
func (r *Relationship) Snapshot() Snapshot {
	return Snapshot{
		ID:             r.id,
		TenantID:       r.tenantID,
		DefinitionID:   r.definitionID,
		ParentEntityID: r.parentEntity,
		ChildEntityID:  r.childEntity,
		ParentVersion:  r.parentVersion,
		ChildVersion:   r.childVersion,
		CreatedAt:      r.createdAt,
		UpdatedAt:      r.updatedAt,
		ArchivedAt:     r.archivedAt,
	}
}

// Rehydrate rebuilds the aggregate from a persisted snapshot. Repository
// use only.
func Rehydrate(s Snapshot) *Relationship {
	return &Relationship{
		id:            s.ID,
		tenantID:      s.TenantID,
		definitionID:  s.DefinitionID,
		parentEntity:  s.ParentEntityID,
		childEntity:   s.ChildEntityID,
		parentVersion: s.ParentVersion,
		childVersion:  s.ChildVersion,
		createdAt:     s.CreatedAt,
		updatedAt:     s.UpdatedAt,
		archivedAt:    s.ArchivedAt,
	}
}
