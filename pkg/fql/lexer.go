package fql

import (
	"strings"
	"unicode"
)

// lex tokenises the input. Strings take double or single quotes with
// backslash escapes; identifiers are [a-zA-Z_][a-zA-Z0-9_]*.
func lex(input string) ([]Token, error) {
	var tokens []Token
	i := 0
	for i < len(input) {
		c := rune(input[i])

		switch {
		case unicode.IsSpace(c):
			i++

		case c == '(':
			tokens = append(tokens, Token{TokenLParen, "(", i})
			i++
		case c == ')':
			tokens = append(tokens, Token{TokenRParen, ")", i})
			i++
		case c == '{':
			tokens = append(tokens, Token{TokenLBrace, "{", i})
			i++
		case c == '}':
			tokens = append(tokens, Token{TokenRBrace, "}", i})
			i++
		case c == ',':
			tokens = append(tokens, Token{TokenComma, ",", i})
			i++
		case c == '.':
			tokens = append(tokens, Token{TokenDot, ".", i})
			i++

		case c == '=':
			tokens = append(tokens, Token{TokenOp, "=", i})
			i++
		case c == '!':
			if i+1 < len(input) && input[i+1] == '=' {
				tokens = append(tokens, Token{TokenOp, "!=", i})
				i += 2
			} else {
				return nil, errorf(i, "unexpected '!' (did you mean '!=')")
			}
		case c == '>' || c == '<':
			op := string(c)
			if i+1 < len(input) && input[i+1] == '=' {
				op += "="
			}
			tokens = append(tokens, Token{TokenOp, op, i})
			i += len(op)

		case c == '"' || c == '\'':
			start := i
			quote := byte(c)
			i++
			var sb strings.Builder
			closed := false
			for i < len(input) {
				if input[i] == '\\' && i+1 < len(input) {
					sb.WriteByte(input[i+1])
					i += 2
					continue
				}
				if input[i] == quote {
					closed = true
					i++
					break
				}
				sb.WriteByte(input[i])
				i++
			}
			if !closed {
				return nil, errorf(start, "unterminated string")
			}
			tokens = append(tokens, Token{TokenString, sb.String(), start})

		case unicode.IsDigit(c) || (c == '-' && i+1 < len(input) && unicode.IsDigit(rune(input[i+1]))):
			start := i
			i++
			for i < len(input) && unicode.IsDigit(rune(input[i])) {
				i++
			}
			// An optional single fractional part: exactly one dot, then at
			// least one digit. Rejects a trailing dot (1.) and empty
			// fraction (1..2) at the offending position.
			if i < len(input) && input[i] == '.' {
				i++
				if i >= len(input) || !unicode.IsDigit(rune(input[i])) {
					return nil, errorf(i, "malformed number: a decimal point must be followed by a digit")
				}
				for i < len(input) && unicode.IsDigit(rune(input[i])) {
					i++
				}
			}
			// A further dot means a malformed literal like 1.2.3 — flag it
			// rather than silently splitting into two tokens.
			if i < len(input) && input[i] == '.' {
				return nil, errorf(i, "malformed number: only one decimal point is allowed")
			}
			tokens = append(tokens, Token{TokenNumber, input[start:i], start})

		case unicode.IsLetter(c) || c == '_':
			start := i
			for i < len(input) && (unicode.IsLetter(rune(input[i])) || unicode.IsDigit(rune(input[i])) || input[i] == '_') {
				i++
			}
			tokens = append(tokens, Token{TokenIdent, input[start:i], start})

		default:
			return nil, errorf(i, "unexpected character %q", string(c))
		}
	}
	tokens = append(tokens, Token{TokenEOF, "", len(input)})
	return tokens, nil
}
