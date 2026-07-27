package flexitype_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/pkg/fql"
)

// TestDocumentedFQLParses parses every FQL expression the design document
// shows, so the grammar reference cannot drift from the parser again.
//
// The document's headline example did not parse: it wrote `"sale" in tags`,
// putting the literal on the left of `in`, which the parser refuses and which
// the document's own grammar rule contradicts two lines later. A grammar
// reference whose first example fails is worse than no reference — a reader
// concludes the feature is broken rather than the example.
//
// The document also omitted three implemented constructs, two of them
// mandatory: linked() is the only way to traverse a symmetric relationship,
// and a quantity comparison is refused without a unit suffix. Those cases are
// asserted here directly, so removing them from the grammar breaks a test.
func TestDocumentedFQLParses(t *testing.T) {
	Convey("Given every FQL expression in docs/design/query-language.md", t, func() {
		exprs := fqlExamplesFrom(t, "docs/design/query-language.md")

		Convey("Then the document contains examples to check", func() {
			// The headline block plus every complete inline expression. The
			// count guards against the extractor silently matching nothing,
			// which would make this test pass while checking zero examples.
			So(len(exprs), ShouldBeGreaterThanOrEqualTo, 4)
		})

		Convey("Then every one of them parses", func() {
			var failures []string
			for _, e := range exprs {
				if _, err := fql.Parse(e); err != nil {
					failures = append(failures, e+"\n    -> "+err.Error())
				}
			}
			So(strings.Join(failures, "\n"), ShouldEqual, "")
		})
	})

	Convey("Given the three constructs the document used to omit", t, func() {
		Convey("Then linked() traverses a relationship without a direction", func() {
			_, err := fql.Parse(`linked(supplied_by) { contact_email != "" }`)
			So(err, ShouldBeNil)
		})

		Convey("Then matches() is a bare relevance predicate", func() {
			_, err := fql.Parse(`matches("Alpha")`)
			So(err, ShouldBeNil)
		})

		Convey("Then a quantity comparison carries a unit suffix", func() {
			_, err := fql.Parse(`weight > 1500 g`)
			So(err, ShouldBeNil)
		})
	})

	Convey("Given the in operator", t, func() {
		Convey("Then the attribute is on the left, as the document now says", func() {
			_, err := fql.Parse(`tags in ("sale")`)
			So(err, ShouldBeNil)
		})

		Convey("Then a literal on the left is refused", func() {
			// This is what the headline example used to show.
			_, err := fql.Parse(`"sale" in tags`)
			So(err, ShouldNotBeNil)
		})
	})
}

// fqlExamplesFrom extracts the FQL expressions from the document's fenced
// blocks. It takes the blocks that hold expressions rather than grammar rules
// or URLs, which is what a reader would copy.
func fqlExamplesFrom(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []string
	// Complete inline expressions: a call or a comparison inside backticks,
	// skipping placeholders (`linked(rel) { ... }`) and bare names.
	inline := regexp.MustCompile("`([a-z_]+ *(?:\\(|[<>=!]).*?)`")
	for _, m := range inline.FindAllStringSubmatch(string(body), -1) {
		expr := strings.TrimSpace(m[1])
		// A runnable example carries an operand — a quoted literal or a
		// number. Without that it is prose naming a construct (`child()`,
		// `length(field)`), which is not meant to parse on its own.
		if strings.Contains(expr, "...") || strings.Contains(expr, "…") ||
			strings.Contains(expr, ":=") || !hasOperand(expr) {
			continue
		}
		out = append(out, expr)
	}
	for _, block := range regexp.MustCompile("(?s)```\n(.*?)```").FindAllStringSubmatch(string(body), -1) {
		text := strings.TrimSpace(block[1])
		switch {
		case text == "",
			strings.Contains(text, ":="), // grammar rules
			strings.HasPrefix(text, "GET"), strings.HasPrefix(text, "POST"),
			strings.Contains(text, "flexitype_"): // schema snippets
			continue
		}
		out = append(out, text)
	}
	return out
}

// hasOperand reports whether the expression carries a literal to compare
// against — a quoted string or a digit.
func hasOperand(expr string) bool {
	if strings.ContainsAny(expr, "\"'") {
		return true
	}
	return strings.ContainsAny(expr, "0123456789")
}
