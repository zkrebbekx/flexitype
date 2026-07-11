package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/application/changeset"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

type changesetStore struct {
	q db.QueryExecer
}

// NewChangeSetStore builds the change-set store over the pool.
func NewChangeSetStore(q db.QueryExecer) changeset.Store {
	return &changesetStore{q: q}
}

type changesetRow struct {
	ID              ulid.ID      `db:"id"`
	TenantID        string       `db:"tenant_id"`
	Name            string       `db:"name"`
	State           string       `db:"state"`
	RequireApproval bool         `db:"require_approval"`
	Author          string       `db:"author"`
	Approver        string       `db:"approver"`
	Mutations       []byte       `db:"mutations"`
	PublishAt       sql.NullTime `db:"publish_at"`
	CreatedAt       time.Time    `db:"created_at"`
	UpdatedAt       time.Time    `db:"updated_at"`
	PublishedAt     sql.NullTime `db:"published_at"`
}

func (r changesetRow) toChangeSet() (changeset.ChangeSet, error) {
	muts := []appvalue.Mutation{}
	if len(r.Mutations) > 0 {
		if err := json.Unmarshal(r.Mutations, &muts); err != nil {
			return changeset.ChangeSet{}, fmt.Errorf("decode mutations for %s: %w", r.ID, err)
		}
	}
	return changeset.ChangeSet{
		ID:              r.ID,
		TenantID:        valueobjects.TenantID(r.TenantID),
		Name:            r.Name,
		State:           changeset.State(r.State),
		RequireApproval: r.RequireApproval,
		Author:          r.Author,
		Approver:        r.Approver,
		Mutations:       muts,
		PublishAt:       timePtr(r.PublishAt),
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
		PublishedAt:     timePtr(r.PublishedAt),
	}, nil
}

const changesetColumns = `id, tenant_id, name, state, require_approval, author, approver,
	mutations, publish_at, created_at, updated_at, published_at`

func (s *changesetStore) upsert(ctx context.Context, cs changeset.ChangeSet) error {
	muts, _ := json.Marshal(cs.Mutations)
	_, err := s.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_changeset (`+changesetColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET
		   name = EXCLUDED.name, state = EXCLUDED.state,
		   require_approval = EXCLUDED.require_approval, approver = EXCLUDED.approver,
		   mutations = EXCLUDED.mutations, publish_at = EXCLUDED.publish_at,
		   updated_at = EXCLUDED.updated_at, published_at = EXCLUDED.published_at`),
		cs.ID, cs.TenantID.String(), cs.Name, string(cs.State), cs.RequireApproval, cs.Author, cs.Approver,
		jsonbParam(muts), nullableTime(cs.PublishAt), cs.CreatedAt, cs.UpdatedAt, nullableTime(cs.PublishedAt))
	if err != nil {
		return fmt.Errorf("save change-set: %w", err)
	}
	return nil
}

func (s *changesetStore) Create(ctx context.Context, cs changeset.ChangeSet) error {
	return s.upsert(ctx, cs)
}

func (s *changesetStore) Update(ctx context.Context, cs changeset.ChangeSet) error {
	return s.upsert(ctx, cs)
}

func (s *changesetStore) Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (changeset.ChangeSet, error) {
	var row changesetRow
	err := s.q.GetContext(ctx, &row, bind(
		`SELECT `+changesetColumns+` FROM flexitype_changeset WHERE id = ? AND tenant_id = ?`), id, tenant.String())
	if isNoRows(err) {
		return changeset.ChangeSet{}, domainerrors.NewNotFound("changeset", id.String())
	}
	if err != nil {
		return changeset.ChangeSet{}, fmt.Errorf("get change-set: %w", err)
	}
	return row.toChangeSet()
}

func (s *changesetStore) List(ctx context.Context, tenant valueobjects.TenantID) ([]changeset.ChangeSet, error) {
	var rows []changesetRow
	if err := s.q.SelectContext(ctx, &rows, bind(
		`SELECT `+changesetColumns+` FROM flexitype_changeset WHERE tenant_id = ? ORDER BY created_at DESC`), tenant.String()); err != nil {
		return nil, fmt.Errorf("list change-sets: %w", err)
	}
	return toChangeSets(rows)
}

func (s *changesetStore) DueForPublish(ctx context.Context, now time.Time) ([]changeset.ChangeSet, error) {
	var rows []changesetRow
	if err := s.q.SelectContext(ctx, &rows, bind(
		`SELECT `+changesetColumns+` FROM flexitype_changeset
		 WHERE state = 'approved' AND publish_at IS NOT NULL AND publish_at <= ?`), now); err != nil {
		return nil, fmt.Errorf("list due change-sets: %w", err)
	}
	return toChangeSets(rows)
}

func toChangeSets(rows []changesetRow) ([]changeset.ChangeSet, error) {
	out := make([]changeset.ChangeSet, 0, len(rows))
	for _, r := range rows {
		cs, err := r.toChangeSet()
		if err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, nil
}
