package fql

import (
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestParserBounds(t *testing.T) {
	Convey("Given adversarial query input", t, func() {
		Convey("Deeply nested parentheses are rejected, not crashed", func() {
			q := strings.Repeat("(", 500) + "a = 1" + strings.Repeat(")", 500)
			_, err := Parse(q)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "too deep")
		})

		Convey("A deep not-chain is rejected, not crashed", func() {
			q := strings.Repeat("not ", 500) + "a = 1"
			_, err := Parse(q)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "too deep")
		})

		Convey("An over-length query is rejected before lexing", func() {
			_, err := Parse(strings.Repeat("a", maxQueryLen+1))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "too long")
		})

		Convey("Moderately nested queries still parse", func() {
			q := strings.Repeat("(", 40) + "a = 1" + strings.Repeat(")", 40)
			node, err := Parse(q)
			So(err, ShouldBeNil)
			So(node, ShouldNotBeNil)
		})
	})
}

func TestParser(t *testing.T) {
	Convey("Given FQL query text", t, func() {
		Convey("When parsing a simple comparison", func() {
			node, err := Parse(`category = "bike"`)

			Convey("Then it yields a Compare with an entity-scoped field", func() {
				So(err, ShouldBeNil)
				cmp := node.(*Compare)
				So(cmp.Field.Name, ShouldEqual, "category")
				So(cmp.Field.Scope, ShouldEqual, ScopeEntity)
				So(cmp.Op, ShouldEqual, CmpEq)
				So(cmp.Literal.Kind, ShouldEqual, LitString)
				So(cmp.Literal.Text, ShouldEqual, "bike")
			})
		})

		Convey("When parsing word operators and aggregates", func() {
			node, err := Parse(`min(price) gte 500`)

			Convey("Then the aggregate and alias resolve", func() {
				So(err, ShouldBeNil)
				cmp := node.(*Compare)
				So(cmp.Func, ShouldEqual, FuncMin)
				So(cmp.Op, ShouldEqual, CmpGte)
				So(cmp.Literal.Text, ShouldEqual, "500")
			})
		})

		Convey("When parsing and/or nesting with parentheses", func() {
			node, err := Parse(`a = 1 and (b = 2 or not c = 3)`)

			Convey("Then precedence follows the parens", func() {
				So(err, ShouldBeNil)
				and := node.(*Logical)
				So(and.Op, ShouldEqual, OpAnd)
				So(and.Exprs, ShouldHaveLength, 2)
				or := and.Exprs[1].(*Logical)
				So(or.Op, ShouldEqual, OpOr)
				_, isNot := or.Exprs[1].(*Not)
				So(isNot, ShouldBeTrue)
			})
		})

		Convey("When parsing in, range, has and string matches", func() {
			node, err := Parse(`tags in ("sale", "new") and range(price, 10, 20) and has(sku) and icontains(name, "PRO")`)

			Convey("Then every condition kind parses", func() {
				So(err, ShouldBeNil)
				and := node.(*Logical)
				So(and.Exprs, ShouldHaveLength, 4)
				in := and.Exprs[0].(*In)
				So(in.Values, ShouldHaveLength, 2)
				rng := and.Exprs[1].(*Range)
				So(rng.Lo.Text, ShouldEqual, "10")
				So(rng.Hi.Text, ShouldEqual, "20")
				_, isHas := and.Exprs[2].(*Has)
				So(isHas, ShouldBeTrue)
				match := and.Exprs[3].(*StringMatch)
				So(match.Kind, ShouldEqual, MatchIContains)
			})
		})

		Convey("When parsing a relationship traversal with link attributes", func() {
			node, err := Parse(`child(supplied_by) { iequals(contact_email, "X") and link.lead_time_days <= 14 }`)

			Convey("Then the traversal nests the inner expression", func() {
				So(err, ShouldBeNil)
				tr := node.(*Traversal)
				So(tr.Direction, ShouldEqual, DirChild)
				So(tr.Relationship, ShouldEqual, "supplied_by")
				inner := tr.Inner.(*Logical)
				link := inner.Exprs[1].(*Compare)
				So(link.Field.Scope, ShouldEqual, ScopeLink)
				So(link.Field.Name, ShouldEqual, "lead_time_days")
			})
		})

		Convey("When parsing the virtual type field", func() {
			node, err := Parse(`type isa product and type in (bike, car)`)

			Convey("Then type conditions carry the type scope", func() {
				So(err, ShouldBeNil)
				and := node.(*Logical)
				isa := and.Exprs[0].(*Compare)
				So(isa.Field.Scope, ShouldEqual, ScopeType)
				So(isa.Op, ShouldEqual, CmpIsa)
				So(isa.Literal.Kind, ShouldEqual, LitIdent)
				in := and.Exprs[1].(*In)
				So(in.Field.Scope, ShouldEqual, ScopeType)
			})
		})

		Convey("When parsing malformed queries", func() {
			cases := map[string]string{
				``:                     "empty query",
				`price >`:              "expected a value",
				`(a = 1`:               "expected ')'",
				`a = 1 banana split`:   "unexpected",
				`child(rel) a = 1`:     "expected '{'",
				`contains(name)`:       "expected ','",
				`price ! 5`:            "did you mean",
				`name = "unterminated`: "unterminated string",
				`in (1)`:               "expected",
				`range(price, 10)`:     "expected ','",
				`price = 1.2.3`:        "one decimal point",
				`price = 1..2`:         "must be followed by a digit",
				`price = 1.`:           "must be followed by a digit",
			}
			Convey("Then each fails with a positioned message", func() {
				for input, wants := range cases {
					_, err := Parse(input)
					So(err, ShouldNotBeNil)
					So(err.Error(), ShouldContainSubstring, wants)
				}
			})
		})

		Convey("When keywords appear in different cases", func() {
			node, err := Parse(`a = 1 AND NOT b = 2 OR c IN (3)`)

			Convey("Then parsing is case-insensitive", func() {
				So(err, ShouldBeNil)
				or := node.(*Logical)
				So(or.Op, ShouldEqual, OpOr)
			})
		})
	})
}
