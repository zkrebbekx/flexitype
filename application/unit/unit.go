// Package unit holds tenant unit families for quantity attributes: a family
// (mass, length, …) names a base unit and each member unit's conversion
// factor to it. Quantity values normalize to the base for comparison; the
// original unit is preserved for display.
package unit

import (
	"context"
	"math/big"
	"strconv"

	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// Family is a set of units sharing a base, with per-unit conversion factors.
type Family struct {
	ID       ulid.ID               `json:"id"`
	TenantID valueobjects.TenantID `json:"tenant_id"`
	Name     string                `json:"name"`
	// BaseUnit is the unit factors convert to (factor 1).
	BaseUnit string `json:"base_unit"`
	// Units maps each unit symbol to its factor: value_in_base = magnitude *
	// factor. The base unit's factor is 1.
	Units map[string]float64 `json:"units"`
}

// Factor returns the conversion factor for a unit, or ok=false when the unit
// is not a member of the family (a cross-family value).
func (f Family) Factor(unit string) (float64, bool) {
	factor, ok := f.Units[unit]
	return factor, ok
}

// ToBase converts a magnitude string in one of the family's units to the
// base-unit magnitude. It rejects units outside the family.
func (f Family) ToBase(magnitude, unit string) (float64, error) {
	m, ok := new(big.Rat).SetString(magnitude)
	if !ok {
		return 0, domainerrors.NewValidation("quantity magnitude must be numeric", "magnitude", magnitude)
	}
	factor, ok := f.Factor(unit)
	if !ok {
		return 0, domainerrors.NewValidation("unit is not part of the attribute's unit family",
			"unit", unit, "family", f.Name)
	}
	// Multiply as exact rationals so equal quantities expressed in different
	// units fold to the SAME float64 base (e.g. 997.9024 g and 2.2 lb), rather
	// than differing in the last ~1e-13 from float multiplication. The factor's
	// shortest round-trip string recovers the intended decimal (453.592, not
	// its binary approximation).
	fr, _ := new(big.Rat).SetString(strconv.FormatFloat(factor, 'f', -1, 64))
	base, _ := new(big.Rat).Mul(m, fr).Float64()
	return base, nil
}

// Store persists unit families, scoped by tenant.
type Store interface {
	Create(ctx context.Context, f Family) error
	Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (Family, error)
	List(ctx context.Context, tenant valueobjects.TenantID) ([]Family, error)
	Delete(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) error
}

// Interactor implements the unit-family usecases.
type Interactor struct {
	store Store
}

// NewInteractor wires the unit-family usecases.
func NewInteractor(store Store) *Interactor { return &Interactor{store: store} }

// CreateInput carries a new family's fields.
type CreateInput struct {
	Name     string
	BaseUnit string
	Units    map[string]float64
}

// Create validates and stores a unit family. The base unit must be present
// with factor 1.
func (i *Interactor) Create(ctx context.Context, in CreateInput) (*Family, error) {
	if in.Name == "" || in.BaseUnit == "" {
		return nil, domainerrors.NewValidation("unit family requires a name and base unit")
	}
	if len(in.Units) == 0 {
		return nil, domainerrors.NewValidation("unit family requires at least one unit")
	}
	if f, ok := in.Units[in.BaseUnit]; !ok || f != 1 {
		return nil, domainerrors.NewValidation("the base unit must be present with factor 1", "base_unit", in.BaseUnit)
	}
	for u, factor := range in.Units {
		if factor <= 0 {
			return nil, domainerrors.NewValidation("unit factors must be positive", "unit", u)
		}
	}
	f := Family{
		ID:       ulid.New(),
		TenantID: uow.TenantFromContext(ctx),
		Name:     in.Name,
		BaseUnit: in.BaseUnit,
		Units:    in.Units,
	}
	if err := i.store.Create(ctx, f); err != nil {
		return nil, err
	}
	return &f, nil
}

// Get returns one unit family.
func (i *Interactor) Get(ctx context.Context, rawID string) (*Family, error) {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	f, err := i.store.Get(ctx, uow.TenantFromContext(ctx), id)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// List returns the tenant's unit families.
func (i *Interactor) List(ctx context.Context) ([]Family, error) {
	return i.store.List(ctx, uow.TenantFromContext(ctx))
}

// Delete removes a unit family.
func (i *Interactor) Delete(ctx context.Context, rawID string) error {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	// Confirm it exists (within this tenant) first, so deleting something that
	// is not there reports NotFound like every other by-id operation, rather
	// than a misleading success. The lookup is tenant-scoped, so another
	// tenant's family is indistinguishable from a missing one.
	if _, err := i.store.Get(ctx, uow.TenantFromContext(ctx), id); err != nil {
		return err
	}
	return i.store.Delete(ctx, uow.TenantFromContext(ctx), id)
}
