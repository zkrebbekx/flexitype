package fql

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestLexerStringsAndCharacters(t *testing.T) {
	Convey("Given query text the lexer must tokenise", t, func() {
		Convey("When a string contains escaped characters", func() {
			node, err := Parse(`name = "say \"hi\""`)

			Convey("Then the escapes are unwrapped into the literal text", func() {
				So(err, ShouldBeNil)
				cmp := node.(*Compare)
				So(cmp.Literal.Text, ShouldEqual, `say "hi"`)
			})
		})

		Convey("When a string escapes its own delimiter in single quotes", func() {
			node, err := Parse(`name = 'it\'s here'`)

			Convey("Then the escaped quote does not terminate the string", func() {
				So(err, ShouldBeNil)
				So(node.(*Compare).Literal.Text, ShouldEqual, "it's here")
			})
		})

		Convey("When a backslash escapes an ordinary character", func() {
			node, err := Parse(`name = "a\\b"`)

			Convey("Then the escaped character is taken literally", func() {
				So(err, ShouldBeNil)
				So(node.(*Compare).Literal.Text, ShouldEqual, `a\b`)
			})
		})

		Convey("When the input contains a character the lexer does not know", func() {
			_, err := Parse(`name = #`)

			Convey("Then it is rejected with a positioned message naming the character", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "unexpected character")
				So(err.Error(), ShouldContainSubstring, "#")
			})
		})
	})
}

func TestParserPunctuationErrors(t *testing.T) {
	Convey("Given queries missing required punctuation", t, func() {
		cases := []struct {
			name  string
			query string
			wants string
		}{
			// traversal
			{"a traversal with no opening paren", `child rel { a = 1 }`, "'(' after child"},
			{"a traversal with no relationship name", `child(1) { a = 1 }`, "a relationship name"},
			{"a traversal with no closing paren", `child(rel { a = 1 }`, "')'"},
			{"a traversal with no closing brace", `child(rel) { a = 1`, "'}'"},
			{"a traversal whose body is malformed", `child(rel) { a = }`, "expected a value"},
			{"a parent traversal with no opening paren", `parent rel { a = 1 }`, "'(' after parent"},

			// range
			{"a range with no opening paren", `range price, 1, 2)`, "'(' after range"},
			{"a range missing the separator", `range(price 10, 20)`, "','"},
			{"a range with no closing paren", `range(price, 10, 20`, "')'"},
			{"a range with a malformed bound", `range(price, 10, )`, "expected a value"},

			// has
			{"a has with no opening paren", `has sku)`, "'(' after has"},
			{"a has with no closing paren", `has(sku`, "')'"},

			// string match
			{"a string match with no opening paren", `contains name, "x")`, "'(' after contains"},
			{"a string match with no closing paren", `contains(name, "x"`, "')'"},
			{"a string match with a malformed value", `contains(name, )`, "expected a value"},

			// matches
			{"a full-text match with no opening paren", `matches "x")`, "'(' after matches"},
			{"a full-text match without a quoted string", `matches(name)`, "a quoted search string"},
			{"a full-text match with no closing paren", `matches("x"`, "')'"},

			// in
			{"an in-list with no opening paren", `tags in "sale"`, "'(' after in"},
			{"an in-list with a malformed member", `tags in (1, )`, "expected a value"},
		}

		for _, c := range cases {
			Convey("When parsing "+c.name, func() {
				node, err := Parse(c.query)

				Convey("Then it is rejected with a positioned, explanatory message", func() {
					So(err, ShouldNotBeNil)
					So(node, ShouldBeNil)
					So(err.Error(), ShouldContainSubstring, c.wants)
				})
			})
		}
	})
}

func TestNodePositions(t *testing.T) {
	Convey("Given parsed FQL nodes", t, func() {
		Convey("When each node kind reports its position", func() {
			Convey("Then it points at the offset where the condition starts", func() {
				// Compare: starts at offset 0.
				cmp, err := Parse(`price > 5`)
				So(err, ShouldBeNil)
				So(cmp.Position(), ShouldEqual, 0)

				// Logical and Not: "a = 1 and not b = 2"
				//                   0     6       10
				logical, err := Parse(`a = 1 and not b = 2`)
				So(err, ShouldBeNil)
				and := logical.(*Logical)
				So(and.Position(), ShouldEqual, and.Pos)
				not := and.Exprs[1].(*Not)
				So(not.Position(), ShouldEqual, 10)

				// In
				in, err := Parse(`tags in ("sale")`)
				So(err, ShouldBeNil)
				So(in.Position(), ShouldEqual, 0)

				// Range
				rng, err := Parse(`range(price, 1, 2)`)
				So(err, ShouldBeNil)
				So(rng.Position(), ShouldEqual, 0)

				// Has
				has, err := Parse(`has(sku)`)
				So(err, ShouldBeNil)
				So(has.Position(), ShouldEqual, 0)

				// StringMatch
				sm, err := Parse(`contains(name, "x")`)
				So(err, ShouldBeNil)
				So(sm.Position(), ShouldEqual, 0)

				// Traversal
				tr, err := Parse(`child(rel) { a = 1 }`)
				So(err, ShouldBeNil)
				So(tr.Position(), ShouldEqual, 0)

				// Matches
				m, err := Parse(`matches("widget")`)
				So(err, ShouldBeNil)
				So(m.Position(), ShouldEqual, 0)
			})
		})

		Convey("When a condition is not at the start of the query", func() {
			node, err := Parse(`a = 1 and has(sku)`)
			So(err, ShouldBeNil)

			Convey("Then its position reflects its real offset, for error reporting", func() {
				and := node.(*Logical)
				has := and.Exprs[1].(*Has)
				So(has.Position(), ShouldEqual, 10) // index of "has"
			})
		})
	})
}

func TestParserTraversalDirections(t *testing.T) {
	Convey("Given traversals in each supported direction", t, func() {
		Convey("When each direction keyword is parsed", func() {
			Convey("Then the direction is captured on the node", func() {
				for kw, want := range map[string]Direction{
					"child":  DirChild,
					"parent": DirParent,
					"linked": DirAny,
				} {
					node, err := Parse(kw + `(rel) { a = 1 }`)
					So(err, ShouldBeNil)
					So(node.(*Traversal).Direction, ShouldEqual, want)
				}
			})
		})

		Convey("When a traversal nests another traversal", func() {
			node, err := Parse(`child(a) { parent(b) { x = 1 } }`)

			Convey("Then the inner traversal is nested under the outer", func() {
				So(err, ShouldBeNil)
				outer := node.(*Traversal)
				So(outer.Relationship, ShouldEqual, "a")
				inner := outer.Inner.(*Traversal)
				So(inner.Relationship, ShouldEqual, "b")
				So(inner.Direction, ShouldEqual, DirParent)
			})
		})
	})
}
