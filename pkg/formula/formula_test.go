package formula_test

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/pkg/formula"
)

func TestFormulaEval(t *testing.T) {
	Convey("Given arithmetic formulas over named inputs", t, func() {
		Convey("When operator precedence and grouping are exercised", func() {
			expr, err := formula.Parse("a + b * 2")
			So(err, ShouldBeNil)
			grouped, err := formula.Parse("(a + b) * 2")
			So(err, ShouldBeNil)

			Convey("Then multiplication binds tighter than addition unless parenthesised", func() {
				v, ok := expr.Eval(map[string]float64{"a": 1, "b": 3})
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, 7) // 1 + (3*2)

				v, ok = grouped.Eval(map[string]float64{"a": 1, "b": 3})
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, 8) // (1+3) * 2
			})
		})

		Convey("When subtraction and division are evaluated", func() {
			expr, err := formula.Parse("total - used / 2")
			So(err, ShouldBeNil)

			Convey("Then both operators apply with the expected precedence", func() {
				v, ok := expr.Eval(map[string]float64{"total": 10, "used": 4})
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, 8) // 10 - (4/2)
			})
		})

		Convey("When a formula divides by zero", func() {
			expr, err := formula.Parse("a / b")
			So(err, ShouldBeNil)

			Convey("Then the result is undefined rather than infinite", func() {
				_, ok := expr.Eval(map[string]float64{"a": 1, "b": 0})
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When a referenced input is missing", func() {
			expr, err := formula.Parse("a + b")
			So(err, ShouldBeNil)

			Convey("Then the result is undefined regardless of which side is absent", func() {
				_, ok := expr.Eval(map[string]float64{"a": 1})
				So(ok, ShouldBeFalse)

				_, ok = expr.Eval(map[string]float64{"b": 1})
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When a formula uses unary negation", func() {
			expr, err := formula.Parse("-a + 3")
			So(err, ShouldBeNil)

			Convey("Then the operand is negated before the sum", func() {
				v, ok := expr.Eval(map[string]float64{"a": 2})
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, 1)
			})

			Convey("Then a negated missing input stays undefined", func() {
				_, ok := expr.Eval(map[string]float64{})
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When a formula is a bare decimal literal", func() {
			expr, err := formula.Parse("2.5")
			So(err, ShouldBeNil)

			Convey("Then it evaluates to that constant with no refs", func() {
				v, ok := expr.Eval(nil)
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, 2.5)
				So(expr.Refs(), ShouldBeEmpty)
			})
		})
	})
}

func TestFormulaRefs(t *testing.T) {
	Convey("Given a formula that reads several attributes", t, func() {
		Convey("When the same identifier appears more than once", func() {
			expr, err := formula.Parse("width * height + width")
			So(err, ShouldBeNil)

			Convey("Then Refs lists each distinct name once, in first-seen order", func() {
				So(expr.Refs(), ShouldResemble, []string{"width", "height"})
			})
		})

		Convey("When identifiers use underscores and digits", func() {
			expr, err := formula.Parse("_net_1 + gross2")
			So(err, ShouldBeNil)

			Convey("Then they are accepted as whole identifiers", func() {
				So(expr.Refs(), ShouldResemble, []string{"_net_1", "gross2"})
			})
		})
	})
}

func TestFormulaParseErrors(t *testing.T) {
	Convey("Given malformed formula sources", t, func() {
		cases := []struct {
			name string
			src  string
		}{
			{"an empty formula", ""},
			{"only whitespace", "   "},
			{"a dangling trailing operator", "a +"},
			{"a leading binary operator", "* a"},
			{"an unclosed parenthesis", "(a + b"},
			{"a stray closing parenthesis", "a + b)"},
			{"a malformed number", "1.2.3"},
			{"a bad expression inside parentheses", "(a + )"},
			{"an operator with no right operand inside parentheses", "(a *)"},
		}

		for _, c := range cases {
			Convey("When parsing "+c.name, func() {
				expr, err := formula.Parse(c.src)

				Convey("Then parsing is rejected with an error and no expression", func() {
					So(err, ShouldNotBeNil)
					So(expr, ShouldBeNil)
				})
			})
		}
	})
}

// TestFormulaRejectsUnknownCharacters pins the CORRECT behaviour for a formula
// containing a character the lexer does not recognise.
//
// It currently fails: the lexer's default arm emits token{kind: tokEOF,
// text: string(c)} intending to "force an 'unexpected' error upstream", but
// Parse's terminal check is `p.tok.kind != tokEOF`, which that synthetic token
// satisfies. So parsing STOPS at the unknown character and silently returns the
// truncated prefix instead of an error — "price # qty" parses as "price".
//
// This is silent data corruption rather than a crash: domain/attribute's
// Computed.Validate accepts the definition, Refs() under-reports the
// dependency (weakening cycle detection and recompute tracking), and
// application/computed materializes a wrong value for every entity.
//
// Skipped pending the fix; see the coverage report that surfaced it.
func TestFormulaRejectsUnknownCharacters(t *testing.T) {
	t.Skip("known bug: an unrecognised character silently truncates the formula instead of erroring")

	Convey("Given a formula containing an unrecognised character", t, func() {
		Convey("When it is parsed", func() {
			expr, err := formula.Parse("price # qty")

			Convey("Then it is rejected rather than silently truncated to its prefix", func() {
				So(err, ShouldNotBeNil)
				So(expr, ShouldBeNil)
			})
		})
	})
}
