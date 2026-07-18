// Package formula evaluates small arithmetic expressions over named inputs —
// the scalar half of computed attributes. Grammar (precedence climbing):
//
//	expr   = term (('+' | '-') term)*
//	term   = factor (('*' | '/') factor)*
//	factor = number | ident | '(' expr ')' | '-' factor
//
// Identifiers are attribute internal names; Refs lists them so definitions
// can be cycle-checked before use.
package formula

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Expr is a parsed, ready-to-evaluate formula.
type Expr struct {
	root node
	refs []string
}

// Refs returns the distinct identifiers the formula reads, in first-seen
// order.
func (e *Expr) Refs() []string { return e.refs }

// Eval computes the formula against the given inputs. A referenced name with
// no input, or a division by zero, makes the result undefined (ok=false).
func (e *Expr) Eval(vars map[string]float64) (result float64, ok bool) {
	return e.root.eval(vars)
}

type node interface {
	eval(vars map[string]float64) (float64, bool)
}

type numNode float64

func (n numNode) eval(map[string]float64) (float64, bool) { return float64(n), true }

type refNode string

func (r refNode) eval(vars map[string]float64) (float64, bool) {
	v, ok := vars[string(r)]
	return v, ok
}

type binNode struct {
	op          byte
	left, right node
}

func (b binNode) eval(vars map[string]float64) (float64, bool) {
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

type negNode struct{ inner node }

func (n negNode) eval(vars map[string]float64) (float64, bool) {
	v, ok := n.inner.eval(vars)
	return -v, ok
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
	return &Expr{root: root, refs: p.refs}, nil
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
	src  string
	pos  int
	tok  token
	refs []string
	seen map[string]bool
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
		p.next()
		return numNode(f), nil
	case tokIdent:
		name := p.tok.text
		if p.seen == nil {
			p.seen = map[string]bool{}
		}
		if !p.seen[name] {
			p.seen[name] = true
			p.refs = append(p.refs, name)
		}
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
