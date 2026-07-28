// Package formula evaluates small arithmetic expressions over named inputs —
// the computation half of computed attributes. Grammar (precedence climbing):
//
//	expr   = term (('+' | '-') term)*
//	term   = factor (('*' | '/') factor)*
//	factor = number | call | ident | '(' expr ')' | '-' factor
//	call   = ('sum' | 'count' | 'avg' | 'min' | 'max') '(' ident ')'
//
// Identifiers are attribute internal names; Refs lists them so definitions
// can be cycle-checked before use.
//
// A NAME CARRIES ALL OF AN ENTITY'S VALUES FOR IT, not one. A bare identifier
// requires exactly one — reading a multi-valued attribute bare would mean
// picking a member, and picking one silently is what an aggregate exists to
// replace. The aggregate calls read the whole list, so "sum(line_totals)" says
// what it means and "line_totals * 2" is refused at definition time.
package formula

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode"
)

// Expr is a parsed, ready-to-evaluate formula.
type Expr struct {
	root       node
	refs       []string
	scalarRefs []string
}

// Inputs is an entity's values by attribute internal name: EVERY value for
// that name, in repository order. A single-valued attribute has one.
type Inputs map[string][]float64

// RatInputs is Inputs in exact rational arithmetic, for decimal targets.
type RatInputs map[string][]*big.Rat

// Refs returns the distinct identifiers the formula reads, in first-seen
// order — aggregated and bare alike, which is what a cycle check needs.
func (e *Expr) Refs() []string { return e.refs }

// ScalarRefs returns the identifiers read BARE, outside any aggregate.
//
// Those are the ones that must resolve to a single value, so they are the
// ones a multi-valued attribute cannot be. A name read only inside sum() or
// count() is not here, which is what lets a formula aggregate an attribute it
// may not read directly.
func (e *Expr) ScalarRefs() []string { return e.scalarRefs }

// Eval computes the formula against the given inputs. A referenced name with
// no input, a bare name with more than one, or a division by zero, makes the
// result undefined (ok=false).
func (e *Expr) Eval(vars Inputs) (result float64, ok bool) {
	return e.root.eval(vars)
}

// EvalRat computes the formula in exact rational arithmetic.
//
// It exists for decimal attributes. Evaluating those in binary float64
// materialized artifacts — `0.1 + 0.2` stored as `0.30000000000000004` — which
// then failed exact equality in FQL and appeared verbatim in exports. Choosing
// `decimal` is how a schema author asks for exactness, and computed values
// were the one place the system produced decimals rather than accepting them.
//
// Rationals stay exact through +, -, * and /. A value that has no finite
// decimal expansion (1/3) is exact here and is rounded only when the caller
// renders it.
func (e *Expr) EvalRat(vars RatInputs) (result *big.Rat, ok bool) {
	return e.root.evalRat(vars)
}

type node interface {
	eval(vars Inputs) (float64, bool)
	evalRat(vars RatInputs) (*big.Rat, bool)
}

// numNode keeps the literal's source text alongside its float form, so exact
// evaluation reads the digits the author wrote rather than a float
// round-trip of them.
type numNode struct {
	f    float64
	text string
}

func (n numNode) eval(Inputs) (float64, bool) { return n.f, true }

func (n numNode) evalRat(RatInputs) (*big.Rat, bool) {
	r, ok := new(big.Rat).SetString(n.text)
	return r, ok
}

// refNode reads a name that must hold exactly one value.
//
// Zero is the missing input it always was. MORE than one is also undefined:
// the old evaluator took whichever member the repository returned last, so
// adding a member changed the answer with nothing to explain it. Definitions
// are refused at write time, so this is the defensive half of the same rule.
type refNode string

func (r refNode) eval(vars Inputs) (float64, bool) {
	v, ok := vars[string(r)]
	if !ok || len(v) != 1 {
		return 0, false
	}
	return v[0], true
}

func (r refNode) evalRat(vars RatInputs) (*big.Rat, bool) {
	v, ok := vars[string(r)]
	if !ok || len(v) != 1 || v[0] == nil {
		return nil, false
	}
	return new(big.Rat).Set(v[0]), true
}

// Aggregate is a function that folds all of a name's values into one.
type Aggregate string

// The supported aggregates.
const (
	AggSum   Aggregate = "sum"
	AggCount Aggregate = "count"
	AggAvg   Aggregate = "avg"
	AggMin   Aggregate = "min"
	AggMax   Aggregate = "max"
)

// aggregates is the lookup the parser uses; a name outside it is an ordinary
// identifier, so an attribute called "total" is unaffected.
var aggregates = map[string]Aggregate{
	string(AggSum): AggSum, string(AggCount): AggCount, string(AggAvg): AggAvg,
	string(AggMin): AggMin, string(AggMax): AggMax,
}

// aggNode folds every value a name holds.
//
// count of an ABSENT name is 0 — counting nothing is zero, and there is no
// other reading. sum, avg, min and max of an absent name are UNDEFINED, not
// zero: the entity holds no data for that attribute, so the total is unknown
// rather than nought, and clearing the computed value says so. The asymmetry
// is deliberate and is documented for schema authors.
type aggNode struct {
	fn   Aggregate
	name string
}

func (a aggNode) eval(vars Inputs) (float64, bool) {
	vals := vars[a.name]
	if a.fn == AggCount {
		return float64(len(vals)), true
	}
	if len(vals) == 0 {
		return 0, false
	}
	switch a.fn {
	case AggSum, AggAvg:
		total := 0.0
		for _, v := range vals {
			total += v
		}
		if a.fn == AggAvg {
			return total / float64(len(vals)), true
		}
		return total, true
	case AggMin, AggMax:
		best := vals[0]
		for _, v := range vals[1:] {
			if (a.fn == AggMin && v < best) || (a.fn == AggMax && v > best) {
				best = v
			}
		}
		return best, true
	}
	return 0, false
}

func (a aggNode) evalRat(vars RatInputs) (*big.Rat, bool) {
	vals := vars[a.name]
	if a.fn == AggCount {
		return new(big.Rat).SetInt64(int64(len(vals))), true
	}
	if len(vals) == 0 {
		return nil, false
	}
	for _, v := range vals {
		if v == nil {
			return nil, false
		}
	}
	switch a.fn {
	case AggSum, AggAvg:
		total := new(big.Rat)
		for _, v := range vals {
			total.Add(total, v)
		}
		if a.fn == AggAvg {
			return total.Quo(total, new(big.Rat).SetInt64(int64(len(vals)))), true
		}
		return total, true
	case AggMin, AggMax:
		best := new(big.Rat).Set(vals[0])
		for _, v := range vals[1:] {
			if (a.fn == AggMin && v.Cmp(best) < 0) || (a.fn == AggMax && v.Cmp(best) > 0) {
				best.Set(v)
			}
		}
		return best, true
	}
	return nil, false
}

type binNode struct {
	op          byte
	left, right node
}

func (b binNode) eval(vars Inputs) (float64, bool) {
	l, ok := b.left.eval(vars)
	if !ok {
		return 0, false
	}
	r, ok := b.right.eval(vars)
	if !ok {
		return 0, false
	}
	switch b.op {
	case '+':
		return l + r, true
	case '-':
		return l - r, true
	case '*':
		return l * r, true
	case '/':
		if r == 0 {
			return 0, false
		}
		return l / r, true
	}
	return 0, false
}

func (b binNode) evalRat(vars RatInputs) (*big.Rat, bool) {
	l, ok := b.left.evalRat(vars)
	if !ok {
		return nil, false
	}
	r, ok := b.right.evalRat(vars)
	if !ok {
		return nil, false
	}
	out := new(big.Rat)
	switch b.op {
	case '+':
		return out.Add(l, r), true
	case '-':
		return out.Sub(l, r), true
	case '*':
		return out.Mul(l, r), true
	case '/':
		if r.Sign() == 0 {
			return nil, false
		}
		return out.Quo(l, r), true
	}
	return nil, false
}

type negNode struct{ inner node }

func (n negNode) eval(vars Inputs) (float64, bool) {
	v, ok := n.inner.eval(vars)
	return -v, ok
}

func (n negNode) evalRat(vars RatInputs) (*big.Rat, bool) {
	v, ok := n.inner.evalRat(vars)
	if !ok {
		return nil, false
	}
	return v.Neg(v), true
}

// Parse compiles a formula, returning it and a validation error for malformed
// input.
func Parse(src string) (*Expr, error) {
	p := &parser{src: src}
	p.next()
	root, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.tok.kind != tokEOF {
		return nil, fmt.Errorf("unexpected %q in formula", p.tok.text)
	}
	if root == nil {
		return nil, fmt.Errorf("empty formula")
	}
	return &Expr{root: root, refs: p.refs, scalarRefs: p.scalarRefs}, nil
}

type tokKind int

const (
	tokEOF tokKind = iota
	tokNum
	tokIdent
	tokOp
	tokLParen
	tokRParen
	// tokInvalid marks a character the lexer does not recognise. It must be a
	// DISTINCT kind, not tokEOF: Parse terminates on tokEOF, so reusing tokEOF
	// here made an unknown character look like a clean end of input and
	// silently truncated the formula ("price # qty" parsed as "price").
	tokInvalid
)

type token struct {
	kind tokKind
	text string
}

type parser struct {
	src        string
	pos        int
	tok        token
	refs       []string
	scalarRefs []string
	seen       map[string]bool
	seenScalar map[string]bool
}

func (p *parser) next() {
	for p.pos < len(p.src) && unicode.IsSpace(rune(p.src[p.pos])) {
		p.pos++
	}
	if p.pos >= len(p.src) {
		p.tok = token{kind: tokEOF}
		return
	}
	c := p.src[p.pos]
	switch {
	case c == '(':
		p.pos++
		p.tok = token{kind: tokLParen, text: "("}
	case c == ')':
		p.pos++
		p.tok = token{kind: tokRParen, text: ")"}
	case strings.IndexByte("+-*/", c) >= 0:
		p.pos++
		p.tok = token{kind: tokOp, text: string(c)}
	case unicode.IsDigit(rune(c)) || c == '.':
		start := p.pos
		for p.pos < len(p.src) && (unicode.IsDigit(rune(p.src[p.pos])) || p.src[p.pos] == '.') {
			p.pos++
		}
		p.tok = token{kind: tokNum, text: p.src[start:p.pos]}
	case unicode.IsLetter(rune(c)) || c == '_':
		start := p.pos
		for p.pos < len(p.src) && (unicode.IsLetter(rune(p.src[p.pos])) || unicode.IsDigit(rune(p.src[p.pos])) || p.src[p.pos] == '_') {
			p.pos++
		}
		p.tok = token{kind: tokIdent, text: p.src[start:p.pos]}
	default:
		p.pos++
		p.tok = token{kind: tokInvalid, text: string(c)} // rejected by Parse/parseFactor
	}
}

// parseCall parses `agg '(' ident ')'`. The argument is a single name: an
// aggregate folds one attribute's values, and allowing an expression there
// would raise the question of what it means to fold a computation over
// members that may not correspond.
func (p *parser) parseCall(fn Aggregate) (node, error) {
	p.next()
	if p.tok.kind != tokLParen {
		return nil, fmt.Errorf("%s expects '(' followed by an attribute name", fn)
	}
	p.next()
	if p.tok.kind != tokIdent {
		return nil, fmt.Errorf("%s expects a single attribute name", fn)
	}
	name := p.tok.text
	if _, isAgg := aggregates[name]; isAgg {
		return nil, fmt.Errorf("%s cannot aggregate another aggregate", fn)
	}
	p.recordRef(name, false)
	p.next()
	if p.tok.kind != tokRParen {
		return nil, fmt.Errorf("expected ')' after %s(%s", fn, name)
	}
	p.next()
	return aggNode{fn: fn, name: name}, nil
}

// recordRef notes a referenced name, and whether it was read BARE — which is
// what decides whether the attribute may be multi-valued.
func (p *parser) recordRef(name string, bare bool) {
	if p.seen == nil {
		p.seen = map[string]bool{}
	}
	if !p.seen[name] {
		p.seen[name] = true
		p.refs = append(p.refs, name)
	}
	if !bare {
		return
	}
	if p.seenScalar == nil {
		p.seenScalar = map[string]bool{}
	}
	if !p.seenScalar[name] {
		p.seenScalar[name] = true
		p.scalarRefs = append(p.scalarRefs, name)
	}
}

func (p *parser) parseExpr() (node, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for p.tok.kind == tokOp && (p.tok.text == "+" || p.tok.text == "-") {
		op := p.tok.text[0]
		p.next()
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		left = binNode{op: op, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseTerm() (node, error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for p.tok.kind == tokOp && (p.tok.text == "*" || p.tok.text == "/") {
		op := p.tok.text[0]
		p.next()
		right, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		left = binNode{op: op, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseFactor() (node, error) {
	switch p.tok.kind {
	case tokOp:
		if p.tok.text == "-" {
			p.next()
			inner, err := p.parseFactor()
			if err != nil {
				return nil, err
			}
			return negNode{inner: inner}, nil
		}
		return nil, fmt.Errorf("unexpected operator %q", p.tok.text)
	case tokNum:
		f, err := strconv.ParseFloat(p.tok.text, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", p.tok.text)
		}
		text := p.tok.text
		p.next()
		return numNode{f: f, text: text}, nil
	case tokIdent:
		name := p.tok.text
		if fn, isAgg := aggregates[name]; isAgg {
			return p.parseCall(fn)
		}
		p.recordRef(name, true)
		p.next()
		return refNode(name), nil
	case tokLParen:
		p.next()
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.tok.kind != tokRParen {
			return nil, fmt.Errorf("expected ')'")
		}
		p.next()
		return inner, nil
	default:
		return nil, fmt.Errorf("unexpected %q in formula", p.tok.text)
	}
}
