package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/zkrebbekx/flexitype/application/query"
	"github.com/zkrebbekx/flexitype/domain/attribute"
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
	scope   valueobjects.Scope
}

func (c *compiler) alias(prefix string) string {
	c.aliasNo++
	return fmt.Sprintf("%s%d", prefix, c.aliasNo)
}

func (c *compiler) arg(v any) string {
	c.args = append(c.args, v)
	return "?"
}

func (r *queryRepository) Search(ctx context.Context, tenant valueobjects.TenantID, rootTypeIDs []valueobjects.TypeDefinitionID, node query.BoundNode, scope valueobjects.Scope, page db.Page) ([]domainvalue.EntitySummary, int, error) {
	c := &compiler{scope: scope}

	rootIDs := make([]string, 0, len(rootTypeIDs))
	for _, id := range rootTypeIDs {
		rootIDs = append(rootIDs, id.String())
	}

	// The candidate set is every live entity of the queried types with its
	// value_count and last_updated_at. That is exactly the entity-summary
	// projection (flexitype_entity_summary, maintained by a trigger on
	// flexitype_attribute_value), so reading it here replaces the per-query
	// GROUP BY over the whole value table with a plain index scan. The FQL
	// filter compiled below applies as EXISTS subqueries over the raw value
	// rows, so it is unaffected — only this enumeration base changed.
	// A single root type compiles to an equality, not `= ANY`, so the
	// (tenant_id, type_definition_id, last_updated_at DESC, entity_id) ordering
	// index serves the page as a bounded index scan. With `= ANY` the planner
	// cannot use the index for ordering, so every query sequentially scanned
	// the tenant's whole entity population and sorted it — measured at 200k
	// entities, 57 ms and an external merge sort on disk versus 0.15 ms as an
	// index-only scan. The LIMIT offers no protection, because the sort happens
	// before it. typeDefPredicate is the same helper the entity browser uses.
	tenantArg := c.arg(tenant.String())
	typePred, typeArg := typeDefPredicate(rootIDs)
	typeClause := strings.Replace(typePred, "?", c.arg(typeArg), 1)
	base := fmt.Sprintf(`SELECT tenant_id, type_definition_id, entity_id, value_count, last_updated_at
	 FROM flexitype_entity_summary
	 WHERE tenant_id = %s AND %s`, tenantArg, typeClause)

	where, err := r.compile(c, node, entityRef{
		tenant: "e.tenant_id",
		entity: "e.entity_id",
		typeID: "e.type_definition_id",
	})
	if err != nil {
		return nil, 0, err
	}

	// The count (when requested) is over the full filtered set, so it uses the
	// args built for base + where before the keyset and limit are added.
	countArgs := append([]any(nil), c.args...)
	countSQL := fmt.Sprintf(`SELECT count(*) FROM (%s) e WHERE %s`, base, where)

	// Keyset on the ordered (last update, entity id) so a page is stable under
	// concurrent writes: newest-first, entity_id as the unique tiebreaker.
	// ValidateKeyset rejects a cursor of the wrong arity, and a first value
	// that "::timestamptz" cannot parse, as a validation error. Before that
	// check, a wrong-arity cursor served page 1 again and a bad timestamp
	// failed inside PostgreSQL with SQLSTATE 22007.
	// A SWEEP pages on the immutable key instead. last_updated_at is
	// rewritten by the summary trigger on every value write, so an entity the
	// sweep has not reached yet, written mid-sweep, jumps ahead of the
	// newest-first cursor and can never satisfy the "strictly older"
	// predicate again — it is dropped, silently. Facet counts and a filtered
	// CSV export are exactly such sweeps, and only the unfiltered branch
	// could ask for stable ordering before this: the FQL path had no way to,
	// so its counts disagreed with the grid and its exports lost rows with
	// no total to reveal it. entity_id never changes, and it is the trailing
	// column of the summary's primary key, so this ordering is an index scan
	// too.
	keysetCols := entitySummaryKeyset
	orderBy := "e.last_updated_at DESC, e.entity_id"
	if page.Stable {
		keysetCols = []db.KeysetColumn{{Expr: "entity_id"}}
		orderBy = "e.entity_id"
	}
	keyset := ""
	if page.Cursor != "" {
		vals, verr := db.ValidateKeyset(keysetCols, page.Cursor)
		if verr != nil {
			return nil, 0, verr
		}
		if page.Stable {
			keyset = fmt.Sprintf(` AND e.entity_id > %s`, c.arg(vals[0]))
		} else {
			keyset = fmt.Sprintf(` AND ((e.last_updated_at < %s::timestamptz) OR (e.last_updated_at = %s::timestamptz AND e.entity_id > %s))`,
				c.arg(vals[0]), c.arg(vals[0]), c.arg(vals[1]))
		}
	}

	sql := fmt.Sprintf(`SELECT e.entity_id, e.type_definition_id, e.value_count, e.last_updated_at
	 FROM (%s) e
	 WHERE %s%s
	 ORDER BY %s
	 LIMIT %s`,
		base, where, keyset, orderBy, c.arg(page.FetchLimit()))

	var rows []struct {
		EntityID         string    `db:"entity_id"`
		TypeDefinitionID ulid.ID   `db:"type_definition_id"`
		ValueCount       int       `db:"value_count"`
		LastUpdatedAt    time.Time `db:"last_updated_at"`
	}
	if err := r.q.SelectContext(ctx, &rows, bind(sql), c.args...); err != nil {
		return nil, 0, fmt.Errorf("execute query: %w", err)
	}

	out := make([]domainvalue.EntitySummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, domainvalue.EntitySummary{
			EntityID:         valueobjects.EntityID(row.EntityID),
			TypeDefinitionID: valueobjects.TypeDefinitionID{ID: row.TypeDefinitionID},
			ValueCount:       row.ValueCount,
			LastUpdatedAt:    row.LastUpdatedAt,
		})
	}
	total := 0
	if page.WantTotal {
		if err := r.q.GetContext(ctx, &total, bind(countSQL), countArgs...); err != nil {
			return nil, 0, fmt.Errorf("count query: %w", err)
		}
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
		scope := r.valueScope(c, v, n.Attr, n.Link, e)
		return fmt.Sprintf("EXISTS (%s AND %s = %s)",
			scope, columnExpr(v, n.Attr.DataType), arrayExpr(c.arg(pq.Array(args)), n.Attr.DataType)), nil

	case *query.BoundRange:
		v := c.alias("v")
		scope := r.valueScope(c, v, n.Attr, n.Link, e)
		return fmt.Sprintf("EXISTS (%s AND %s BETWEEN %s AND %s)",
			scope, columnExpr(v, n.Attr.DataType), c.arg(valueArg(n.Lo)), c.arg(valueArg(n.Hi))), nil

	case *query.BoundHas:
		v := c.alias("v")
		return fmt.Sprintf("EXISTS (%s)", r.valueScope(c, v, n.Attr, n.Link, e)), nil

	case *query.BoundStringMatch:
		v := c.alias("v")
		scope := r.valueScope(c, v, n.Attr, n.Link, e)
		var pred string
		switch n.Kind {
		// LIKE/ILIKE rather than strpos: strpos is opaque to the planner, so a
		// substring match could only ever be a filter applied per candidate row,
		// and no index could serve it. The pattern operators can be served by a
		// pg_trgm GIN index, which migration 000021 restores. The needle is
		// escaped so a value containing % or _ matches literally.
		case fql.MatchContains:
			pred = fmt.Sprintf("%s.value_text LIKE %s ESCAPE '\\'", v, c.arg(containsPattern(n.Value)))
		case fql.MatchIContains:
			pred = fmt.Sprintf("%s.value_text ILIKE %s ESCAPE '\\'", v, c.arg(containsPattern(n.Value)))
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

// likeEscaper escapes the three characters LIKE treats specially, so a needle
// containing them matches literally. The backslash must be replaced first, or
// the escapes introduced for % and _ would themselves be escaped.
var likeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// containsPattern wraps an escaped needle in wildcards for a substring match.
func containsPattern(needle string) string {
	return "%" + likeEscaper.Replace(needle) + "%"
}

// valueScope renders the correlated FROM/WHERE prefix selecting the
// current entity's live values of one attribute. Link-scoped attributes
// anchor on the enclosing relationship's id instead of the entity.
//
// The query scope pins each dimension separately. Locale narrows only a
// localizable attribute. Channel narrows only a scopable one. The write
// path stores an empty string in a dimension the attribute does not carry,
// so pinning that dimension too would exclude every row of a
// single-dimension attribute (issue #474). Base (zero) scope selects the
// unscoped value.
// Link attributes ignore scope entirely — that keeps the pre-existing
// behavior, stated here as a deliberate choice.
func (r *queryRepository) valueScope(c *compiler, alias string, attr attribute.Snapshot, link bool, e entityRef) string {
	entity := e.entity
	if link {
		entity = e.link
	}
	base := fmt.Sprintf(`SELECT 1 FROM flexitype_attribute_value %s
	 WHERE %s.tenant_id = %s AND %s.entity_id = %s
	   AND %s.attribute_definition_id = %s AND %s.archived_at IS NULL`,
		alias, alias, e.tenant, alias, entity, alias, c.arg(attr.ID.String()), alias)
	if !link {
		if attr.Localizable {
			base += fmt.Sprintf(" AND %s.locale = %s", alias, c.arg(c.scope.Locale))
		}
		if attr.Scopable {
			base += fmt.Sprintf(" AND %s.channel = %s", alias, c.arg(c.scope.Channel))
		}
	}
	return base
}

// columnExpr renders the typed column for comparisons. Decimals persist in
// value_text and compare numerically only through a cast.
//
// Textual types never reach an ordering comparison or min/max here — the
// binder restricts both to IsOrdered() types — so no collation pin is
// needed for SQL/memory parity; text only meets = / != / in, where
// PostgreSQL compares bytes regardless of collation.
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
			strings.Replace(r.valueScope(c, v, n.Attr, n.Link, e),
				"SELECT 1", fmt.Sprintf("SELECT %s(%s)", n.Func, col), 1),
			op, c.arg(valueArg(n.Value))), nil

	case fql.FuncCount:
		return fmt.Sprintf("(%s) %s %s",
			strings.Replace(r.valueScope(c, v, n.Attr, n.Link, e),
				"SELECT 1", "SELECT count(*)", 1),
			op, c.arg(n.Value.Int())), nil

	case fql.FuncLength:
		return fmt.Sprintf("EXISTS (%s AND char_length(%s.value_text) %s %s)",
			r.valueScope(c, v, n.Attr, n.Link, e), v, op, c.arg(n.Value.Int())), nil

	default:
		return fmt.Sprintf("EXISTS (%s AND %s %s %s)",
			r.valueScope(c, v, n.Attr, n.Link, e), col, op, c.arg(valueArg(n.Value))), nil
	}
}

func (r *queryRepository) compileTraversal(c *compiler, n *query.BoundTraversal, e entityRef) (string, error) {
	rel := c.alias("r")

	// Arguments bind positionally: the definition id's placeholder appears
	// before the inner expression's, so register it first.
	defArg := c.arg(n.Def.ID.String())

	// linked() matches either end and evaluates against the opposite one.
	var nearCond, farExpr string
	switch n.Direction {
	case fql.DirAny:
		nearCond = fmt.Sprintf("(%s.parent_entity_id = %s OR %s.child_entity_id = %s)",
			rel, e.entity, rel, e.entity)
		farExpr = fmt.Sprintf("(CASE WHEN %s.parent_entity_id = %s THEN %s.child_entity_id ELSE %s.parent_entity_id END)",
			rel, e.entity, rel, rel)
	case fql.DirParent:
		nearCond = fmt.Sprintf("%s.child_entity_id = %s", rel, e.entity)
		farExpr = rel + ".parent_entity_id"
	default:
		nearCond = fmt.Sprintf("%s.parent_entity_id = %s", rel, e.entity)
		farExpr = rel + ".child_entity_id"
	}

	// A relationship can outlive its counterpart: removing an entity's last
	// value deletes its entity-summary row (migration 000019), but the
	// relationship stays live. Without this guard the traversal matches such
	// value-less "ghost" counterparts, so count()=0, `not has()` and negated
	// type conditions wrongly select the near entity (issue #475). The guard
	// reads the same projection the root candidate set reads, so a
	// counterpart is traversable exactly when it is visible at the root.
	// idx_flexitype_entity_summary_entity (tenant_id, entity_id), added in
	// migration 000031, serves the probe.
	es := c.alias("es")
	liveGuard := fmt.Sprintf(`EXISTS (SELECT 1 FROM flexitype_entity_summary %s
	 WHERE %s.tenant_id = %s.tenant_id AND %s.entity_id = %s)`,
		es, es, rel, es, farExpr)

	inner, err := r.compile(c, n.Inner, entityRef{
		tenant: rel + ".tenant_id",
		entity: farExpr,
		// The counterpart's declared type isn't materialised on the link;
		// type conditions inside traversals compare against value rows.
		typeID: r.counterpartType(rel, farExpr),
		link:   rel + ".id",
	})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`EXISTS (SELECT 1 FROM flexitype_relationship %s
	 WHERE %s.tenant_id = %s AND %s.relationship_definition_id = %s
	   AND %s.archived_at IS NULL AND %s
	   AND %s
	   AND %s)`,
		rel, rel, e.tenant, rel, defArg,
		rel, nearCond, liveGuard, inner), nil
}

// counterpartType resolves the counterpart entity's declared type as a
// scalar subquery over its value rows. farExpr is a full SQL expression for
// the counterpart entity id.
//
// ORDER BY created_at, id, matching EntityAnchor: mid-reanchor the value
// rows transiently carry mixed type_definition_id, and an unordered LIMIT 1
// let the same `is type(...)` traversal match or not match across
// executions of one query.
func (r *queryRepository) counterpartType(rel, farExpr string) string {
	return fmt.Sprintf(`(SELECT tv.type_definition_id FROM flexitype_attribute_value tv
	 WHERE tv.tenant_id = %s.tenant_id AND tv.entity_id = %s AND tv.archived_at IS NULL
	 ORDER BY tv.created_at, tv.id
	 LIMIT 1)`, rel, farExpr)
}
