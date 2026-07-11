package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/application/savedview"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

type savedViewStore struct {
	q db.QueryExecer
}

// NewSavedViewStore builds the saved-view store over the pool.
func NewSavedViewStore(q db.QueryExecer) savedview.Store {
	return &savedViewStore{q: q}
}

type savedViewRow struct {
	ID        ulid.ID   `db:"id"`
	TenantID  string    `db:"tenant_id"`
	Name      string    `db:"name"`
	RootType  string    `db:"root_type"`
	Query     string    `db:"query"`
	Columns   []byte    `db:"columns"`
	Sort      string    `db:"sort"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func (r savedViewRow) toView() (savedview.View, error) {
	cols := []string{}
	if len(r.Columns) > 0 {
		if err := json.Unmarshal(r.Columns, &cols); err != nil {
			return savedview.View{}, fmt.Errorf("decode columns for %s: %w", r.ID, err)
		}
	}
	return savedview.View{
		ID:        r.ID,
		TenantID:  valueobjects.TenantID(r.TenantID),
		Name:      r.Name,
		RootType:  r.RootType,
		Query:     r.Query,
		Columns:   cols,
		Sort:      r.Sort,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}, nil
}

func (s *savedViewStore) Create(ctx context.Context, v savedview.View) error {
	cols, _ := json.Marshal(v.Columns)
	_, err := s.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_saved_view (id, tenant_id, name, root_type, query, columns, sort, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		v.ID, v.TenantID.String(), v.Name, v.RootType, v.Query, jsonbParam(cols), v.Sort, v.CreatedAt, v.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert saved view: %w", err)
	}
	return nil
}

func (s *savedViewStore) Update(ctx context.Context, v savedview.View) error {
	cols, _ := json.Marshal(v.Columns)
	_, err := s.q.ExecContext(ctx, bind(
		`UPDATE flexitype_saved_view SET name = ?, root_type = ?, query = ?, columns = ?, sort = ?, updated_at = ?
		 WHERE id = ? AND tenant_id = ?`),
		v.Name, v.RootType, v.Query, jsonbParam(cols), v.Sort, v.UpdatedAt, v.ID, v.TenantID.String())
	if err != nil {
		return fmt.Errorf("update saved view: %w", err)
	}
	return nil
}

func (s *savedViewStore) Delete(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) error {
	_, err := s.q.ExecContext(ctx, bind(
		`DELETE FROM flexitype_saved_view WHERE id = ? AND tenant_id = ?`), id, tenant.String())
	if err != nil {
		return fmt.Errorf("delete saved view: %w", err)
	}
	return nil
}

func (s *savedViewStore) Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (savedview.View, error) {
	var row savedViewRow
	err := s.q.GetContext(ctx, &row, bind(
		`SELECT id, tenant_id, name, root_type, query, columns, sort, created_at, updated_at
		 FROM flexitype_saved_view WHERE id = ? AND tenant_id = ?`), id, tenant.String())
	if isNoRows(err) {
		return savedview.View{}, domainerrors.NewNotFound("saved_view", id.String())
	}
	if err != nil {
		return savedview.View{}, fmt.Errorf("get saved view: %w", err)
	}
	return row.toView()
}

func (s *savedViewStore) List(ctx context.Context, tenant valueobjects.TenantID) ([]savedview.View, error) {
	var rows []savedViewRow
	if err := s.q.SelectContext(ctx, &rows, bind(
		`SELECT id, tenant_id, name, root_type, query, columns, sort, created_at, updated_at
		 FROM flexitype_saved_view WHERE tenant_id = ? ORDER BY name`), tenant.String()); err != nil {
		return nil, fmt.Errorf("list saved views: %w", err)
	}
	out := make([]savedview.View, 0, len(rows))
	for _, r := range rows {
		v, err := r.toView()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
