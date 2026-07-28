// Package dependency holds the AttributeValueDependency aggregate: when a
// source attribute's value matches all conditions, an effect applies to a
// target attribute — narrowing its allowed values, adding constraints or
// overriding whether it is required. This powers cascading picklists and
// conditional validation.
package dependency

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// Effect is what a matched dependency does to its target attribute.
type Effect struct {
	// AllowedValues narrows the target to this set when non-empty.
	AllowedValues []valueobjects.Value
	// Constraints are additional rules applied to the target's values.
	Constraints attribute.Constraints
	// Required overrides the target's required flag when non-nil.
	Required *bool
}

type effectJSON struct {
	AllowedValues []json.RawMessage     `json:"allowed_values,omitempty"`
	Constraints   attribute.Constraints `json:"constraints,omitempty"`
	Required      *bool                 `json:"required,omitempty"`
}

// MarshalJSON encodes allowed values in their self-describing typed form so
// the effect survives storage round-trips.
func (e Effect) MarshalJSON() ([]byte, error) {
	out := effectJSON{Constraints: e.Constraints, Required: e.Required}
	for _, v := range e.AllowedValues {
		typed, err := v.MarshalTyped()
		if err != nil {
			return nil, err
		}
		out.AllowedValues = append(out.AllowedValues, typed)
	}
	return json.Marshal(out)
}

// UnmarshalJSON is the inverse of MarshalJSON.
func (e *Effect) UnmarshalJSON(b []byte) error {
	var in effectJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return err
	}
	e.Constraints = in.Constraints
	e.Required = in.Required
	e.AllowedValues = nil
	for _, raw := range in.AllowedValues {
		v, err := valueobjects.UnmarshalTypedValue(raw)
		if err != nil {
			return fmt.Errorf("allowed value: %w", err)
		}
		e.AllowedValues = append(e.AllowedValues, v)
	}
	return nil
}

// isEmpty reports whether the effect changes nothing.
func (e Effect) isEmpty() bool {
	return len(e.AllowedValues) == 0 && len(e.Constraints) == 0 && e.Required == nil
}

// Dependency is the aggregate root for one source→target rule.
type Dependency struct {
	id          valueobjects.DependencyID
	tenantID    valueobjects.TenantID
	sourceID    valueobjects.AttributeDefinitionID
	targetID    valueobjects.AttributeDefinitionID
	conditions  []Condition
	effect      Effect
	description string
	version     int
	createdAt   time.Time
	updatedAt   time.Time
	archivedAt  *time.Time
}

// NewInput carries construction parameters for a Dependency.
type NewInput struct {
	TenantID    valueobjects.TenantID
	Source      *attribute.Definition
	Target      *attribute.Definition
	Conditions  []Condition
	Effect      Effect
	Description string
}

// New creates a Dependency, validating conditions against the source
// attribute and the effect against the target attribute.
func New(in NewInput, now time.Time) (*Dependency, []events.Event, error) {
	if in.TenantID.IsZero() {
		in.TenantID = valueobjects.DefaultTenant
	}
	if in.Source == nil || in.Target == nil {
		return nil, nil, domainerrors.NewValidation("source and target attributes are required")
	}
	if in.Source.ID().Equals(in.Target.ID()) {
		return nil, nil, domainerrors.NewValidation("source and target attributes must differ")
	}
	if err := validateRule(in.Source, in.Target, in.Conditions, in.Effect); err != nil {
		return nil, nil, err
	}

	d := &Dependency{
		id:          valueobjects.NewDependencyID(),
		tenantID:    in.TenantID,
		sourceID:    in.Source.ID(),
		targetID:    in.Target.ID(),
		conditions:  in.Conditions,
		effect:      in.Effect,
		description: in.Description,
		version:     1,
		createdAt:   now,
		updatedAt:   now,
	}
	return d, []events.Event{Created{
		DependencyID:      d.id,
		TenantID:          d.tenantID,
		SourceAttributeID: d.sourceID,
		TargetAttributeID: d.targetID,
		OccurredAt:        now,
	}}, nil
}

// UpdateInput carries the mutable fields of a Dependency.
type UpdateInput struct {
	Conditions  []Condition
	Effect      Effect
	Description string
}

// Update replaces conditions and effect, bumping the version. Source and
// target are immutable — create a new dependency to rewire.
func (d *Dependency) Update(source, target *attribute.Definition, in UpdateInput, now time.Time) ([]events.Event, error) {
	if d.IsArchived() {
		return nil, domainerrors.NewArchived(AggregateType, d.id.String())
	}
	if source == nil || !source.ID().Equals(d.sourceID) || target == nil || !target.ID().Equals(d.targetID) {
		return nil, domainerrors.NewValidation("source and target must match the dependency's attributes")
	}
	if err := validateRule(source, target, in.Conditions, in.Effect); err != nil {
		return nil, err
	}

	d.conditions = in.Conditions
	d.effect = in.Effect
	d.description = in.Description
	d.version++
	d.updatedAt = now

	return []events.Event{Updated{
		DependencyID:      d.id,
		TenantID:          d.tenantID,
		SourceAttributeID: d.sourceID,
		TargetAttributeID: d.targetID,
		Version:           d.version,
		OccurredAt:        now,
	}}, nil
}

// Archive soft-deletes the dependency.
func (d *Dependency) Archive(now time.Time) ([]events.Event, error) {
	if d.IsArchived() {
		return nil, domainerrors.NewArchived(AggregateType, d.id.String())
	}
	d.archivedAt = &now
	d.updatedAt = now

	return []events.Event{Archived{
		DependencyID:      d.id,
		TenantID:          d.tenantID,
		SourceAttributeID: d.sourceID,
		TargetAttributeID: d.targetID,
		OccurredAt:        now,
	}}, nil
}

// MatchesAny reports whether ANY of an entity's values for the source
// attribute satisfies every condition.
//
// A multi-valued source used to collapse to one arbitrary member, and a
// localizable or scopable one to whichever variant was written last, because
// the caller built a map of one value per attribute. So a rule such as "if
// certifications contains asbestos then require disposal_plan" evaluated only
// the newest certification: adding a second certification stopped the rule
// matching the first, with no schema or data change to explain it.
//
// Any-match is the conventional reading of a condition over a set, and it is
// the safe direction here: a rule that fired on a member keeps firing when
// another member is added.
//
// An attribute with no values is evaluated once against the zero value, so
// conditions that test absence behave as before.
func (d *Dependency) MatchesAny(sources []valueobjects.Value, now time.Time) (bool, error) {
	return d.MatchesAnyWithContext(sources, nil, now)
}

// MatchesAnyWithContext is MatchesAny with the caller's own facts available
// to conditions that name one.
//
// An embedder's entities keep their primary fields in host tables — a
// customer's tier, an order's channel — and no condition could reference
// them, so a rule that depended on one had to be expressed by copying the
// field into flexitype and keeping the copy in step. ctxValues carries those
// facts for this evaluation only; nothing is stored.
func (d *Dependency) MatchesAnyWithContext(sources []valueobjects.Value, ctxValues map[string]valueobjects.Value, now time.Time) (bool, error) {
	if len(sources) == 0 {
		return d.matches(valueobjects.Value{}, ctxValues, now)
	}
	for _, v := range sources {
		ok, err := d.matches(v, ctxValues, now)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// Matches reports whether one source value satisfies every condition.
func (d *Dependency) Matches(source valueobjects.Value, now time.Time) (bool, error) {
	return d.matches(source, nil, now)
}

func (d *Dependency) matches(source valueobjects.Value, ctxValues map[string]valueobjects.Value, now time.Time) (bool, error) {
	for _, c := range d.conditions {
		// A condition naming a context key tests the caller's fact instead
		// of the rule's source attribute. An absent key does not match: a
		// rule must not fire on a fact the caller did not supply, because
		// "not supplied" and "supplied as the zero value" mean different
		// things and only the caller knows which happened.
		subject := source
		if c.ContextKey != "" {
			v, ok := ctxValues[c.ContextKey]
			if !ok {
				return false, nil
			}
			subject = v
		}
		ok, err := c.Matches(subject, now)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func validateRule(source, target *attribute.Definition, conditions []Condition, effect Effect) error {
	if len(conditions) == 0 {
		return domainerrors.NewValidation("at least one condition is required")
	}
	for _, c := range conditions {
		if err := c.Validate(source.DataType()); err != nil {
			return err
		}
	}
	if effect.isEmpty() {
		return domainerrors.NewValidation("effect must narrow values, add constraints or override required")
	}
	for _, v := range effect.AllowedValues {
		if v.DataType() != target.DataType() {
			return domainerrors.NewValidation("allowed value type must match the target attribute type",
				"value_type", v.DataType().String(), "target_type", target.DataType().String())
		}
	}
	if err := effect.Constraints.Validate(target.DataType()); err != nil {
		return err
	}
	return nil
}

// ID returns the aggregate identifier.
func (d *Dependency) ID() valueobjects.DependencyID { return d.id }

// TenantID returns the owning tenant.
func (d *Dependency) TenantID() valueobjects.TenantID { return d.tenantID }

// SourceAttributeID returns the condition-side attribute.
func (d *Dependency) SourceAttributeID() valueobjects.AttributeDefinitionID { return d.sourceID }

// TargetAttributeID returns the effect-side attribute.
func (d *Dependency) TargetAttributeID() valueobjects.AttributeDefinitionID { return d.targetID }

// Conditions returns the predicates.
func (d *Dependency) Conditions() []Condition { return d.conditions }

// Effect returns the applied effect.
func (d *Dependency) Effect() Effect { return d.effect }

// Description returns the human description.
func (d *Dependency) Description() string { return d.description }

// Version returns the optimistic version counter.
func (d *Dependency) Version() int { return d.version }

// CreatedAt returns the creation instant.
func (d *Dependency) CreatedAt() time.Time { return d.createdAt }

// UpdatedAt returns the last mutation instant.
func (d *Dependency) UpdatedAt() time.Time { return d.updatedAt }

// ArchivedAt returns the archive instant, if archived.
func (d *Dependency) ArchivedAt() *time.Time { return d.archivedAt }

// IsArchived reports whether the dependency is archived.
func (d *Dependency) IsArchived() bool { return d.archivedAt != nil }

// Snapshot is the exported projection of a Dependency.
type Snapshot struct {
	ID                valueobjects.DependencyID          `json:"id"`
	TenantID          valueobjects.TenantID              `json:"tenant_id"`
	SourceAttributeID valueobjects.AttributeDefinitionID `json:"source_attribute_id"`
	TargetAttributeID valueobjects.AttributeDefinitionID `json:"target_attribute_id"`
	Conditions        []Condition                        `json:"conditions"`
	Effect            Effect                             `json:"effect"`
	Description       string                             `json:"description,omitempty"`
	Version           int                                `json:"version"`
	CreatedAt         time.Time                          `json:"created_at"`
	UpdatedAt         time.Time                          `json:"updated_at"`
	ArchivedAt        *time.Time                         `json:"archived_at,omitempty"`
}

// Snapshot projects the aggregate's current state.
func (d *Dependency) Snapshot() Snapshot {
	return Snapshot{
		ID:                d.id,
		TenantID:          d.tenantID,
		SourceAttributeID: d.sourceID,
		TargetAttributeID: d.targetID,
		Conditions:        d.conditions,
		Effect:            d.effect,
		Description:       d.description,
		Version:           d.version,
		CreatedAt:         d.createdAt,
		UpdatedAt:         d.updatedAt,
		ArchivedAt:        d.archivedAt,
	}
}

// Rehydrate rebuilds the aggregate from a persisted snapshot. Repository
// use only.
func Rehydrate(s Snapshot) *Dependency {
	return &Dependency{
		id:          s.ID,
		tenantID:    s.TenantID,
		sourceID:    s.SourceAttributeID,
		targetID:    s.TargetAttributeID,
		conditions:  s.Conditions,
		effect:      s.Effect,
		description: s.Description,
		version:     s.Version,
		createdAt:   s.CreatedAt,
		updatedAt:   s.UpdatedAt,
		archivedAt:  s.ArchivedAt,
	}
}
