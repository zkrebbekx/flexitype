package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/zkrebbekx/flexitype/application/query"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/fql"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// queryRepository compiles bound FQL trees into one SQL statement: each
// condition is an EXISTS (or aggregate) subquery over the value table,
// traversals correlate through the relationship table, and the tree
// composes with AND/OR/NOT. Only resolved ULIDs and bound arguments reach
// SQL — never user text.
type queryRepository struct {
	q db.QueryExecer
}

// NewQueryRepository builds the FQL execution adapter.
func NewQueryRepository(q db.QueryExecer) query.Repository {
	return &queryRepository{q: q}
}

// entityRef carries the correlated columns identifying "the current
// entity" at one compilation scope.
type entityRef struct {
	tenant string // SQL expression for the tenant column
	entity string // SQL expression for the entity id column
	typeID string // SQL expression for the declared type column
	link   string // SQL expression for the enclosing relationship id ("" at root)
}

// compiler accumulates arguments and generates unique aliases.
type compiler struct {
	args    []any
	aliasNo int
}

func (c *compiler) alias(prefix string) string {
	c.aliasNo++
	return fmt.Sprintf("%s%d", prefix, c.aliasNo)
}

func (c *compiler) arg(v any) string {
	c.args = append(c.args, v)
	return "?"
}

func (r *queryRepository) Search(ctx context.Context, tenant valueobjects.TenantID, rootTypeIDs []valueobjects.TypeDefinitionID, node query.BoundNode, page db.Page) ([]domainvalue.EntitySummary, int, error) {
	c := &compiler{}

	rootIDs := make([]string, 0, len(rootTypeIDs))
	for _, id := range rootTypeIDs {
		rootIDs = append(rootIDs, id.String())
	}

	base := fmt.Sprintf(`SELECT tenant_id, type_definition_id, entity_id,
	       count(*) AS value_count, max(updated_at) AS last_updated_at
	 FROM flexitype_attribute_value
	 WHERE tenant_id = %s AND type_definition_id = ANY(%s) AND archived_at IS NULL
	 GROUP BY tenant_id, type_definition_id, entity_id`,
		c.arg(tenant.String()), c.arg(pq.Array(rootIDs)))

	where, err := r.compile(c, node, entityRef{
		tenant: "e.tenant_id",
		entity: "e.entity_id",
		typeID: "e.type_definition_id",
	})
	if err != nil {
		return nil, 0, err
	}

	sql := fmt.Sprintf(`SELECT e.entity_id, e.type_definition_id, e.value_count, e.last_updated_at,
	       count(*) OVER () AS total_count
	 FROM (%s) e
	 WHERE %s
	 ORDER BY e.last_updated_at DESC, e.entity_id
	 LIMIT %s OFFSET %s`,
		base, where, c.arg(page.Limit), c.arg(page.Offset))

	var rows []struct {
		EntityID         string    `db:"entity_id"`
		TypeDefinitionID ulid.ID   `db:"type_definition_id"`
		ValueCount       int       `db:"value_count"`
		LastUpdatedAt    time.Time `db:"last_updated_at"`
		TotalCount       int       `db:"total_count"`
	}
	if err := r.q.SelectContext(ctx, &rows, bind(sql), c.args...); err != nil {
		return nil, 0, fmt.Errorf("execute query: %w", err)
	}

	out := make([]domainvalue.EntitySummary, 0, len(rows))
	total := 0
	for _, row := range rows {
		out = append(out, domainvalue.EntitySummary{
			EntityID:         valueobjects.EntityID(row.EntityID),
			TypeDefinitionID: valueobjects.TypeDefinitionID{ID: row.TypeDefinitionID},
			ValueCount:       row.ValueCount,
			LastUpdatedAt:    row.LastUpdatedAt,
		})
		total = row.TotalCount
	}
	return out, total, nil
}

func (r *queryRepository) compile(c *compiler, node query.BoundNode, e entityRef) (string, error) {
	switch n := node.(type) {
	case *query.BoundLogical:
		parts := make([]string, 0, len(n.Exprs))
		for _, expr := range n.Exprs {
			part, err := r.compile(c, expr, e)
			if err != nil {
				return "", err
			}
			parts = append(parts, part)
		}
		joiner := " AND "
		if n.Op == fql.OpOr {
			joiner = " OR "
		}
		return "(" + strings.Join(parts, joiner) + ")", nil

	case *query.BoundNot:
		inner, err := r.compile(c, n.Expr, e)
		if err != nil {
			return "", err
		}
		return "NOT " + inner, nil

	case *query.BoundType:
		ids := make([]string, 0, len(n.TypeIDs))
		for _, id := range n.TypeIDs {
			ids = append(ids, id.String())
		}
		expr := fmt.Sprintf("%s = ANY(%s)", e.typeID, c.arg(pq.Array(ids)))
		if n.Negate {
			expr = "NOT " + expr
		}
		return expr, nil

	case *query.BoundCompare:
		return r.compileCompare(c, n, e)

	case *query.BoundIn:
		v := c.alias("v")
		args := make([]any, 0, len(n.Values))
		for _, val := range n.Values {
			args = append(args, valueArg(val))
		}
		scope := r.valueScope(c, v, n.Attr.ID.String(), n.Link, e)
		return fmt.Sprintf("EXISTS (%s AND %s = %s)",
			scope, columnExpr(v, n.Attr.DataType), arrayExpr(c.arg(pq.Array(args)), n.Attr.DataType)), nil

	case *query.BoundRange:
		v := c.alias("v")
		scope := r.valueScope(c, v, n.Attr.ID.String(), n.Link, e)
		return fmt.Sprintf("EXISTS (%s AND %s BETWEEN %s AND %s)",
			scope, columnExpr(v, n.Attr.DataType), c.arg(valueArg(n.Lo)), c.arg(valueArg(n.Hi))), nil

	case *query.BoundHas:
		v := c.alias("v")
		return fmt.Sprintf("EXISTS (%s)", r.valueScope(c, v, n.Attr.ID.String(), n.Link, e)), nil

	case *query.BoundStringMatch:
		v := c.alias("v")
		scope := r.valueScope(c, v, n.Attr.ID.String(), n.Link, e)
		var pred string
		switch n.Kind {
		case fql.MatchContains:
			pred = fmt.Sprintf("strpos(%s.value_text, %s) > 0", v, c.arg(n.Value))
		case fql.MatchIContains:
			pred = fmt.Sprintf("strpos(lower(%s.value_text), lower(%s)) > 0", v, c.arg(n.Value))
		case fql.MatchIEquals:
			pred = fmt.Sprintf("lower(%s.value_text) = lower(%s)", v, c.arg(n.Value))
		default:
			return "", fmt.Errorf("unknown string match %q", n.Kind)
		}
		return fmt.Sprintf("EXISTS (%s AND %s)", scope, pred), nil

	case *query.BoundMatches:
		s := c.alias("s")
		return fmt.Sprintf(`EXISTS (SELECT 1 FROM flexitype_entity_search %s
		 WHERE %s.tenant_id = %s AND %s.entity_id = %s
		   AND %s.text_vector @@ plainto_tsquery('simple', %s))`,
			s, s, e.tenant, s, e.entity, s, c.arg(n.Query)), nil

	case *query.BoundTraversal:
		return r.compileTraversal(c, n, e)

	default:
		return "", fmt.Errorf("unsupported bound node %T", node)
	}
}

// valueScope renders the correlated FROM/WHERE prefix selecting the
// current entity's live values of one attribute. Link-scoped attributes
// anchor on the enclosing relationship's id instead of the entity.
func (r *queryRepository) valueScope(c *compiler, alias, attrDefID string, link bool, e entityRef) string {
	entity := e.entity
	if link {
		entity = e.link
	}
	return fmt.Sprintf(`SELECT 1 FROM flexitype_attribute_value %s
	 WHERE %s.tenant_id = %s AND %s.entity_id = %s
	   AND %s.attribute_definition_id = %s AND %s.archived_at IS NULL`,
		alias, alias, e.tenant, alias, entity, alias, c.arg(attrDefID), alias)
}

// columnExpr renders the typed column for comparisons. Decimals persist in
// value_text and compare numerically only through a cast.
func columnExpr(alias string, dt valueobjects.DataType) string {
	if dt == valueobjects.DataTypeDecimal {
		return "(" + alias + ".value_text)::numeric"
	}
	return alias + "." + valueColumnName(dt)
}

// arrayExpr renders the ANY() operand with the cast the column type needs.
func arrayExpr(placeholder string, dt valueobjects.DataType) string {
	if dt == valueobjects.DataTypeDecimal {
		return "ANY(" + placeholder + "::numeric[])"
	}
	return "ANY(" + placeholder + ")"
}

var sqlOps = map[fql.CompareOp]string{
	fql.CmpEq: "=", fql.CmpNeq: "<>",
	fql.CmpGt: ">", fql.CmpGte: ">=",
	fql.CmpLt: "<", fql.CmpLte: "<=",
}

func (r *queryRepository) compileCompare(c *compiler, n *query.BoundCompare, e entityRef) (string, error) {
	op, ok := sqlOps[n.Op]
	if !ok {
		return "", fmt.Errorf("unsupported operator %q", n.Op)
	}
	v := c.alias("v")
	col := columnExpr(v, n.Attr.DataType)

	switch n.Func {
	case fql.FuncMin, fql.FuncMax:
		// NULL (no values) never satisfies the comparison — absent
		// attributes don't match, mirroring the EXISTS semantics.
		return fmt.Sprintf("(%s) %s %s",
			strings.Replace(r.valueScope(c, v, n.Attr.ID.String(), n.Link, e),
				"SELECT 1", fmt.Sprintf("SELECT %s(%s)", n.Func, col), 1),
			op, c.arg(valueArg(n.Value))), nil

	case fql.FuncCount:
		return fmt.Sprintf("(%s) %s %s",
			strings.Replace(r.valueScope(c, v, n.Attr.ID.String(), n.Link, e),
				"SELECT 1", "SELECT count(*)", 1),
			op, c.arg(n.Value.Int())), nil

	case fql.FuncLength:
		return fmt.Sprintf("EXISTS (%s AND char_length(%s.value_text) %s %s)",
			r.valueScope(c, v, n.Attr.ID.String(), n.Link, e), v, op, c.arg(n.Value.Int())), nil

	default:
		return fmt.Sprintf("EXISTS (%s AND %s %s %s)",
			r.valueScope(c, v, n.Attr.ID.String(), n.Link, e), col, op, c.arg(valueArg(n.Value))), nil
	}
}

func (r *queryRepository) compileTraversal(c *compiler, n *query.BoundTraversal, e entityRef) (string, error) {
	rel := c.alias("r")

	nearCol, farCol := "parent_entity_id", "child_entity_id"
	if n.Direction == fql.DirParent {
		nearCol, farCol = "child_entity_id", "parent_entity_id"
	}

	// Arguments bind positionally: the definition id's placeholder appears
	// before the inner expression's, so register it first.
	defArg := c.arg(n.Def.ID.String())

	inner, err := r.compile(c, n.Inner, entityRef{
		tenant: rel + ".tenant_id",
		entity: rel + "." + farCol,
		// The counterpart's declared type isn't materialised on the link;
		// type conditions inside traversals compare against value rows.
		typeID: r.counterpartType(c, rel, farCol),
		link:   rel + ".id",
	})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`EXISTS (SELECT 1 FROM flexitype_relationship %s
	 WHERE %s.tenant_id = %s AND %s.relationship_definition_id = %s
	   AND %s.archived_at IS NULL AND %s.%s = %s
	   AND %s)`,
		rel, rel, e.tenant, rel, defArg,
		rel, rel, nearCol, e.entity, inner), nil
}

// counterpartType resolves the counterpart entity's declared type as a
// scalar subquery over its value rows (any row carries it).
func (r *queryRepository) counterpartType(_ *compiler, rel, farCol string) string {
	return fmt.Sprintf(`(SELECT tv.type_definition_id FROM flexitype_attribute_value tv
	 WHERE tv.tenant_id = %s.tenant_id AND tv.entity_id = %s.%s AND tv.archived_at IS NULL
	 LIMIT 1)`, rel, rel, farCol)
}
