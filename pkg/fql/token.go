// Package fql implements the flexitype query language: a lexer, a
// recursive-descent parser and a positioned AST. The package is
// dependency-free and schema-agnostic — binding names to attribute and
// relationship definitions happens in the application layer.
package fql

import "fmt"

// TokenKind classifies lexed tokens.
type TokenKind int

// The token kinds the lexer emits.
const (
	TokenEOF TokenKind = iota
	TokenIdent
	TokenString
	TokenNumber
	TokenLParen
	TokenRParen
	TokenLBrace
	TokenRBrace
	TokenComma
	TokenDot
	TokenOp // = != > >= < <=
)

// Token is one lexed unit with its byte position in the input.
type Token struct {
	Kind TokenKind
	Text string
	Pos  int
}

// Error is a positioned language error, suitable for editor underlines.
type Error struct {
	Pos     int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("position %d: %s", e.Pos, e.Message)
}

func errorf(pos int, format string, args ...any) *Error {
	return &Error{Pos: pos, Message: fmt.Sprintf(format, args...)}
}
