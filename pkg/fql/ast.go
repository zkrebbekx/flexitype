package fql

// Node is any AST node. Every node carries the byte position it started
// at, so later phases (binding, compilation) report errors against the
// original query text.
type Node interface {
	Position() int
}

// LogicalOp joins expressions.
type LogicalOp string

// The logical joiners.
const (
	OpAnd LogicalOp = "and"
	OpOr  LogicalOp = "or"
)

// Logical is an and/or over two or more expressions.
type Logical struct {
	Op    LogicalOp
	Exprs []Node
	Pos   int
}

// Not negates an expression.
type Not struct {
	Expr Node
	Pos  int
}

// CompareOp is a comparison operator.
type CompareOp string

// The comparison operators.
const (
	CmpEq  CompareOp = "="
	CmpNeq CompareOp = "!="
	CmpGt  CompareOp = ">"
	CmpGte CompareOp = ">="
	CmpLt  CompareOp = "<"
	CmpLte CompareOp = "<="
	CmpIsa CompareOp = "isa" // type field only: type or any descendant
)

// AggFunc wraps an operand field.
type AggFunc string

// The aggregate functions usable on the left of a comparison.
const (
	FuncNone   AggFunc = ""
	FuncMin    AggFunc = "min"
	FuncMax    AggFunc = "max"
	FuncCount  AggFunc = "count"
	FuncLength AggFunc = "length"
)

// FieldScope says where a field name resolves.
type FieldScope string

const (
	// ScopeEntity resolves against the current entity's effective schema.
	ScopeEntity FieldScope = "entity"
	// ScopeLink resolves against the enclosing traversal's relationship
	// attribute set ("link.x").
	ScopeLink FieldScope = "link"
	// ScopeType is the virtual "type" field.
	ScopeType FieldScope = "type"
)

// Field references an attribute (or the virtual type field).
type Field struct {
	Scope FieldScope
	Name  string
	Pos   int
}

// LiteralKind classifies literal values; the binder coerces them by the
// attribute's data type.
type LiteralKind int

// The literal kinds.
const (
	LitString LiteralKind = iota
	LitNumber
	LitBool
	LitIdent // bare identifiers: enum members, type names
)

// Literal is one literal operand.
type Literal struct {
	Kind LiteralKind
	Text string
	Pos  int
}

// Compare is `operand op literal`, optionally through an aggregate
// function: min(price) >= 500.
type Compare struct {
	Func    AggFunc
	Field   Field
	Op      CompareOp
	Literal Literal
	Pos     int
}

// In is `operand in (v1, v2, ...)`.
type In struct {
	Func   AggFunc
	Field  Field
	Values []Literal
	Pos    int
}

// Range is `range(operand, lo, hi)` — inclusive on both ends.
type Range struct {
	Func  AggFunc
	Field Field
	Lo    Literal
	Hi    Literal
	Pos   int
}

// Has is `has(field)` — the entity holds at least one live value.
type Has struct {
	Field Field
	Pos   int
}

// StringMatchKind is a textual predicate.
type StringMatchKind string

// The textual predicates.
const (
	MatchContains  StringMatchKind = "contains"
	MatchIContains StringMatchKind = "icontains"
	MatchIEquals   StringMatchKind = "iequals"
)

// StringMatch is contains(field, "x") / icontains / iequals.
type StringMatch struct {
	Kind  StringMatchKind
	Field Field
	Value Literal
	Pos   int
}

// Direction of a relationship traversal, from the current entity's side.
type Direction string

const (
	// DirChild — the current entity is the parent; the inner expression
	// evaluates against child-side counterparts.
	DirChild Direction = "child"
	// DirParent — the current entity is the child; the inner expression
	// evaluates against parent-side counterparts.
	DirParent Direction = "parent"
)

// Traversal crosses a relationship: child(rel) { expr }.
type Traversal struct {
	Direction    Direction
	Relationship string
	Inner        Node
	Pos          int
}

// Position implements Node.
func (n *Logical) Position() int { return n.Pos }

// Position implements Node.
func (n *Not) Position() int { return n.Pos }

// Position implements Node.
func (n *Compare) Position() int { return n.Pos }

// Position implements Node.
func (n *In) Position() int { return n.Pos }

// Position implements Node.
func (n *Range) Position() int { return n.Pos }

// Position implements Node.
func (n *Has) Position() int { return n.Pos }

// Position implements Node.
func (n *StringMatch) Position() int { return n.Pos }

// Position implements Node.
func (n *Traversal) Position() int { return n.Pos }
