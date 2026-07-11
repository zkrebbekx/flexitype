package fql

import "strings"

// Parse turns query text into an AST. Errors carry byte positions.
func Parse(input string) (Node, error) {
	if strings.TrimSpace(input) == "" {
		return nil, errorf(0, "empty query")
	}
	tokens, err := lex(input)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	node, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind != TokenEOF {
		return nil, errorf(p.peek().Pos, "unexpected %q", p.peek().Text)
	}
	return node, nil
}

type parser struct {
	tokens []Token
	pos    int
}

func (p *parser) peek() Token { return p.tokens[p.pos] }

func (p *parser) next() Token {
	t := p.tokens[p.pos]
	if t.Kind != TokenEOF {
		p.pos++
	}
	return t
}

// keyword reports whether the current token is the given case-insensitive
// bare word.
func (p *parser) keyword(word string) bool {
	t := p.peek()
	return t.Kind == TokenIdent && strings.EqualFold(t.Text, word)
}

func (p *parser) expect(kind TokenKind, what string) (Token, error) {
	t := p.peek()
	if t.Kind != kind {
		return t, errorf(t.Pos, "expected %s, got %q", what, displayToken(t))
	}
	return p.next(), nil
}

func displayToken(t Token) string {
	if t.Kind == TokenEOF {
		return "end of query"
	}
	return t.Text
}

func (p *parser) parseOr() (Node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	exprs := []Node{left}
	for p.keyword("or") {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, right)
	}
	if len(exprs) == 1 {
		return left, nil
	}
	return &Logical{Op: OpOr, Exprs: exprs, Pos: left.Position()}, nil
}

func (p *parser) parseAnd() (Node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	exprs := []Node{left}
	for p.keyword("and") {
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, right)
	}
	if len(exprs) == 1 {
		return left, nil
	}
	return &Logical{Op: OpAnd, Exprs: exprs, Pos: left.Position()}, nil
}

func (p *parser) parseUnary() (Node, error) {
	if p.keyword("not") {
		t := p.next()
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Not{Expr: inner, Pos: t.Pos}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Node, error) {
	t := p.peek()

	if t.Kind == TokenLParen {
		p.next()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokenRParen, "')'"); err != nil {
			return nil, err
		}
		return inner, nil
	}

	if t.Kind != TokenIdent {
		return nil, errorf(t.Pos, "expected a condition, got %q", displayToken(t))
	}

	switch strings.ToLower(t.Text) {
	case "child", "parent":
		return p.parseTraversal()
	case "range":
		return p.parseRange()
	case "has":
		return p.parseHas()
	case "contains", "icontains", "iequals":
		return p.parseStringMatch()
	}
	return p.parseComparisonOrIn()
}

// parseTraversal: (child|parent) "(" ident ")" "{" expr "}"
func (p *parser) parseTraversal() (Node, error) {
	kw := p.next()
	direction := Direction(strings.ToLower(kw.Text))

	if _, err := p.expect(TokenLParen, "'(' after "+string(direction)); err != nil {
		return nil, err
	}
	rel, err := p.expect(TokenIdent, "a relationship name")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenRParen, "')'"); err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenLBrace, "'{' opening the traversal expression"); err != nil {
		return nil, err
	}
	inner, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenRBrace, "'}'"); err != nil {
		return nil, err
	}
	return &Traversal{Direction: direction, Relationship: rel.Text, Inner: inner, Pos: kw.Pos}, nil
}

// parseRange: range "(" operand "," literal "," literal ")"
func (p *parser) parseRange() (Node, error) {
	kw := p.next()
	if _, err := p.expect(TokenLParen, "'(' after range"); err != nil {
		return nil, err
	}
	fn, field, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenComma, "','"); err != nil {
		return nil, err
	}
	lo, err := p.parseLiteral()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenComma, "','"); err != nil {
		return nil, err
	}
	hi, err := p.parseLiteral()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenRParen, "')'"); err != nil {
		return nil, err
	}
	return &Range{Func: fn, Field: field, Lo: lo, Hi: hi, Pos: kw.Pos}, nil
}

// parseHas: has "(" field ")"
func (p *parser) parseHas() (Node, error) {
	kw := p.next()
	if _, err := p.expect(TokenLParen, "'(' after has"); err != nil {
		return nil, err
	}
	field, err := p.parseField()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenRParen, "')'"); err != nil {
		return nil, err
	}
	return &Has{Field: field, Pos: kw.Pos}, nil
}

// parseStringMatch: contains "(" field "," literal ")"
func (p *parser) parseStringMatch() (Node, error) {
	kw := p.next()
	kind := StringMatchKind(strings.ToLower(kw.Text))

	if _, err := p.expect(TokenLParen, "'(' after "+string(kind)); err != nil {
		return nil, err
	}
	field, err := p.parseField()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenComma, "','"); err != nil {
		return nil, err
	}
	value, err := p.parseLiteral()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenRParen, "')'"); err != nil {
		return nil, err
	}
	return &StringMatch{Kind: kind, Field: field, Value: value, Pos: kw.Pos}, nil
}

// parseComparisonOrIn: operand (op literal | in "(" literals ")")
func (p *parser) parseComparisonOrIn() (Node, error) {
	start := p.peek()
	fn, field, err := p.parseOperand()
	if err != nil {
		return nil, err
	}

	if p.keyword("in") {
		p.next()
		if _, err := p.expect(TokenLParen, "'(' after in"); err != nil {
			return nil, err
		}
		var values []Literal
		for {
			lit, err := p.parseLiteral()
			if err != nil {
				return nil, err
			}
			values = append(values, lit)
			if p.peek().Kind == TokenComma {
				p.next()
				continue
			}
			break
		}
		if _, err := p.expect(TokenRParen, "')'"); err != nil {
			return nil, err
		}
		return &In{Func: fn, Field: field, Values: values, Pos: start.Pos}, nil
	}

	op, err := p.parseCompareOp()
	if err != nil {
		return nil, err
	}
	lit, err := p.parseLiteral()
	if err != nil {
		return nil, err
	}
	return &Compare{Func: fn, Field: field, Op: op, Literal: lit, Pos: start.Pos}, nil
}

// parseOperand: [func "("] field [")"]
func (p *parser) parseOperand() (AggFunc, Field, error) {
	t := p.peek()
	if t.Kind == TokenIdent {
		switch strings.ToLower(t.Text) {
		case "min", "max", "count", "length":
			// Function form only when followed by '('.
			if p.tokens[p.pos+1].Kind == TokenLParen {
				fn := AggFunc(strings.ToLower(t.Text))
				p.next()
				p.next() // '('
				field, err := p.parseField()
				if err != nil {
					return FuncNone, Field{}, err
				}
				if _, err := p.expect(TokenRParen, "')'"); err != nil {
					return FuncNone, Field{}, err
				}
				return fn, field, nil
			}
		}
	}
	field, err := p.parseField()
	return FuncNone, field, err
}

// parseField: ident | "link" "." ident | "type"
func (p *parser) parseField() (Field, error) {
	t, err := p.expect(TokenIdent, "an attribute name")
	if err != nil {
		return Field{}, err
	}
	name := t.Text

	if strings.EqualFold(name, "type") {
		return Field{Scope: ScopeType, Name: "type", Pos: t.Pos}, nil
	}
	if strings.EqualFold(name, "link") && p.peek().Kind == TokenDot {
		p.next()
		attr, err := p.expect(TokenIdent, "a link attribute name")
		if err != nil {
			return Field{}, err
		}
		return Field{Scope: ScopeLink, Name: attr.Text, Pos: t.Pos}, nil
	}
	return Field{Scope: ScopeEntity, Name: name, Pos: t.Pos}, nil
}

// wordOps maps word aliases onto symbolic comparison operators.
var wordOps = map[string]CompareOp{
	"eq": CmpEq, "neq": CmpNeq,
	"gt": CmpGt, "gte": CmpGte,
	"lt": CmpLt, "lte": CmpLte,
	"isa": CmpIsa,
}

func (p *parser) parseCompareOp() (CompareOp, error) {
	t := p.peek()
	if t.Kind == TokenOp {
		p.next()
		return CompareOp(t.Text), nil
	}
	if t.Kind == TokenIdent {
		if op, ok := wordOps[strings.ToLower(t.Text)]; ok {
			p.next()
			return op, nil
		}
	}
	return "", errorf(t.Pos, "expected a comparison operator, got %q", displayToken(t))
}

func (p *parser) parseLiteral() (Literal, error) {
	t := p.peek()
	switch t.Kind {
	case TokenString:
		p.next()
		return Literal{Kind: LitString, Text: t.Text, Pos: t.Pos}, nil
	case TokenNumber:
		p.next()
		return Literal{Kind: LitNumber, Text: t.Text, Pos: t.Pos}, nil
	case TokenIdent:
		p.next()
		if strings.EqualFold(t.Text, "true") || strings.EqualFold(t.Text, "false") {
			return Literal{Kind: LitBool, Text: strings.ToLower(t.Text), Pos: t.Pos}, nil
		}
		return Literal{Kind: LitIdent, Text: t.Text, Pos: t.Pos}, nil
	default:
		return Literal{}, errorf(t.Pos, "expected a value, got %q", displayToken(t))
	}
}
