package gql

import (
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestTypeName(t *testing.T) {
	Convey("Given an internal type name being rendered as a GraphQL object name", t, func() {
		Convey("When the name is snake_case", func() {
			Convey("Then it becomes PascalCase, distinct from the lowercase root field", func() {
				So(typeName("product"), ShouldEqual, "Product")
				So(typeName("purchase_order"), ShouldEqual, "PurchaseOrder")
				So(typeName("a_b_c"), ShouldEqual, "ABC")
			})
		})

		Convey("When the name has empty segments from repeated or edge underscores", func() {
			Convey("Then the empty segments are skipped", func() {
				So(typeName("purchase__order"), ShouldEqual, "PurchaseOrder")
				So(typeName("_product_"), ShouldEqual, "Product")
			})
		})

		Convey("When the name yields no usable characters", func() {
			Convey("Then a safe placeholder object name is used", func() {
				So(typeName(""), ShouldEqual, "Type")
				So(typeName("___"), ShouldEqual, "Type")
			})
		})
	})
}

func TestSanitizeName(t *testing.T) {
	Convey("Given a relationship label being coerced into a GraphQL field name", t, func() {
		Convey("When it contains spaces or dashes", func() {
			Convey("Then they become underscores and the result is lowercased", func() {
				So(sanitizeName("Supplied By"), ShouldEqual, "supplied_by")
				So(sanitizeName("supplied-by"), ShouldEqual, "supplied_by")
			})
		})

		Convey("When it contains characters GraphQL does not allow", func() {
			Convey("Then those characters are dropped", func() {
				So(sanitizeName("supplied@by!"), ShouldEqual, "suppliedby")
				So(sanitizeName("a.b/c"), ShouldEqual, "abc")
			})
		})

		Convey("When it starts with a digit", func() {
			Convey("Then that digit is dropped, since index 0 may not be numeric", func() {
				So(sanitizeName("1st_choice"), ShouldEqual, "st_choice")
				So(sanitizeName("1"), ShouldEqual, "")
			})
		})

		Convey("When trimming leading underscores exposes a digit", func() {
			Convey("Then the name is re-prefixed so it stays a valid identifier", func() {
				// '_' at index 0 is kept, so '1' at index 1 survives; trimming the
				// underscore would leave a digit first, which is not a legal
				// GraphQL name start.
				So(sanitizeName("_1st_choice"), ShouldEqual, "_1st_choice")
			})
		})

		Convey("When digits appear after the first position", func() {
			Convey("Then they are kept", func() {
				So(sanitizeName("a1"), ShouldEqual, "a1")
				So(sanitizeName("tier2_supplier"), ShouldEqual, "tier2_supplier")
			})
		})

		Convey("When it reduces to nothing usable", func() {
			Convey("Then the empty name signals the field must be skipped", func() {
				So(sanitizeName(""), ShouldEqual, "")
				So(sanitizeName("@@@"), ShouldEqual, "")
				So(sanitizeName("___"), ShouldEqual, "")
			})
		})
	})
}

func TestFirstNonEmpty(t *testing.T) {
	Convey("Given a label preference order", t, func() {
		Convey("When some candidates are empty", func() {
			Convey("Then the first non-empty one wins", func() {
				So(firstNonEmpty("", "child", "internal"), ShouldEqual, "child")
				So(firstNonEmpty("label"), ShouldEqual, "label")
			})
		})

		Convey("When every candidate is empty", func() {
			Convey("Then the empty string is returned", func() {
				So(firstNonEmpty("", ""), ShouldEqual, "")
				So(firstNonEmpty(), ShouldEqual, "")
			})
		})
	})
}

func TestClampFirst(t *testing.T) {
	Convey("Given a connection's first argument", t, func() {
		Convey("When it is absent, the wrong type, or non-positive", func() {
			Convey("Then the default page size is used", func() {
				So(clampFirst(nil), ShouldEqual, defaultFirst)
				So(clampFirst("10"), ShouldEqual, defaultFirst)
				So(clampFirst(0), ShouldEqual, defaultFirst)
				So(clampFirst(-5), ShouldEqual, defaultFirst)
			})
		})

		Convey("When it is within range", func() {
			Convey("Then it is honoured exactly", func() {
				So(clampFirst(1), ShouldEqual, 1)
				So(clampFirst(25), ShouldEqual, 25)
				So(clampFirst(maxFirst), ShouldEqual, maxFirst)
			})
		})

		Convey("When it exceeds the hard cap", func() {
			Convey("Then it is clamped so one query cannot demand an unbounded page", func() {
				So(clampFirst(maxFirst+1), ShouldEqual, maxFirst)
				So(clampFirst(1_000_000), ShouldEqual, maxFirst)
			})
		})
	})
}

func TestArgHelpers(t *testing.T) {
	Convey("Given GraphQL argument AST nodes", t, func() {
		Convey("When an integer literal is read", func() {
			Convey("Then it is decoded as an int", func() {
				So(argValue(&ast.IntValue{Value: "42"}, nil), ShouldEqual, 42)
			})

			Convey("Then an unparseable integer yields nil rather than a bogus value", func() {
				So(argValue(&ast.IntValue{Value: "not-a-number"}, nil), ShouldBeNil)
			})
		})

		Convey("When a string or boolean literal is read", func() {
			Convey("Then it is decoded to its Go value", func() {
				So(argValue(&ast.StringValue{Value: "cursor"}, nil), ShouldEqual, "cursor")
				So(argValue(&ast.BooleanValue{Value: true}, nil), ShouldEqual, true)
			})
		})

		Convey("When a variable is read", func() {
			vars := map[string]any{"n": 7}

			Convey("Then it resolves against the request's variables", func() {
				v := &ast.Variable{Name: &ast.Name{Value: "n"}}
				So(argValue(v, vars), ShouldEqual, 7)
			})

			Convey("Then an undefined variable yields nil", func() {
				v := &ast.Variable{Name: &ast.Name{Value: "missing"}}
				So(argValue(v, vars), ShouldBeNil)
			})
		})

		Convey("When an unsupported literal kind is read", func() {
			Convey("Then it yields nil", func() {
				So(argValue(&ast.FloatValue{Value: "1.5"}, nil), ShouldBeNil)
			})
		})

		Convey("When an argument list is collected", func() {
			Convey("Then an empty list yields no map at all", func() {
				So(argValues(nil, nil), ShouldBeNil)
			})

			Convey("Then each argument is keyed by name", func() {
				out := argValues([]*ast.Argument{
					{Name: &ast.Name{Value: "first"}, Value: &ast.IntValue{Value: "3"}},
					{Name: &ast.Name{Value: "after"}, Value: &ast.StringValue{Value: "cur"}},
				}, nil)
				So(out, ShouldResemble, map[string]any{"first": 3, "after": "cur"})
			})
		})

		Convey("When a cursor argument is coerced to a string", func() {
			Convey("Then non-string values become the empty string", func() {
				So(argString("cur"), ShouldEqual, "cur")
				So(argString(nil), ShouldEqual, "")
				So(argString(7), ShouldEqual, "")
			})
		})
	})
}

func TestSelectionHelpers(t *testing.T) {
	Convey("Given a connection's selection set", t, func() {
		Convey("When totalCount is directly selected", func() {
			sels := []selection{{name: "edges"}, {name: "totalCount"}}

			Convey("Then it is detected so the total is computed only on demand", func() {
				So(selectionHasField(sels, "totalCount"), ShouldBeTrue)
				So(selectionHasField(sels, "pageInfo"), ShouldBeFalse)
				So(selectionHasField(nil, "totalCount"), ShouldBeFalse)
			})
		})

		Convey("When node fields are dug out of edges → node", func() {
			sels := []selection{{
				name: "edges",
				sub:  []selection{{name: "node", sub: []selection{{name: "entityId"}, {name: "name"}}}},
			}}

			Convey("Then the node-level fields are returned", func() {
				got := nodeSelections(sels)
				So(got, ShouldHaveLength, 2)
				So(got[0].name, ShouldEqual, "entityId")
			})
		})

		Convey("When the selection has no edges or no node", func() {
			Convey("Then no node selections are found", func() {
				So(nodeSelections(nil), ShouldBeNil)
				So(nodeSelections([]selection{{name: "totalCount"}}), ShouldBeNil)
				So(nodeSelections([]selection{{name: "edges", sub: []selection{{name: "cursor"}}}}), ShouldBeNil)
			})
		})

		Convey("When resolve info carries no field ASTs", func() {
			Convey("Then no selections are produced", func() {
				So(selectionsFromInfo(graphql.ResolveInfo{}), ShouldBeNil)
			})
		})

		Convey("When a selection set is nil", func() {
			Convey("Then collecting yields nothing", func() {
				So(collectSelections(nil, nil, nil), ShouldBeNil)
			})
		})

		Convey("When selections arrive through inline fragments and named spreads", func() {
			frag := &ast.FragmentDefinition{
				Name: &ast.Name{Value: "F"},
				SelectionSet: &ast.SelectionSet{Selections: []ast.Selection{
					&ast.Field{Name: &ast.Name{Value: "fromSpread"}},
				}},
			}
			set := &ast.SelectionSet{Selections: []ast.Selection{
				&ast.Field{Name: &ast.Name{Value: "direct"}},
				&ast.InlineFragment{SelectionSet: &ast.SelectionSet{Selections: []ast.Selection{
					&ast.Field{Name: &ast.Name{Value: "fromInline"}},
				}}},
				&ast.FragmentSpread{Name: &ast.Name{Value: "F"}},
				&ast.FragmentSpread{Name: &ast.Name{Value: "Undefined"}},
			}}

			got := collectSelections(set, map[string]ast.Definition{"F": frag}, nil)

			Convey("Then fragment contents are flattened in with the direct fields", func() {
				names := make([]string, 0, len(got))
				for _, s := range got {
					names = append(names, s.name)
				}
				So(names, ShouldResemble, []string{"direct", "fromInline", "fromSpread"})
			})
		})
	})
}

func TestRelationshipDepth(t *testing.T) {
	Convey("Given nested relationship selections", t, func() {
		metas := map[string]typeMeta{
			"product": {
				internalName: "product",
				attrByField:  map[string]attrMeta{"name": {}},
				relByField:   map[string]relMeta{"similar": {otherType: "product"}},
			},
		}

		Convey("When the selection contains no relationship fields", func() {
			Convey("Then the depth is zero", func() {
				So(relationshipDepth([]selection{{name: "name"}}, "product", metas), ShouldEqual, 0)
			})
		})

		Convey("When relationships are nested through edges/node wrappers", func() {
			// product → similar → similar
			inner := []selection{{
				name: "similar",
				sub: []selection{{name: "edges", sub: []selection{
					{name: "node", sub: []selection{{name: "entityId"}}},
				}}},
			}}
			outer := []selection{{
				name: "similar",
				sub: []selection{{name: "edges", sub: []selection{
					{name: "node", sub: inner},
				}}},
			}}

			Convey("Then the deepest chain is counted independent of the wrappers", func() {
				So(relationshipDepth(outer, "product", metas), ShouldEqual, 2)
			})
		})

		Convey("When the type is not in the schema", func() {
			Convey("Then the depth is zero rather than panicking", func() {
				So(relationshipDepth([]selection{{name: "similar"}}, "ghost", metas), ShouldEqual, 0)
			})
		})
	})
}

func TestAddRelField(t *testing.T) {
	Convey("Given a type whose relationship fields are being registered", t, func() {
		newMetas := func() map[string]typeMeta {
			return map[string]typeMeta{"product": {
				internalName: "product",
				attrByField:  map[string]attrMeta{"supplier": {}},
				relByField:   map[string]relMeta{},
			}}
		}

		Convey("When a relationship field collides with an attribute field", func() {
			metas := newMetas()
			addRelField(metas, "product", "supplier", relMeta{otherType: "supplier"})

			Convey("Then it is suffixed so the attribute field is not shadowed", func() {
				_, ok := metas["product"].relByField["supplier_rel"]
				So(ok, ShouldBeTrue)
				_, plain := metas["product"].relByField["supplier"]
				So(plain, ShouldBeFalse)
			})
		})

		Convey("When two relationships want the same field name", func() {
			metas := newMetas()
			addRelField(metas, "product", "peers", relMeta{defID: "first", otherType: "product"})
			addRelField(metas, "product", "peers", relMeta{defID: "second", otherType: "product"})

			Convey("Then the first registration wins and the second is dropped", func() {
				So(metas["product"].relByField["peers"].defID, ShouldEqual, "first")
			})
		})

		Convey("When the target type, field name, or other endpoint is missing", func() {
			metas := newMetas()
			addRelField(metas, "ghost", "x", relMeta{otherType: "product"})
			addRelField(metas, "product", "", relMeta{otherType: "product"})
			addRelField(metas, "product", "y", relMeta{otherType: ""})

			Convey("Then nothing is registered", func() {
				So(metas["product"].relByField, ShouldBeEmpty)
			})
		})
	})
}

func TestProjectValues(t *testing.T) {
	Convey("Given stored values projected onto GraphQL scalars", t, func() {
		Convey("When a single-valued attribute has no value", func() {
			Convey("Then the field resolves to null", func() {
				So(projectValues(nil, attrMeta{dataType: valueobjects.DataTypeString}), ShouldBeNil)
			})
		})

		Convey("When a multi-valued attribute has no values", func() {
			Convey("Then the field resolves to an empty list, not null", func() {
				got := projectValues(nil, attrMeta{dataType: valueobjects.DataTypeString, multi: true})
				So(got, ShouldResemble, []any{})
			})
		})

		Convey("When a multi-valued attribute has several values", func() {
			vals := []valueobjects.Value{
				valueobjects.NewIntegerValue(1),
				valueobjects.NewIntegerValue(2),
			}
			got := projectValues(vals, attrMeta{dataType: valueobjects.DataTypeInteger, multi: true})

			Convey("Then every value is projected into the list", func() {
				So(got, ShouldResemble, []any{1, 2})
			})
		})

		Convey("When a single-valued attribute has a value", func() {
			got := projectValues(
				[]valueobjects.Value{valueobjects.NewStringValue("widget")},
				attrMeta{dataType: valueobjects.DataTypeString},
			)

			Convey("Then the first value is projected", func() {
				So(got, ShouldEqual, "widget")
			})
		})

		Convey("When scalars of each supported kind are projected", func() {
			Convey("Then each maps onto its native Go type", func() {
				So(projectScalar(valueobjects.NewBoolValue(true), valueobjects.DataTypeBool), ShouldEqual, true)
				So(projectScalar(valueobjects.NewIntegerValue(5), valueobjects.DataTypeInteger), ShouldEqual, 5)
				So(projectScalar(valueobjects.NewFloatValue(1.5), valueobjects.DataTypeFloat), ShouldEqual, 1.5)
				// Everything else (string, enum, decimal, date, …) renders as text.
				So(projectScalar(valueobjects.NewStringValue("x"), valueobjects.DataTypeString), ShouldEqual, "x")
				So(projectScalar(valueobjects.NewEnumValue("red"), valueobjects.DataTypeEnum), ShouldEqual, "red")
			})
		})
	})
}

func TestGQLTypeMapping(t *testing.T) {
	Convey("Given attribute data types mapped onto GraphQL scalars", t, func() {
		Convey("When each supported data type is mapped", func() {
			Convey("Then it uses the matching GraphQL scalar", func() {
				So(gqlScalar(valueobjects.DataTypeBool), ShouldEqual, graphql.Boolean)
				So(gqlScalar(valueobjects.DataTypeInteger), ShouldEqual, graphql.Int)
				So(gqlScalar(valueobjects.DataTypeFloat), ShouldEqual, graphql.Float)
				So(gqlScalar(valueobjects.DataTypeString), ShouldEqual, graphql.String)
				So(gqlScalar(valueobjects.DataTypeDecimal), ShouldEqual, graphql.String)
			})
		})

		Convey("When the attribute is multi-valued", func() {
			Convey("Then the scalar is wrapped in a list", func() {
				single := gqlType(valueobjects.DataTypeInteger, false)
				multi := gqlType(valueobjects.DataTypeInteger, true)

				So(single, ShouldEqual, graphql.Int)
				list, ok := multi.(*graphql.List)
				So(ok, ShouldBeTrue)
				So(list.OfType, ShouldEqual, graphql.Int)
			})
		})
	})
}

func TestConnectionShape(t *testing.T) {
	Convey("Given edges assembled into a Relay connection", t, func() {
		Convey("When the page is empty", func() {
			conn := connection([]map[string]any{}, false, false, nil)
			pi := conn["pageInfo"].(map[string]any)

			Convey("Then the cursors are null and totalCount is omitted as null", func() {
				So(pi["startCursor"], ShouldBeNil)
				So(pi["endCursor"], ShouldBeNil)
				So(pi["hasNextPage"], ShouldBeFalse)
				So(pi["hasPreviousPage"], ShouldBeFalse)
				So(conn["totalCount"], ShouldBeNil)
			})
		})

		Convey("When the page has edges and a requested total", func() {
			total := 9
			conn := connection([]map[string]any{
				{"cursor": "c1"}, {"cursor": "c2"}, {"cursor": "c3"},
			}, true, true, &total)
			pi := conn["pageInfo"].(map[string]any)

			Convey("Then the boundary cursors bracket the page and the total is reported", func() {
				So(pi["startCursor"], ShouldEqual, "c1")
				So(pi["endCursor"], ShouldEqual, "c3")
				So(pi["hasNextPage"], ShouldBeTrue)
				So(pi["hasPreviousPage"], ShouldBeTrue)
				So(conn["totalCount"], ShouldEqual, 9)
			})
		})
	})
}
