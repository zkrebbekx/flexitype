package gql

import (
	"testing"

	"github.com/graphql-go/graphql/language/ast"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestSDLType(t *testing.T) {
	Convey("Given an attribute being rendered into the subgraph SDL", t, func() {
		Convey("When it carries each data type", func() {
			Convey("Then the SDL type matches the executable schema's scalar", func() {
				So(sdlType(attrMeta{dataType: valueobjects.DataTypeBool}), ShouldEqual, "Boolean")
				So(sdlType(attrMeta{dataType: valueobjects.DataTypeInteger}), ShouldEqual, "Int")
				So(sdlType(attrMeta{dataType: valueobjects.DataTypeFloat}), ShouldEqual, "Float")
				So(sdlType(attrMeta{dataType: valueobjects.DataTypeString}), ShouldEqual, "String")
			})

			Convey("Then a type with no scalar of its own renders as String, as it executes", func() {
				// decimal, date, enum, url, … all execute as String.
				So(sdlType(attrMeta{dataType: valueobjects.DataTypeDecimal}), ShouldEqual, "String")
				So(sdlType(attrMeta{dataType: valueobjects.DataTypeDate}), ShouldEqual, "String")
			})
		})

		Convey("When it is multi-valued", func() {
			Convey("Then it renders as a list of that scalar", func() {
				So(sdlType(attrMeta{dataType: valueobjects.DataTypeInteger, multi: true}), ShouldEqual, "[Int]")
				So(sdlType(attrMeta{dataType: valueobjects.DataTypeString, multi: true}), ShouldEqual, "[String]")
			})
		})
	})
}

func TestFederationSDLSkipsUnreadableRelationshipTarget(t *testing.T) {
	Convey("Given a type whose relationship points at a type the caller cannot read", t, func() {
		metas := map[string]typeMeta{
			"product": {
				internalName: "product",
				attrByField:  map[string]attrMeta{"name": {dataType: valueobjects.DataTypeString}},
				relByField: map[string]relMeta{
					"suppliers": {otherType: "supplier"},
				},
			},
		}

		Convey("When the SDL is rendered", func() {
			sdl := federationSDL(metas)

			Convey("Then the field is omitted, so the SDL only names types it declares", func() {
				So(sdl, ShouldContainSubstring, `type Product @key(fields: "entityId")`)
				So(sdl, ShouldNotContainSubstring, "suppliers")
				So(sdl, ShouldNotContainSubstring, "Supplier")
			})
		})
	})
}

func TestParseAnyLiteral(t *testing.T) {
	Convey("Given representations written inline in a query rather than passed as a variable", t, func() {
		Convey("When an object literal is parsed", func() {
			obj := &ast.ObjectValue{Fields: []*ast.ObjectField{
				{Name: &ast.Name{Value: "__typename"}, Value: &ast.StringValue{Value: "Product"}},
				{Name: &ast.Name{Value: "entityId"}, Value: &ast.StringValue{Value: "prod1"}},
			}}

			Convey("Then it becomes the same map a variable would produce", func() {
				got := parseAnyLiteral(obj).(map[string]any)
				So(got["__typename"], ShouldEqual, "Product")
				So(got["entityId"], ShouldEqual, "prod1")
			})
		})

		Convey("When each other literal kind is parsed", func() {
			Convey("Then the Go value carries the literal's own value", func() {
				So(parseAnyLiteral(&ast.BooleanValue{Value: true}), ShouldEqual, true)
				So(parseAnyLiteral(&ast.IntValue{Value: "7"}), ShouldEqual, "7")
				So(parseAnyLiteral(&ast.FloatValue{Value: "1.5"}), ShouldEqual, "1.5")
				So(parseAnyLiteral(&ast.EnumValue{Value: "ACTIVE"}), ShouldEqual, "ACTIVE")
			})
		})

		Convey("When a list literal is parsed", func() {
			list := &ast.ListValue{Values: []ast.Value{
				&ast.StringValue{Value: "a"}, &ast.StringValue{Value: "b"},
			}}

			Convey("Then every element is converted in order", func() {
				So(parseAnyLiteral(list), ShouldResemble, []any{"a", "b"})
			})
		})

		Convey("When a literal kind with no value is parsed", func() {
			Convey("Then it becomes nil rather than a zero value that reads as data", func() {
				So(parseAnyLiteral(&ast.Variable{Name: &ast.Name{Value: "r"}}), ShouldBeNil)
			})
		})
	})
}

func TestDedupeRepresentations(t *testing.T) {
	Convey("Given a batch naming the same entity more than once", t, func() {
		Convey("When the ids are deduped", func() {
			Convey("Then each id is read once, in first-seen order", func() {
				So(dedupe([]string{"b", "a", "b", "c", "a"}), ShouldResemble, []string{"b", "a", "c"})
			})
		})

		Convey("When the batch holds no repeats", func() {
			Convey("Then the order is unchanged", func() {
				So(dedupe([]string{"a", "b"}), ShouldResemble, []string{"a", "b"})
			})
		})
	})
}
