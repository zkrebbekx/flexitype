package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/zkrebbekx/flexitype/application/query"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/fql"
)

// queryRepo evaluates bound FQL trees directly against the store — the
// in-process twin of the SQL compiler. Semantics mirror PostgreSQL:
// conditions are EXISTS over live values, traversals correlate through
// live relationships, matches() probes the search projection.
type queryRepo struct{ s *Store }

// evalScope identifies "the current entity" during evaluation.
type evalScope struct {
	tenant string
	entity string
	typeID string // declared type; resolved lazily inside traversals
	link   string // enclosing relationship id ("" at root)
	// query is the locale/channel selecting scoped-attribute values; base
	// (zero) matches unscoped values.
	query valueobjects.Scope
}

func (r *queryRepo) Search(_ context.Context, tenant valueobjects.TenantID, rootTypeIDs []valueobjects.TypeDefinitionID, node query.BoundNode, scope valueobjects.Scope, page db.Page) ([]domainvalue.EntitySummary, int, error) {
	wanted := make(map[string]bool, len(rootTypeIDs))
	for _, id := range rootTypeIDs {
		wanted[id.String()] = true
	}

	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	// Aggregate live values into candidate entities, as the SQL base query does.
	agg := map[string]*domainvalue.EntitySummary{}
	for _, snap := range r.s.values {
		if snap.TenantID != tenant || snap.ArchivedAt != nil || !wanted[snap.TypeDefinitionID.String()] {
			continue
		}
		key := snap.TypeDefinitionID.String() + "\x00" + snap.EntityID.String()
		e := agg[key]
		if e == nil {
			e = &domainvalue.EntitySummary{
				EntityID:         snap.EntityID,
				TypeDefinitionID: snap.TypeDefinitionID,
			}
			agg[key] = e
		}
		e.ValueCount++
		if snap.UpdatedAt.After(e.LastUpdatedAt) {
			e.LastUpdatedAt = snap.UpdatedAt
		}
	}

	var matched []domainvalue.EntitySummary
	for _, e := range agg {
		res, err := r.eval(node, evalScope{
			tenant: tenant.String(),
			entity: e.EntityID.String(),
			typeID: e.TypeDefinitionID.String(),
			query:  scope,
		})
		if err != nil {
			return nil, 0, err
		}
		// Only a definite TRUE selects the row — UNKNOWN (SQL NULL) and
		// FALSE both exclude, matching a Postgres WHERE clause.
		if res == triTrue {
			matched = append(matched, *e)
		}
	}

	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].LastUpdatedAt.Equal(matched[j].LastUpdatedAt) {
			return matched[i].LastUpdatedAt.After(matched[j].LastUpdatedAt)
		}
		return matched[i].EntityID.String() < matched[j].EntityID.String()
	})
	pageItems, total := paginate(matched, page, func(e domainvalue.EntitySummary) string { return entityCursor(e.LastUpdatedAt, e.EntityID.String()) })
	return pageItems, total, nil
}

// tri is a three-valued truth (Kleene) logic value, mirroring SQL's
// boolean-with-NULL semantics so the in-memory evaluator agrees with the
// PostgreSQL compiler on which rows a query selects.
type tri int

const (
	triFalse tri = iota
	triTrue
	triUnknown
)

func triOf(b bool) tri {
	if b {
		return triTrue
	}
	return triFalse
}

// eval runs one bound node against the current scope, returning a
// three-valued result. Callers hold the store read lock.
func (r *queryRepo) eval(node query.BoundNode, s evalScope) (tri, error) {
	switch n := node.(type) {
	case *query.BoundLogical:
		// Kleene AND/OR: AND is false if any operand is false, else unknown
		// if any is unknown, else true; OR is the dual.
		anyUnknown := false
		for _, expr := range n.Exprs {
			res, err := r.eval(expr, s)
			if err != nil {
				return triFalse, err
			}
			if n.Op == fql.OpAnd && res == triFalse {
				return triFalse, nil
			}
			if n.Op == fql.OpOr && res == triTrue {
				return triTrue, nil
			}
			if res == triUnknown {
				anyUnknown = true
			}
		}
		if anyUnknown {
			return triUnknown, nil
		}
		return triOf(n.Op == fql.OpAnd), nil

	case *query.BoundNot:
		res, err := r.eval(n.Expr, s)
		switch res {
		case triTrue:
			return triFalse, err
		case triFalse:
			return triTrue, err
		default:
			return triUnknown, err // NOT NULL = NULL
		}

	case *query.BoundType:
		member := false
		for _, id := range n.TypeIDs {
			if id.String() == s.typeID {
				member = true
				break
			}
		}
		if n.Negate {
			return triOf(!member), nil
		}
		return triOf(member), nil

	case *query.BoundCompare:
		return r.evalCompare(n, s)

	case *query.BoundIn:
		for _, snap := range r.scopedValues(n.Attr.ID.String(), n.Link, (n.Attr.Localizable || n.Attr.Scopable) && !n.Link, s) {
			for _, want := range n.Values {
				if snap.Value.Equal(want) {
					return triTrue, nil
				}
			}
		}
		return triFalse, nil

	case *query.BoundRange:
		for _, snap := range r.scopedValues(n.Attr.ID.String(), n.Link, (n.Attr.Localizable || n.Attr.Scopable) && !n.Link, s) {
			lo, err := snap.Value.Compare(n.Lo)
			if err != nil {
				return triFalse, err
			}
			hi, err := snap.Value.Compare(n.Hi)
			if err != nil {
				return triFalse, err
			}
			if lo >= 0 && hi <= 0 {
				return triTrue, nil
			}
		}
		return triFalse, nil

	case *query.BoundHas:
		return triOf(len(r.scopedValues(n.Attr.ID.String(), n.Link, (n.Attr.Localizable || n.Attr.Scopable) && !n.Link, s)) > 0), nil

	case *query.BoundStringMatch:
		for _, snap := range r.scopedValues(n.Attr.ID.String(), n.Link, (n.Attr.Localizable || n.Attr.Scopable) && !n.Link, s) {
			text := snap.Value.Text()
			switch n.Kind {
			case fql.MatchContains:
				if strings.Contains(text, n.Value) {
					return triTrue, nil
				}
			case fql.MatchIContains:
				if containsFold(text, n.Value) {
					return triTrue, nil
				}
			case fql.MatchIEquals:
				if strings.EqualFold(text, n.Value) {
					return triTrue, nil
				}
			default:
				return triFalse, fmt.Errorf("unknown string match %q", n.Kind)
			}
		}
		return triFalse, nil

	case *query.BoundMatches:
		doc, ok := r.s.searchDocs[s.tenant+"\x00"+s.entity]
		if !ok {
			return triFalse, nil
		}
		return triOf(matchesText(doc.text, n.Query)), nil

	case *query.BoundTraversal:
		return r.evalTraversal(n, s)

	default:
		return triFalse, fmt.Errorf("unsupported bound node %T", node)
	}
}

func (r *queryRepo) evalCompare(n *query.BoundCompare, s evalScope) (tri, error) {
	snaps := r.scopedValues(n.Attr.ID.String(), n.Link, (n.Attr.Localizable || n.Attr.Scopable) && !n.Link, s)

	switch n.Func {
	case fql.FuncMin, fql.FuncMax:
		// SQL compiles these to a scalar subquery: min/max over no rows is
		// NULL, and NULL compared to anything is NULL (UNKNOWN) — which,
		// unlike FALSE, stays excluded even under NOT.
		if len(snaps) == 0 {
			return triUnknown, nil
		}
		best := snaps[0].Value
		for _, snap := range snaps[1:] {
			cmp, err := snap.Value.Compare(best)
			if err != nil {
				return triFalse, err
			}
			if (n.Func == fql.FuncMin && cmp < 0) || (n.Func == fql.FuncMax && cmp > 0) {
				best = snap.Value
			}
		}
		ok, err := compareValues(best, n.Value, n.Op)
		return triOf(ok), err

	case fql.FuncCount:
		// count() over no rows is 0, not NULL — a definite comparison.
		ok, err := compareInts(int64(len(snaps)), n.Value.Int(), n.Op)
		return triOf(ok), err

	case fql.FuncLength:
		for _, snap := range snaps {
			ok, err := compareInts(int64(snap.Value.Length()), n.Value.Int(), n.Op)
			if err != nil {
				return triFalse, err
			}
			if ok {
				return triTrue, nil
			}
		}
		return triFalse, nil

	default:
		for _, snap := range snaps {
			ok, err := compareValues(snap.Value, n.Value, n.Op)
			if err != nil {
				return triFalse, err
			}
			if ok {
				return triTrue, nil
			}
		}
		return triFalse, nil
	}
}

func (r *queryRepo) evalTraversal(n *query.BoundTraversal, s evalScope) (tri, error) {
	for _, rel := range r.s.rels {
		if rel.TenantID.String() != s.tenant || rel.ArchivedAt != nil ||
			!rel.DefinitionID.Equals(n.Def.ID) {
			continue
		}

		parent, child := rel.ParentEntityID.String(), rel.ChildEntityID.String()
		var far string
		switch n.Direction {
		case fql.DirAny:
			// linked(): match either end, evaluate the opposite one.
			switch s.entity {
			case parent:
				far = child
			case child:
				far = parent
			default:
				continue
			}
		case fql.DirParent:
			if child != s.entity {
				continue
			}
			far = parent
		default:
			if parent != s.entity {
				continue
			}
			far = child
		}

		res, err := r.eval(n.Inner, evalScope{
			tenant: s.tenant,
			entity: far,
			typeID: r.declaredType(s.tenant, far),
			link:   rel.ID.String(),
			query:  s.query,
		})
		if err != nil {
			return triFalse, err
		}
		// The traversal is an EXISTS over counterparts: any counterpart
		// satisfying the inner expression makes it true, matching SQL.
		if res == triTrue {
			return triTrue, nil
		}
	}
	return triFalse, nil
}

// scopedValues returns the current scope's live values of one attribute.
// Link-scoped attributes anchor on the enclosing relationship's id.
func (r *queryRepo) scopedValues(attrDefID string, link, scoped bool, s evalScope) []domainvalue.Snapshot {
	entity := s.entity
	if link {
		entity = s.link
	}
	var out []domainvalue.Snapshot
	for _, snap := range r.s.values {
		if snap.TenantID.String() != s.tenant || snap.EntityID.String() != entity ||
			snap.AttributeDefinitionID.String() != attrDefID || snap.ArchivedAt != nil {
			continue
		}
		// Scoped attributes match only within the query's locale/channel;
		// non-scoped attributes ignore scope.
		if scoped && (snap.Locale != s.query.Locale || snap.Channel != s.query.Channel) {
			continue
		}
		out = append(out, snap)
	}
	return out
}

// declaredType resolves an entity's declared type from any of its live
// value rows, as the SQL counterpart-type subquery does.
func (r *queryRepo) declaredType(tenant, entity string) string {
	for _, snap := range r.s.values {
		if snap.TenantID.String() == tenant && snap.EntityID.String() == entity && snap.ArchivedAt == nil {
			return snap.TypeDefinitionID.String()
		}
	}
	return ""
}

// compareValues applies a comparison operator to two typed values.
// Equality uses Value.Equal; ordering uses Value.Compare with a lexical
// fallback for textual types, matching the SQL text column behaviour.
func compareValues(a, b valueobjects.Value, op fql.CompareOp) (bool, error) {
	switch op {
	case fql.CmpEq:
		return a.Equal(b), nil
	case fql.CmpNeq:
		return !a.Equal(b), nil
	}

	cmp, err := a.Compare(b)
	if err != nil {
		if !a.DataType().IsTextual() {
			return false, err
		}
		cmp = strings.Compare(a.Text(), b.Text())
	}
	switch op {
	case fql.CmpGt:
		return cmp > 0, nil
	case fql.CmpGte:
		return cmp >= 0, nil
	case fql.CmpLt:
		return cmp < 0, nil
	case fql.CmpLte:
		return cmp <= 0, nil
	default:
		return false, fmt.Errorf("unsupported operator %q", op)
	}
}

func compareInts(a, b int64, op fql.CompareOp) (bool, error) {
	switch op {
	case fql.CmpEq:
		return a == b, nil
	case fql.CmpNeq:
		return a != b, nil
	case fql.CmpGt:
		return a > b, nil
	case fql.CmpGte:
		return a >= b, nil
	case fql.CmpLt:
		return a < b, nil
	case fql.CmpLte:
		return a <= b, nil
	default:
		return false, fmt.Errorf("unsupported operator %q", op)
	}
}

// matchesText approximates plainto_tsquery('simple', q): every word of the
// query must appear as a whole word in the document text, case-insensitive.
func matchesText(docText, queryText string) bool {
	queryWords := tokenize(queryText)
	if len(queryWords) == 0 {
		return false
	}
	docWords := map[string]bool{}
	for _, w := range tokenize(docText) {
		docWords[w] = true
	}
	for _, w := range queryWords {
		if !docWords[w] {
			return false
		}
	}
	return true
}

func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
