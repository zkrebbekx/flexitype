// Package query executes FQL: it binds parsed queries against the schema
// (attributes, relationships, type hierarchies) and hands the bound tree
// to the persistence layer for compilation. Names never reach SQL — only
// resolved definitions and typed values do.
package query

import (
	"github.com/zkrebbekx/flexitype/domain/attribute"
	"github.com/zkrebbekx/flexitype/domain/relationship"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/fql"
)

// BoundNode is a schema-resolved query node ready for compilation.
type BoundNode interface {
	boundNode()
}

// BoundLogical joins bound expressions with and/or.
type BoundLogical struct {
	Op    fql.LogicalOp
	Exprs []BoundNode
}

// BoundNot negates a bound expression.
type BoundNot struct {
	Expr BoundNode
}

// BoundCompare is a comparison against one attribute, optionally through
// an aggregate. For length and count the value is an integer regardless of
// the attribute's type.
type BoundCompare struct {
	Attr  attribute.Snapshot
	Link  bool // the attribute lives on the enclosing traversal's link
	Func  fql.AggFunc
	Op    fql.CompareOp
	Value valueobjects.Value
}

// BoundIn is set membership over one attribute.
type BoundIn struct {
	Attr   attribute.Snapshot
	Link   bool
	Values []valueobjects.Value
}

// BoundRange is an inclusive between over one ordered attribute.
type BoundRange struct {
	Attr attribute.Snapshot
	Link bool
	Lo   valueobjects.Value
	Hi   valueobjects.Value
}

// BoundHas asserts the entity holds at least one live value.
type BoundHas struct {
	Attr attribute.Snapshot
	Link bool
}

// BoundStringMatch is a textual predicate over one attribute.
type BoundStringMatch struct {
	Attr  attribute.Snapshot
	Link  bool
	Kind  fql.StringMatchKind
	Value string
}

// BoundType constrains the entity's declared type. Negate inverts the
// membership (for !=).
type BoundType struct {
	TypeIDs []valueobjects.TypeDefinitionID
	Negate  bool
}

// BoundMatches is full-text search over the entity's search document.
type BoundMatches struct {
	Query string
}

// BoundTraversal crosses a relationship; Inner evaluates against the
// counterpart entity (and the link's own attributes).
type BoundTraversal struct {
	Def       relationship.DefinitionSnapshot
	Direction fql.Direction
	Inner     BoundNode
}

func (*BoundLogical) boundNode()     {}
func (*BoundNot) boundNode()         {}
func (*BoundCompare) boundNode()     {}
func (*BoundIn) boundNode()          {}
func (*BoundRange) boundNode()       {}
func (*BoundHas) boundNode()         {}
func (*BoundStringMatch) boundNode() {}
func (*BoundType) boundNode()        {}
func (*BoundTraversal) boundNode()   {}
func (*BoundMatches) boundNode()     {}
