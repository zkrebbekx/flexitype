package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/application/dedup"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

type matchStore struct {
	q db.QueryExecer
}

// NewMatchStore builds the duplicate-detection store over the pool.
func NewMatchStore(q db.QueryExecer) dedup.Store {
	return &matchStore{q: q}
}

type matchRuleRow struct {
	ID                    ulid.ID   `db:"id"`
	TenantID              string    `db:"tenant_id"`
	TypeDefinitionID      string    `db:"type_definition_id"`
	AttributeDefinitionID string    `db:"attribute_definition_id"`
	Strategy              string    `db:"strategy"`
	Threshold             float64   `db:"threshold"`
	CreatedAt             time.Time `db:"created_at"`
}

func (r matchRuleRow) toRule() dedup.Rule {
	return dedup.Rule{
		ID:                    r.ID,
		TenantID:              valueobjects.TenantID(r.TenantID),
		TypeDefinitionID:      r.TypeDefinitionID,
		AttributeDefinitionID: r.AttributeDefinitionID,
		Strategy:              dedup.Strategy(r.Strategy),
		Threshold:             r.Threshold,
		CreatedAt:             r.CreatedAt,
	}
}

func (s *matchStore) CreateRule(ctx context.Context, r dedup.Rule) error {
	_, err := s.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_match_rule
		   (id, tenant_id, type_definition_id, attribute_definition_id, strategy, threshold, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`),
		r.ID, r.TenantID.String(), r.TypeDefinitionID, r.AttributeDefinitionID, string(r.Strategy), r.Threshold, r.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert match rule: %w", err)
	}
	return nil
}

func (s *matchStore) GetRule(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (dedup.Rule, error) {
	var row matchRuleRow
	err := s.q.GetContext(ctx, &row, bind(
		`SELECT id, tenant_id, type_definition_id, attribute_definition_id, strategy, threshold, created_at
		 FROM flexitype_match_rule WHERE id = ? AND tenant_id = ?`), id, tenant.String())
	if isNoRows(err) {
		return dedup.Rule{}, domainerrors.NewNotFound("match_rule", id.String())
	}
	if err != nil {
		return dedup.Rule{}, fmt.Errorf("get match rule: %w", err)
	}
	return row.toRule(), nil
}

func (s *matchStore) ListRules(ctx context.Context, tenant valueobjects.TenantID, typeDefID string) ([]dedup.Rule, error) {
	var rows []matchRuleRow
	if err := s.q.SelectContext(ctx, &rows, bind(
		`SELECT id, tenant_id, type_definition_id, attribute_definition_id, strategy, threshold, created_at
		 FROM flexitype_match_rule WHERE tenant_id = ? AND type_definition_id = ? ORDER BY created_at`),
		tenant.String(), typeDefID); err != nil {
		return nil, fmt.Errorf("list match rules: %w", err)
	}
	out := make([]dedup.Rule, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toRule())
	}
	return out, nil
}

func (s *matchStore) DeleteRule(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) error {
	if _, err := s.q.ExecContext(ctx, bind(
		`DELETE FROM flexitype_match_rule WHERE id = ? AND tenant_id = ?`), id, tenant.String()); err != nil {
		return fmt.Errorf("delete match rule: %w", err)
	}
	// Dismissals cascade via ON DELETE CASCADE in the schema.
	return nil
}

func (s *matchStore) Dismiss(ctx context.Context, d dedup.Dismissal) error {
	_, err := s.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_match_dismissal (rule_id, tenant_id, entity_a, entity_b)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (rule_id, entity_a, entity_b) DO NOTHING`),
		d.RuleID, d.TenantID.String(), d.EntityA, d.EntityB)
	if err != nil {
		return fmt.Errorf("insert match dismissal: %w", err)
	}
	return nil
}

func (s *matchStore) ListDismissals(ctx context.Context, tenant valueobjects.TenantID, ruleID ulid.ID) ([]dedup.Dismissal, error) {
	type row struct {
		RuleID   ulid.ID `db:"rule_id"`
		TenantID string  `db:"tenant_id"`
		EntityA  string  `db:"entity_a"`
		EntityB  string  `db:"entity_b"`
	}
	var rows []row
	if err := s.q.SelectContext(ctx, &rows, bind(
		`SELECT rule_id, tenant_id, entity_a, entity_b
		 FROM flexitype_match_dismissal WHERE rule_id = ? AND tenant_id = ?`), ruleID, tenant.String()); err != nil {
		return nil, fmt.Errorf("list match dismissals: %w", err)
	}
	out := make([]dedup.Dismissal, 0, len(rows))
	for _, r := range rows {
		out = append(out, dedup.Dismissal{
			RuleID:   r.RuleID,
			TenantID: valueobjects.TenantID(r.TenantID),
			EntityA:  r.EntityA,
			EntityB:  r.EntityB,
		})
	}
	return out, nil
}
