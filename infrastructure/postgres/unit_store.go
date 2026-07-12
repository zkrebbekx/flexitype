package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zkrebbekx/flexitype/application/unit"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

type unitStore struct {
	q db.QueryExecer
}

// NewUnitFamilyStore builds the unit-family store over the pool.
func NewUnitFamilyStore(q db.QueryExecer) unit.Store {
	return &unitStore{q: q}
}

type unitFamilyRow struct {
	ID       ulid.ID `db:"id"`
	TenantID string  `db:"tenant_id"`
	Name     string  `db:"name"`
	BaseUnit string  `db:"base_unit"`
	Units    []byte  `db:"units"`
}

func (r unitFamilyRow) toFamily() (unit.Family, error) {
	units := map[string]float64{}
	if len(r.Units) > 0 {
		if err := json.Unmarshal(r.Units, &units); err != nil {
			return unit.Family{}, fmt.Errorf("decode units for %s: %w", r.ID, err)
		}
	}
	return unit.Family{
		ID:       r.ID,
		TenantID: valueobjects.TenantID(r.TenantID),
		Name:     r.Name,
		BaseUnit: r.BaseUnit,
		Units:    units,
	}, nil
}

func (s *unitStore) Create(ctx context.Context, f unit.Family) error {
	units, _ := json.Marshal(f.Units)
	_, err := s.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_unit_family (id, tenant_id, name, base_unit, units)
		 VALUES (?, ?, ?, ?, ?)`),
		f.ID, f.TenantID.String(), f.Name, f.BaseUnit, jsonbParam(units))
	if err != nil {
		return fmt.Errorf("insert unit family: %w", err)
	}
	return nil
}

func (s *unitStore) Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (unit.Family, error) {
	var row unitFamilyRow
	err := s.q.GetContext(ctx, &row, bind(
		`SELECT id, tenant_id, name, base_unit, units FROM flexitype_unit_family WHERE id = ? AND tenant_id = ?`),
		id, tenant.String())
	if isNoRows(err) {
		return unit.Family{}, domainerrors.NewNotFound("unit_family", id.String())
	}
	if err != nil {
		return unit.Family{}, fmt.Errorf("get unit family: %w", err)
	}
	return row.toFamily()
}

func (s *unitStore) List(ctx context.Context, tenant valueobjects.TenantID) ([]unit.Family, error) {
	var rows []unitFamilyRow
	if err := s.q.SelectContext(ctx, &rows, bind(
		`SELECT id, tenant_id, name, base_unit, units FROM flexitype_unit_family WHERE tenant_id = ? ORDER BY name`),
		tenant.String()); err != nil {
		return nil, fmt.Errorf("list unit families: %w", err)
	}
	out := make([]unit.Family, 0, len(rows))
	for _, r := range rows {
		f, err := r.toFamily()
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

func (s *unitStore) Delete(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) error {
	if _, err := s.q.ExecContext(ctx, bind(
		`DELETE FROM flexitype_unit_family WHERE id = ? AND tenant_id = ?`), id, tenant.String()); err != nil {
		return fmt.Errorf("delete unit family: %w", err)
	}
	return nil
}
