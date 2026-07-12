package query

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/fql"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// maxTraversalDepth bounds nested relationship crossings per query.
const maxTraversalDepth = 5

// scope is one binding context: the attribute and relationship names
// visible to expressions at this level, plus link attributes when inside a
// traversal.
type scope struct {
	attrs     map[string]domainattribute.Snapshot
	linkAttrs map[string]domainattribute.Snapshot
	rels      map[string]domainrelationship.DefinitionSnapshot
	depth     int
}

// binder resolves parsed queries against the schema.
type binder struct {
	tenant      valueobjects.TenantID
	searchIndex bool
	access      uow.Access
	typeDefs    domaintypedef.Repository
	attrs       domainattribute.Repository
	relDefs     domainrelationship.DefinitionRepository
	// units resolves unit families for unit-suffixed quantity literals; nil
	// when unit families are disabled.
	units appunit.Store
	// typesByName caches every live entity type for the virtual type field.
	typesByName map[string]domaintypedef.Snapshot
}

// scopeFor assembles the binding scope for one root type: attributes from
// the type's chain and its descendants (shadowing rules keep names
// unambiguous), and every relationship definition touching those types.
func (b *binder) scopeFor(ctx context.Context, root *domaintypedef.TypeDefinition, depth int) (*scope, []*domaintypedef.TypeDefinition, error) {
	chain, err := apptypedef.Chain(ctx, b.typeDefs, root)
	if err != nil {
		return nil, nil, err
	}
	descendants, err := apptypedef.Descendants(ctx, b.typeDefs, root)
	if err != nil {
		return nil, nil, err
	}
	types := make([]*domaintypedef.TypeDefinition, 0, len(chain)+len(descendants))
	types = append(types, chain...)
	types = append(types, descendants...)

	s := &scope{
		attrs: make(map[string]domainattribute.Snapshot),
		rels:  make(map[string]domainrelationship.DefinitionSnapshot),
		depth: depth,
	}
	typeIDs := make([]valueobjects.TypeDefinitionID, 0, len(types))
	for _, t := range types {
		// Archived schema is invisible to search: neither archived types nor
		// archived attributes bind.
		if t.IsArchived() {
			continue
		}
		typeIDs = append(typeIDs, t.ID())
		attrs, _, err := b.attrs.ListByTypeDefinition(ctx, t.ID(), db.Page{Limit: 500})
		if err != nil {
			return nil, nil, err
		}
		for _, a := range attrs {
			if a.IsArchived() {
				continue
			}
			// Field-level access control: an attribute the principal may not
			// read is invisible to the binder, so filtering on it fails as an
			// unknown attribute rather than leaking its existence (oracle
			// attacks). Admins and unauthenticated development see all.
			if !b.access.CanRead(a.InternalName()) {
				continue
			}
			s.attrs[a.InternalName()] = a.Snapshot()
		}
	}

	rels, _, err := b.relDefs.List(ctx, domainrelationship.DefinitionFilter{
		TenantID:          b.tenant,
		TypeDefinitionIDs: typeIDs,
	}, db.Page{Limit: 500})
	if err != nil {
		return nil, nil, err
	}
	for _, r := range rels {
		s.rels[r.InternalName()] = r.Snapshot()
	}

	// The root type set: the queried type plus its descendants (ancestors
	// contribute attributes, not rows).
	rowTypes := append([]*domaintypedef.TypeDefinition{root}, descendants...)
	return s, rowTypes, nil
}

// linkAttrsFor resolves a relationship definition's inherited attribute
// sets into a name → attribute map.
func (b *binder) linkAttrsFor(ctx context.Context, def domainrelationship.DefinitionSnapshot) (map[string]domainattribute.Snapshot, error) {
	out := make(map[string]domainattribute.Snapshot)
	current := def
	for depth := 0; depth < maxTraversalDepth*2; depth++ {
		attrs, _, err := b.attrs.ListByTypeDefinition(ctx, current.AttributeSetID, db.Page{Limit: 500})
		if err != nil {
			return nil, err
		}
		for _, a := range attrs {
			if a.IsArchived() {
				continue
			}
			if _, exists := out[a.InternalName()]; !exists {
				out[a.InternalName()] = a.Snapshot()
			}
		}
		if current.ExtendsID == nil {
			return out, nil
		}
		base, err := b.relDefs.Get(ctx, *current.ExtendsID)
		if err != nil {
			return nil, err
		}
		current = base.Snapshot()
	}
	return out, nil
}

func (b *binder) bind(ctx context.Context, node fql.Node, s *scope) (BoundNode, error) {
	switch n := node.(type) {
	case *fql.Logical:
		out := &BoundLogical{Op: n.Op}
		for _, e := range n.Exprs {
			bound, err := b.bind(ctx, e, s)
			if err != nil {
				return nil, err
			}
			out.Exprs = append(out.Exprs, bound)
		}
		return out, nil

	case *fql.Not:
		inner, err := b.bind(ctx, n.Expr, s)
		if err != nil {
			return nil, err
		}
		return &BoundNot{Expr: inner}, nil

	case *fql.Compare:
		if n.Field.Scope == fql.ScopeType {
			return b.bindTypeCompare(n)
		}
		return b.bindCompare(ctx, n, s)

	case *fql.In:
		if n.Field.Scope == fql.ScopeType {
			return b.bindTypeIn(n)
		}
		return b.bindIn(ctx, n, s)

	case *fql.Range:
		return b.bindRange(ctx, n, s)

	case *fql.Has:
		attr, link, err := b.resolveField(n.Field, s)
		if err != nil {
			return nil, err
		}
		return &BoundHas{Attr: attr, Link: link}, nil

	case *fql.StringMatch:
		attr, link, err := b.resolveField(n.Field, s)
		if err != nil {
			return nil, err
		}
		if !attr.DataType.IsTextual() {
			return nil, positioned(n.Pos, "%s requires a textual attribute; %q is %s", n.Kind, attr.InternalName, attr.DataType)
		}
		return &BoundStringMatch{Attr: attr, Link: link, Kind: n.Kind, Value: n.Value.Text}, nil

	case *fql.Matches:
		if !b.searchIndex {
			return nil, positioned(n.Pos, "matches() requires the search-index feature, which is disabled in this deployment")
		}
		return &BoundMatches{Query: n.Query}, nil

	case *fql.Traversal:
		return b.bindTraversal(ctx, n, s)

	default:
		return nil, domainerrors.NewValidation("unsupported query construct")
	}
}

func (b *binder) resolveField(f fql.Field, s *scope) (domainattribute.Snapshot, bool, error) {
	switch f.Scope {
	case fql.ScopeLink:
		if s.linkAttrs == nil {
			return domainattribute.Snapshot{}, false, positioned(f.Pos, "link.%s is only valid inside a relationship traversal", f.Name)
		}
		attr, ok := s.linkAttrs[f.Name]
		if !ok {
			return domainattribute.Snapshot{}, false, positioned(f.Pos, "unknown link attribute %q", f.Name)
		}
		return attr, true, nil
	default:
		attr, ok := s.attrs[f.Name]
		if !ok {
			return domainattribute.Snapshot{}, false, positioned(f.Pos, "unknown attribute %q", f.Name)
		}
		return attr, false, nil
	}
}

func (b *binder) bindCompare(ctx context.Context, n *fql.Compare, s *scope) (BoundNode, error) {
	attr, link, err := b.resolveField(n.Field, s)
	if err != nil {
		return nil, err
	}
	if n.Op == fql.CmpIsa {
		return nil, positioned(n.Pos, "isa applies to the type field only")
	}

	ordered := n.Op == fql.CmpGt || n.Op == fql.CmpGte || n.Op == fql.CmpLt || n.Op == fql.CmpLte

	switch n.Func {
	case fql.FuncLength:
		if !attr.DataType.IsTextual() {
			return nil, positioned(n.Pos, "length() requires a textual attribute; %q is %s", attr.InternalName, attr.DataType)
		}
		v, err := intLiteral(n.Literal)
		if err != nil {
			return nil, err
		}
		return &BoundCompare{Attr: attr, Link: link, Func: n.Func, Op: n.Op, Value: v}, nil

	case fql.FuncCount:
		v, err := intLiteral(n.Literal)
		if err != nil {
			return nil, err
		}
		return &BoundCompare{Attr: attr, Link: link, Func: n.Func, Op: n.Op, Value: v}, nil

	case fql.FuncMin, fql.FuncMax:
		if !attr.DataType.IsOrdered() {
			return nil, positioned(n.Pos, "%s() requires an ordered attribute; %q is %s", n.Func, attr.InternalName, attr.DataType)
		}
	case fql.FuncNone:
		if ordered && !attr.DataType.IsOrdered() {
			return nil, positioned(n.Pos, "%q is %s and does not support ordering comparisons", attr.InternalName, attr.DataType)
		}
	}

	v, err := b.coerceLiteral(ctx, attr, n.Literal)
	if err != nil {
		return nil, err
	}
	return &BoundCompare{Attr: attr, Link: link, Func: n.Func, Op: n.Op, Value: v}, nil
}

func (b *binder) bindIn(ctx context.Context, n *fql.In, s *scope) (BoundNode, error) {
	attr, link, err := b.resolveField(n.Field, s)
	if err != nil {
		return nil, err
	}
	if n.Func != fql.FuncNone {
		return nil, positioned(n.Pos, "in does not combine with %s()", n.Func)
	}
	values := make([]valueobjects.Value, 0, len(n.Values))
	for _, lit := range n.Values {
		v, err := b.coerceLiteral(ctx, attr, lit)
		if err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return &BoundIn{Attr: attr, Link: link, Values: values}, nil
}

func (b *binder) bindRange(ctx context.Context, n *fql.Range, s *scope) (BoundNode, error) {
	attr, link, err := b.resolveField(n.Field, s)
	if err != nil {
		return nil, err
	}
	if n.Func != fql.FuncNone {
		return nil, positioned(n.Pos, "range does not combine with %s()", n.Func)
	}
	if !attr.DataType.IsOrdered() {
		return nil, positioned(n.Pos, "range() requires an ordered attribute; %q is %s", attr.InternalName, attr.DataType)
	}
	lo, err := b.coerceLiteral(ctx, attr, n.Lo)
	if err != nil {
		return nil, err
	}
	hi, err := b.coerceLiteral(ctx, attr, n.Hi)
	if err != nil {
		return nil, err
	}
	return &BoundRange{Attr: attr, Link: link, Lo: lo, Hi: hi}, nil
}

func (b *binder) bindTypeCompare(n *fql.Compare) (BoundNode, error) {
	if n.Func != fql.FuncNone {
		return nil, positioned(n.Pos, "aggregate functions do not apply to the type field")
	}
	t, err := b.lookupType(n.Literal)
	if err != nil {
		return nil, err
	}

	switch n.Op {
	case fql.CmpEq, fql.CmpNeq:
		return &BoundType{TypeIDs: []valueobjects.TypeDefinitionID{t.ID}, Negate: n.Op == fql.CmpNeq}, nil
	case fql.CmpIsa:
		// The type or any of its descendants.
		root, err := b.typeDefs.Get(context.Background(), t.ID)
		if err != nil {
			return nil, err
		}
		descendants, err := apptypedef.Descendants(context.Background(), b.typeDefs, root)
		if err != nil {
			return nil, err
		}
		ids := []valueobjects.TypeDefinitionID{t.ID}
		for _, d := range descendants {
			ids = append(ids, d.ID())
		}
		return &BoundType{TypeIDs: ids}, nil
	default:
		return nil, positioned(n.Pos, "the type field supports =, != and isa")
	}
}

func (b *binder) bindTypeIn(n *fql.In) (BoundNode, error) {
	ids := make([]valueobjects.TypeDefinitionID, 0, len(n.Values))
	for _, lit := range n.Values {
		t, err := b.lookupType(lit)
		if err != nil {
			return nil, err
		}
		ids = append(ids, t.ID)
	}
	return &BoundType{TypeIDs: ids}, nil
}

func (b *binder) lookupType(lit fql.Literal) (domaintypedef.Snapshot, error) {
	t, ok := b.typesByName[lit.Text]
	if !ok {
		return domaintypedef.Snapshot{}, positioned(lit.Pos, "unknown type %q", lit.Text)
	}
	return t, nil
}

func (b *binder) bindTraversal(ctx context.Context, n *fql.Traversal, s *scope) (BoundNode, error) {
	if s.depth+1 > maxTraversalDepth {
		return nil, positioned(n.Pos, "traversals nest at most %d deep", maxTraversalDepth)
	}
	def, ok := s.rels[n.Relationship]
	if !ok {
		return nil, positioned(n.Pos, "unknown relationship %q for this type", n.Relationship)
	}
	if def.Kind == domainrelationship.KindSymmetric && n.Direction != fql.DirAny {
		return nil, positioned(n.Pos,
			"%q is symmetric; traverse it with linked(%s)", n.Relationship, n.Relationship)
	}

	inner, err := b.traversalScope(ctx, n, def, s.depth+1)
	if err != nil {
		return nil, err
	}
	if inner.linkAttrs, err = b.linkAttrsFor(ctx, def); err != nil {
		return nil, err
	}

	boundInner, err := b.bind(ctx, n.Inner, inner)
	if err != nil {
		return nil, err
	}
	return &BoundTraversal{Def: def, Direction: n.Direction, Inner: boundInner}, nil
}

// traversalScope builds the binding scope for a traversal's counterpart.
// child()/parent() scope one endpoint; linked() can land on either end,
// so its scope is the union of both endpoint schemas (on a name clash the
// parent endpoint's attribute wins).
func (b *binder) traversalScope(ctx context.Context, n *fql.Traversal, def domainrelationship.DefinitionSnapshot, depth int) (*scope, error) {
	scopeOf := func(id valueobjects.TypeDefinitionID) (*scope, error) {
		t, err := b.typeDefs.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		s, _, err := b.scopeFor(ctx, t, depth)
		return s, err
	}

	if n.Direction != fql.DirAny {
		counterpartTypeID := def.ChildTypeID
		if n.Direction == fql.DirParent {
			counterpartTypeID = def.ParentTypeID
		}
		return scopeOf(counterpartTypeID)
	}

	merged, err := scopeOf(def.ParentTypeID)
	if err != nil {
		return nil, err
	}
	if def.ChildTypeID.Equals(def.ParentTypeID) {
		return merged, nil
	}
	other, err := scopeOf(def.ChildTypeID)
	if err != nil {
		return nil, err
	}
	for name, a := range other.attrs {
		if _, exists := merged.attrs[name]; !exists {
			merged.attrs[name] = a
		}
	}
	for name, r := range other.rels {
		if _, exists := merged.rels[name]; !exists {
			merged.rels[name] = r
		}
	}
	return merged, nil
}

// coerceLiteral turns a literal into a typed value for the attribute. It
// special-cases quantity attributes, normalizing a unit-suffixed number
// (`weight > 5.5 kg`) to the attribute's base unit; everything else defers to
// the type-directed coerce.
func (b *binder) coerceLiteral(ctx context.Context, attr domainattribute.Snapshot, lit fql.Literal) (valueobjects.Value, error) {
	if attr.DataType == valueobjects.DataTypeQuantity {
		return b.coerceQuantity(ctx, attr, lit)
	}
	if lit.Unit != "" {
		return valueobjects.Value{}, positioned(lit.Pos, "unit suffix %q is only valid on quantity attributes; %q is %s", lit.Unit, attr.InternalName, attr.DataType)
	}
	return coerce(attr.DataType, lit)
}

// coerceQuantity resolves the attribute's unit family and folds a
// `magnitude unit` literal to the base-unit magnitude, so comparisons run in a
// single normalized dimension. The unit suffix is required and must be a
// member of the family.
func (b *binder) coerceQuantity(ctx context.Context, attr domainattribute.Snapshot, lit fql.Literal) (valueobjects.Value, error) {
	if lit.Kind != fql.LitNumber {
		return valueobjects.Value{}, positioned(lit.Pos, "quantity comparison expects a number with a unit, got %q", lit.Text)
	}
	if lit.Unit == "" {
		return valueobjects.Value{}, positioned(lit.Pos, "quantity comparison requires a unit, e.g. %q", lit.Text+" "+b.baseUnitHint(ctx, attr))
	}
	if b.units == nil {
		return valueobjects.Value{}, positioned(lit.Pos, "unit families are not configured in this deployment")
	}
	if attr.UnitFamilyID == "" {
		return valueobjects.Value{}, positioned(lit.Pos, "quantity attribute %q has no unit family", attr.InternalName)
	}
	famID, err := ulid.Parse(attr.UnitFamilyID)
	if err != nil {
		return valueobjects.Value{}, positioned(lit.Pos, "invalid unit family: %s", err.Error())
	}
	family, err := b.units.Get(ctx, b.tenant, famID)
	if err != nil {
		return valueobjects.Value{}, err
	}
	base, err := family.ToBase(lit.Text, lit.Unit)
	if err != nil {
		if verr, ok := err.(*domainerrors.Error); ok {
			return valueobjects.Value{}, positioned(lit.Pos, "%s", verr.Message)
		}
		return valueobjects.Value{}, err
	}
	return valueobjects.NewQuantityValue(lit.Text, lit.Unit, base)
}

// baseUnitHint returns the family's base unit for an error message, or a
// generic placeholder when it cannot be resolved.
func (b *binder) baseUnitHint(ctx context.Context, attr domainattribute.Snapshot) string {
	if b.units == nil || attr.UnitFamilyID == "" {
		return "<unit>"
	}
	famID, err := ulid.Parse(attr.UnitFamilyID)
	if err != nil {
		return "<unit>"
	}
	family, err := b.units.Get(ctx, b.tenant, famID)
	if err != nil || family.BaseUnit == "" {
		return "<unit>"
	}
	return family.BaseUnit
}

// coerce turns a literal into a typed value via the attribute's data type.
func coerce(dt valueobjects.DataType, lit fql.Literal) (valueobjects.Value, error) {
	var raw json.RawMessage
	switch lit.Kind {
	case fql.LitNumber:
		raw = json.RawMessage(lit.Text)
	case fql.LitBool:
		raw = json.RawMessage(lit.Text)
	default:
		// Strings and bare identifiers (enum members) quote as JSON strings;
		// decimals accept the string form.
		raw, _ = json.Marshal(lit.Text)
	}
	v, err := valueobjects.ParseValue(dt, raw)
	if err != nil {
		return valueobjects.Value{}, positioned(lit.Pos, "value %q is not a valid %s: %s", lit.Text, dt, err.Error())
	}
	return v, nil
}

func intLiteral(lit fql.Literal) (valueobjects.Value, error) {
	n, err := strconv.ParseInt(lit.Text, 10, 64)
	if err != nil {
		return valueobjects.Value{}, positioned(lit.Pos, "expected a whole number, got %q", lit.Text)
	}
	return valueobjects.NewIntegerValue(n), nil
}

// positioned wraps a schema-binding failure as a validation error carrying
// the query position, so the console can underline it like a parse error.
func positioned(pos int, format string, args ...any) error {
	return domainerrors.NewValidation(fmt.Sprintf(format, args...), "position", pos)
}
