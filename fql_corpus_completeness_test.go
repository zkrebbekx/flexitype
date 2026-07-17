package flexitype_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/fql"
)

// This is the corpus-completeness guard for issue #207. The FQL parity corpus
// (parityCorpus, in fql_parity_pg_test.go) is only meaningful if it actually
// exercises every FQL construct — a future construct that no corpus query
// produces would silently ship with zero memory-vs-Postgres coverage.
//
// The guard derives the required construct set by REFLECTING OVER SOURCE, not
// from a hand-maintained list: it scans application/query/bound.go for every
// type carrying a boundNode() method (the BoundNode implementations) and
// pkg/fql/ast.go for every AggFunc constant. Adding a new Bound* type or a new
// aggregate func therefore automatically raises the bar, and this test fails
// until parityCorpus grows a query that produces it. Conversely it walks each
// corpus query's parsed AST and maps each node to the Bound* type / func the
// binder would produce, so the observed set is grounded in the real corpus.

// findRepoRoot walks up from the test's working directory until it finds the
// module's go.mod, so the source scan works regardless of where `go test` is
// invoked.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate go.mod above %s", dir)
		}
		dir = parent
	}
}

// scanBoundTypes returns the names of every type in bound.go that implements
// BoundNode — i.e. every type with a `func (*X) boundNode()` method.
func scanBoundTypes(t *testing.T, path string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := map[string]bool{}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 || fd.Name.Name != "boundNode" {
			continue
		}
		star, ok := fd.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		if id, ok := star.X.(*ast.Ident); ok {
			out[id.Name] = true
		}
	}
	return out
}

// scanAggFuncs returns the required aggregate-func names (every AggFunc const
// except the FuncNone sentinel) plus a value->name map so the AST walk can
// translate a node's func value ("min") back to its Go name ("FuncMin").
func scanAggFuncs(t *testing.T, path string) (required map[string]bool, valueToName map[string]string) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	required = map[string]bool{}
	valueToName = map[string]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		// Within a const block the type declared on one spec carries down to
		// the specs that follow it without a type of their own.
		curType := ""
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if vs.Type != nil {
				if id, ok := vs.Type.(*ast.Ident); ok {
					curType = id.Name
				}
			}
			if curType != "AggFunc" {
				continue
			}
			for i, name := range vs.Names {
				val := ""
				if i < len(vs.Values) {
					if bl, ok := vs.Values[i].(*ast.BasicLit); ok {
						if unq, uerr := strconv.Unquote(bl.Value); uerr == nil {
							val = unq
						}
					}
				}
				valueToName[val] = name.Name
				if name.Name != "FuncNone" {
					required[name.Name] = true
				}
			}
		}
	}
	return required, valueToName
}

// walkFQL records, for one parsed query, which Bound* types and aggregate
// funcs the binder would emit. It mirrors binder.bind's dispatch exactly:
// type-scoped Compare/In bind to BoundType, everything else to its namesake,
// and traversals recurse into their inner expression.
func walkFQL(n fql.Node, boundTypes map[string]bool, funcs map[string]bool, valueToName map[string]string) {
	recordFunc := func(fn fql.AggFunc) {
		if fn == fql.FuncNone {
			return
		}
		if name, ok := valueToName[string(fn)]; ok {
			funcs[name] = true
		}
	}
	switch x := n.(type) {
	case *fql.Logical:
		boundTypes["BoundLogical"] = true
		for _, e := range x.Exprs {
			walkFQL(e, boundTypes, funcs, valueToName)
		}
	case *fql.Not:
		boundTypes["BoundNot"] = true
		walkFQL(x.Expr, boundTypes, funcs, valueToName)
	case *fql.Compare:
		if x.Field.Scope == fql.ScopeType {
			boundTypes["BoundType"] = true
		} else {
			boundTypes["BoundCompare"] = true
		}
		recordFunc(x.Func)
	case *fql.In:
		if x.Field.Scope == fql.ScopeType {
			boundTypes["BoundType"] = true
		} else {
			boundTypes["BoundIn"] = true
		}
		recordFunc(x.Func)
	case *fql.Range:
		boundTypes["BoundRange"] = true
		recordFunc(x.Func)
	case *fql.Has:
		boundTypes["BoundHas"] = true
	case *fql.StringMatch:
		boundTypes["BoundStringMatch"] = true
	case *fql.Matches:
		boundTypes["BoundMatches"] = true
	case *fql.Traversal:
		boundTypes["BoundTraversal"] = true
		walkFQL(x.Inner, boundTypes, funcs, valueToName)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// missingFrom returns the members of want not present in have.
func missingFrom(want, have map[string]bool) []string {
	var out []string
	for k := range want {
		if !have[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func TestFQLCorpusCompleteness(t *testing.T) {
	root := findRepoRoot(t)
	requiredBound := scanBoundTypes(t, filepath.Join(root, "application", "query", "bound.go"))
	requiredFuncs, valueToName := scanAggFuncs(t, filepath.Join(root, "pkg", "fql", "ast.go"))

	Convey("Given the FQL constructs declared in source (reflected, not hand-listed)", t, func() {
		So(requiredBound, ShouldNotBeEmpty)
		So(requiredFuncs, ShouldNotBeEmpty)

		observedBound := map[string]bool{}
		observedFuncs := map[string]bool{}
		for _, pq := range parityCorpus {
			node, err := fql.Parse(pq.q)
			So(err, ShouldBeNil)
			walkFQL(node, observedBound, observedFuncs, valueToName)
		}

		Convey("Then every BoundNode type is exercised by at least one corpus query", func() {
			// A non-empty result names the exact construct(s) that ship with
			// zero parity coverage — add a corpus query producing each.
			So(missingFrom(requiredBound, observedBound), ShouldBeEmpty)
		})

		Convey("And the corpus references no Bound* type that no longer exists in source", func() {
			So(missingFrom(observedBound, requiredBound), ShouldBeEmpty)
		})

		Convey("Then every aggregate func is exercised by at least one corpus query", func() {
			So(missingFrom(requiredFuncs, observedFuncs), ShouldBeEmpty)
		})

		Convey("And the corpus references no aggregate func that no longer exists in source", func() {
			So(missingFrom(observedFuncs, requiredFuncs), ShouldBeEmpty)
		})

		Convey("Then the enforced construct list matches the documented surface", func() {
			// Belt-and-braces: pin the reflected sets so an accidental scan
			// regression (e.g. a parser change that drops a method) is visible.
			So(sortedKeys(requiredBound), ShouldResemble, []string{
				"BoundCompare", "BoundHas", "BoundIn", "BoundLogical", "BoundMatches",
				"BoundNot", "BoundRange", "BoundStringMatch", "BoundTraversal", "BoundType",
			})
			So(sortedKeys(requiredFuncs), ShouldResemble, []string{
				"FuncCount", "FuncLength", "FuncMax", "FuncMin",
			})
		})
	})

	Convey("Given a seeded in-memory catalog", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory(flexitype.WithSearchIndex())
		seedProductCatalog(ctx, t, svc)

		Convey("Then every corpus query binds and executes without error", func() {
			// Proves the corpus is not merely parseable but actually bindable
			// against a real schema — a query that referenced a missing
			// attribute or relationship would fail here.
			for _, pq := range parityCorpus {
				typ := pq.typ
				if typ == "" {
					typ = "product"
				}
				_, err := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
					Type: typ, Query: pq.q, Page: db.PageArgs{}, Scope: pq.scope,
				})
				So(err, ShouldBeNil)
			}
		})
	})
}
