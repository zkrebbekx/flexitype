package formula_test

import (
	"math/big"
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
				v, ok := expr.Eval(formula.Inputs{"a": []float64{1}, "b": []float64{3}})
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, 7) // 1 + (3*2)

				v, ok = grouped.Eval(formula.Inputs{"a": []float64{1}, "b": []float64{3}})
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, 8) // (1+3) * 2
			})
		})

		Convey("When subtraction and division are evaluated", func() {
			expr, err := formula.Parse("total - used / 2")
			So(err, ShouldBeNil)

			Convey("Then both operators apply with the expected precedence", func() {
				v, ok := expr.Eval(formula.Inputs{"total": []float64{10}, "used": []float64{4}})
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, 8) // 10 - (4/2)
			})
		})

		Convey("When a formula divides by zero", func() {
			expr, err := formula.Parse("a / b")
			So(err, ShouldBeNil)

			Convey("Then the result is undefined rather than infinite", func() {
				_, ok := expr.Eval(formula.Inputs{"a": []float64{1}, "b": []float64{0}})
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When a referenced input is missing", func() {
			expr, err := formula.Parse("a + b")
			So(err, ShouldBeNil)

			Convey("Then the result is undefined regardless of which side is absent", func() {
				_, ok := expr.Eval(formula.Inputs{"a": []float64{1}})
				So(ok, ShouldBeFalse)

				_, ok = expr.Eval(formula.Inputs{"b": []float64{1}})
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When a formula uses unary negation", func() {
			expr, err := formula.Parse("-a + 3")
			So(err, ShouldBeNil)

			Convey("Then the operand is negated before the sum", func() {
				v, ok := expr.Eval(formula.Inputs{"a": []float64{2}})
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, 1)
			})

			Convey("Then a negated missing input stays undefined", func() {
				_, ok := expr.Eval(formula.Inputs{})
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

// TestFormulaRejectsUnknownCharacters is the regression test for a formula
// containing a character the lexer does not recognise.
//
// It used to pass silently: the lexer's default arm emitted token{kind: tokEOF,
// text: string(c)} intending to "force an 'unexpected' error upstream", but
// Parse's terminal check is `p.tok.kind != tokEOF`, which that synthetic token
// satisfied. Parsing therefore STOPPED at the unknown character and returned
// the truncated prefix instead of an error — "price # qty" parsed as "price".
//
// That was silent data corruption rather than a crash: domain/attribute's
// Computed.Validate accepted the definition, Refs() under-reported the
// dependency (weakening cycle detection and recompute tracking), and
// application/computed materialized a wrong value for every entity.
//
// The fix gives an unrecognised character its own token kind (tokInvalid), so
// the terminal check rejects it instead of mistaking it for end of input.
func TestFormulaRejectsUnknownCharacters(t *testing.T) {
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

func TestAggregates(t *testing.T) {
	Convey("Given a formula that aggregates a multi-valued name", t, func() {
		expr, err := formula.Parse("sum(lines) * 2")
		So(err, ShouldBeNil)

		Convey("When it is evaluated over several members", func() {
			v, ok := expr.Eval(formula.Inputs{"lines": []float64{10, 20, 30}})

			Convey("Then every member contributes", func() {
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, 120)
			})
		})

		Convey("When the name is read", func() {
			Convey("Then it is a reference but not a BARE one", func() {
				So(expr.Refs(), ShouldResemble, []string{"lines"})
				So(expr.ScalarRefs(), ShouldBeEmpty)
			})
		})
	})

	Convey("Given each aggregate over the same members", t, func() {
		in := formula.Inputs{"x": []float64{4, 1, 7}}
		eval := func(src string) (float64, bool) {
			expr, err := formula.Parse(src)
			So(err, ShouldBeNil)
			return expr.Eval(in)
		}

		Convey("Then each folds the whole list", func() {
			v, ok := eval("sum(x)")
			So(ok, ShouldBeTrue)
			So(v, ShouldEqual, 12)

			v, ok = eval("count(x)")
			So(ok, ShouldBeTrue)
			So(v, ShouldEqual, 3)

			v, ok = eval("avg(x)")
			So(ok, ShouldBeTrue)
			So(v, ShouldEqual, 4)

			v, ok = eval("min(x)")
			So(ok, ShouldBeTrue)
			So(v, ShouldEqual, 1)

			v, ok = eval("max(x)")
			So(ok, ShouldBeTrue)
			So(v, ShouldEqual, 7)
		})
	})

	Convey("Given an attribute with no values at all", t, func() {
		empty := formula.Inputs{}

		Convey("When it is counted", func() {
			expr, err := formula.Parse("count(x)")
			So(err, ShouldBeNil)
			v, ok := expr.Eval(empty)

			Convey("Then the answer is 0: counting nothing has one reading", func() {
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, 0)
			})
		})

		Convey("When it is summed", func() {
			expr, err := formula.Parse("sum(x)")
			So(err, ShouldBeNil)
			_, ok := expr.Eval(empty)

			Convey("Then the answer is UNDEFINED, not zero: the entity holds no data", func() {
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When min, max or avg is taken", func() {
			for _, src := range []string{"min(x)", "max(x)", "avg(x)"} {
				expr, err := formula.Parse(src)
				So(err, ShouldBeNil)
				_, ok := expr.Eval(empty)
				So(ok, ShouldBeFalse)
			}
		})
	})

	Convey("Given a bare name holding more than one value", t, func() {
		expr, err := formula.Parse("x + 1")
		So(err, ShouldBeNil)

		Convey("When it is evaluated", func() {
			_, ok := expr.Eval(formula.Inputs{"x": []float64{1, 2}})

			Convey("Then it is undefined rather than using an arbitrary member", func() {
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When exactly one value is present", func() {
			v, ok := expr.Eval(formula.Inputs{"x": []float64{41}})

			Convey("Then it evaluates as before", func() {
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, 42)
			})
		})
	})

	Convey("Given aggregates in exact arithmetic", t, func() {
		expr, err := formula.Parse("avg(x)")
		So(err, ShouldBeNil)
		rat := func(s string) *big.Rat { r, _ := new(big.Rat).SetString(s); return r }

		Convey("When decimals that no float can hold are averaged", func() {
			v, ok := expr.EvalRat(formula.RatInputs{"x": []*big.Rat{rat("0.1"), rat("0.2")}})

			Convey("Then the result is exact", func() {
				So(ok, ShouldBeTrue)
				So(v.RatString(), ShouldEqual, "3/20") // 0.15
			})
		})
	})

	Convey("Given malformed aggregate syntax", t, func() {
		Convey("When the call is not a single name", func() {
			Convey("Then it is refused at parse time", func() {
				for _, src := range []string{
					"sum(", "sum()", "sum x", "sum(a + b)", "sum(sum(a))", "sum(a",
				} {
					_, err := formula.Parse(src)
					So(err, ShouldNotBeNil)
				}
			})
		})
	})

	Convey("Given an attribute whose name is not an aggregate", t, func() {
		expr, err := formula.Parse("summary + total")

		Convey("Then ordinary names that merely start alike are unaffected", func() {
			So(err, ShouldBeNil)
			So(expr.ScalarRefs(), ShouldResemble, []string{"summary", "total"})
		})
	})
}

// TestAggregateNamesAreNotReservedWords proves an attribute called count,
// sum, min, max or avg still reads bare.
//
// The five names were treated as calls unconditionally, so `count * 2` — an
// ordinary quantity attribute named count — stopped parsing. A stored formula
// is rehydrated without re-validation, so it survived in the database and
// then failed twice: the materializer skipped it silently and froze the last
// value, and the validation sweep, which walks every computed attribute in
// the hierarchy, failed the create or update of UNRELATED attributes with an
// "invalid formula" the caller never wrote.
func TestAggregateNamesAreNotReservedWords(t *testing.T) {
	Convey("Given formulas that read aggregate names as ordinary attributes", t, func() {
		cases := []struct {
			src  string
			refs []string
		}{
			{"count * 2", []string{"count"}},
			{"min + max", []string{"min", "max"}},
			{"price * count", []string{"price", "count"}},
			{"sum / 2", []string{"sum"}},
			{"avg - 1", []string{"avg"}},
		}

		Convey("Then each parses and reads the name bare", func() {
			for _, c := range cases {
				expr, err := formula.Parse(c.src)
				So(err, ShouldBeNil)
				So(expr.Refs(), ShouldResemble, c.refs)
				So(expr.ScalarRefs(), ShouldResemble, c.refs)
			}
		})

		Convey("Then a call form is still a call, including with a space before '('", func() {
			expr, err := formula.Parse("count (tags) + 1")
			So(err, ShouldBeNil)
			So(expr.Refs(), ShouldResemble, []string{"tags"})
			So(expr.ScalarRefs(), ShouldBeEmpty)
		})

		Convey("Then an aggregate over an attribute named count still works", func() {
			expr, err := formula.Parse("sum(count)")
			So(err, ShouldBeNil)
			So(expr.Refs(), ShouldResemble, []string{"count"})
			So(expr.NumericRefs(), ShouldResemble, []string{"count"})
		})

		Convey("Then a nested aggregate is still refused", func() {
			_, err := formula.Parse("sum(count(x))")
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "cannot aggregate another aggregate")
		})
	})
}

// TestCountFoldsMembersNotNumbers proves count() reports how many values a
// name holds, whatever their data type.
//
// Inputs carries only the values that coerce to a number, so count() over a
// name whose members are strings, dates or media saw an empty list and
// answered 0 — a plausible number for an attribute that in fact held values.
func TestCountFoldsMembersNotNumbers(t *testing.T) {
	Convey("Given a name with three members, none of them numeric", t, func() {
		expr, err := formula.Parse("count(tags)")
		So(err, ShouldBeNil)

		Convey("When the member count is supplied", func() {
			got, ok := expr.EvalWithMembers(formula.Inputs{}, formula.Members{"tags": 3})

			Convey("Then it counts the members", func() {
				So(ok, ShouldBeTrue)
				So(got, ShouldEqual, 3)
			})
		})

		Convey("When exact evaluation is used", func() {
			got, ok := expr.EvalRatWithMembers(formula.RatInputs{}, formula.Members{"tags": 3})

			Convey("Then it counts the members there too", func() {
				So(ok, ShouldBeTrue)
				So(got.RatString(), ShouldEqual, "3")
			})
		})

		Convey("When no member count is supplied", func() {
			got, ok := expr.Eval(formula.Inputs{})

			Convey("Then it falls back to the numeric inputs", func() {
				So(ok, ShouldBeTrue)
				So(got, ShouldEqual, 0)
			})
		})
	})

	Convey("Given a sum over a name whose members do not coerce", t, func() {
		expr, err := formula.Parse("sum(tags)")
		So(err, ShouldBeNil)

		Convey("When it is evaluated with a member count but no numbers", func() {
			_, ok := expr.EvalWithMembers(formula.Inputs{}, formula.Members{"tags": 3})

			Convey("Then the result is undefined rather than zero", func() {
				So(ok, ShouldBeFalse)
			})
		})
	})
}

// TestNumericRefsSeparatesCountFromTheRest pins which names have to coerce to
// numbers: every bare read, and every name folded by sum, avg, min or max.
func TestNumericRefsSeparatesCountFromTheRest(t *testing.T) {
	Convey("Given a formula that counts one name and sums another", t, func() {
		expr, err := formula.Parse("count(photos) + sum(amounts) + price")
		So(err, ShouldBeNil)

		Convey("Then only the folded and bare names are numeric", func() {
			So(expr.Refs(), ShouldResemble, []string{"photos", "amounts", "price"})
			So(expr.NumericRefs(), ShouldResemble, []string{"amounts", "price"})
			So(expr.ScalarRefs(), ShouldResemble, []string{"price"})
		})
	})
}
