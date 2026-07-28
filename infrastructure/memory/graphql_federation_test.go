package memory_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	"github.com/zkrebbekx/flexitype/application/gql"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// federationFixture builds a two-type schema with values and a relationship,
// and returns an executor over the service's GraphQL engine.
func federationFixture(federated bool) (context.Context, func(string, map[string]any) *gql.Result) {
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	var svc *flexitype.Service
	if federated {
		svc = flexitype.NewInMemory(flexitype.WithGraphQLFederation())
	} else {
		svc = flexitype.NewInMemory()
	}
	it := svc.Interactors(ctx)

	product, err := it.TypeDefinitions().Create(ctx,
		apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
	So(err, ShouldBeNil)
	supplier, err := it.TypeDefinitions().Create(ctx,
		apptypedef.CreateInput{InternalName: "supplier", DisplayName: "Supplier"})
	So(err, ShouldBeNil)

	mk := func(typeID, name, dt string, multi bool) string {
		a, e := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: name, DisplayName: name,
			DataType: dt, MultiValued: multi,
		})
		So(e, ShouldBeNil)
		return a.ID.String()
	}
	nameA := mk(product.ID.String(), "name", "string", false)
	stockA := mk(product.ID.String(), "stock", "integer", false)
	tagsA := mk(product.ID.String(), "tags", "string", true)
	regionA := mk(supplier.ID.String(), "region", "string", false)

	def, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
		InternalName: "supplied_by", DisplayName: "Supplied by",
		ParentTypeID: product.ID.String(), ChildTypeID: supplier.ID.String(),
		ChildLabel: "suppliers", ParentLabel: "products",
	})
	So(err, ShouldBeNil)

	set := func(typeID, entity, attrID, raw string) {
		_, e := it.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: attrID, EntityID: entity, TypeDefinitionID: typeID,
			Value: json.RawMessage(raw),
		})
		So(e, ShouldBeNil)
	}
	set(product.ID.String(), "prod1", nameA, `"Widget"`)
	set(product.ID.String(), "prod1", stockA, `7`)
	set(product.ID.String(), "prod1", tagsA, `"blue"`)
	set(product.ID.String(), "prod2", nameA, `"Gadget"`)
	set(supplier.ID.String(), "sup1", regionA, `"EU"`)

	_, err = it.Relationships().Link(ctx, apprelationship.LinkInput{
		DefinitionID: def.ID.String(), ParentEntity: "prod1", ChildEntity: "sup1",
	})
	So(err, ShouldBeNil)

	eng := svc.GraphQLEngine()
	exec := func(query string, vars map[string]any) *gql.Result {
		return eng.Execute(application.WithInteractors(ctx, svc.Interactors(ctx)), query, vars)
	}
	return ctx, exec
}

// TestGraphQLFederationContract covers the subgraph contract a federated
// gateway composes against: the SDL, the entity resolver, and the fact that
// neither exists unless the deployment asks for them.
func TestGraphQLFederationContract(t *testing.T) {
	Convey("Given a service that has not enabled federation", t, func() {
		_, exec := federationFixture(false)

		Convey("When a gateway asks for the subgraph SDL", func() {
			res := exec(`{ _service { sdl } }`, nil)

			Convey("Then the field does not exist, so composition fails loudly", func() {
				So(res.Errors, ShouldNotBeEmpty)
				So(res.Errors[0].Message, ShouldContainSubstring, "_service")
			})
		})

		Convey("When a gateway asks it to resolve entities", func() {
			res := exec(`{ _entities(representations: []) { __typename } }`, nil)

			Convey("Then that field does not exist either", func() {
				So(res.Errors, ShouldNotBeEmpty)
			})
		})

		Convey("When an ordinary query runs", func() {
			res := exec(`{ product { edges { node { entityId name } } } }`, nil)

			Convey("Then the standalone schema is unchanged", func() {
				So(res.Errors, ShouldBeEmpty)
			})
		})
	})

	Convey("Given a service with federation enabled", t, func() {
		_, exec := federationFixture(true)

		Convey("When a gateway reads the subgraph SDL", func() {
			res := exec(`{ _service { sdl } }`, nil)

			Convey("Then every entity type carries a key on entityId", func() {
				So(res.Errors, ShouldBeEmpty)
				sdl := res.Data.(map[string]any)["_service"].(map[string]any)["sdl"].(string)
				So(sdl, ShouldContainSubstring, `type Product @key(fields: "entityId")`)
				So(sdl, ShouldContainSubstring, `type Supplier @key(fields: "entityId")`)
			})

			Convey("Then the attribute types match the executable schema", func() {
				sdl := res.Data.(map[string]any)["_service"].(map[string]any)["sdl"].(string)
				So(sdl, ShouldContainSubstring, "name: String")
				So(sdl, ShouldContainSubstring, "stock: Int")
				So(sdl, ShouldContainSubstring, "tags: [String]")
			})

			Convey("Then the relationship connection and the root fields are declared", func() {
				sdl := res.Data.(map[string]any)["_service"].(map[string]any)["sdl"].(string)
				So(sdl, ShouldContainSubstring, "suppliers(first: Int, after: String): SupplierConnection")
				So(sdl, ShouldContainSubstring, "product(first: Int, after: String, filter: String): ProductConnection")
			})

			Convey("Then the federation additions are absent, as the spec requires", func() {
				sdl := res.Data.(map[string]any)["_service"].(map[string]any)["sdl"].(string)
				So(sdl, ShouldNotContainSubstring, "_entities")
				So(sdl, ShouldNotContainSubstring, "_Any")
				So(sdl, ShouldNotContainSubstring, "_Entity")
				So(sdl, ShouldNotContainSubstring, "_service")
			})
		})

		Convey("When a gateway resolves one representation", func() {
			res := exec(`query($r: [_Any!]!) {
				_entities(representations: $r) {
					__typename
					... on Product { entityId name stock tags }
				}
			}`, map[string]any{"r": []any{
				map[string]any{"__typename": "Product", "entityId": "prod1"},
			}})

			Convey("Then the entity comes back with its attribute values", func() {
				So(res.Errors, ShouldBeEmpty)
				items := res.Data.(map[string]any)["_entities"].([]any)
				So(items, ShouldHaveLength, 1)
				node := items[0].(map[string]any)
				So(node["__typename"], ShouldEqual, "Product")
				So(node["entityId"], ShouldEqual, "prod1")
				So(node["name"], ShouldEqual, "Widget")
				So(node["stock"], ShouldEqual, 7)
				So(node["tags"], ShouldResemble, []any{"blue"})
			})
		})

		Convey("When a gateway mixes types in one batch", func() {
			res := exec(`query($r: [_Any!]!) {
				_entities(representations: $r) {
					__typename
					... on Product { entityId name }
					... on Supplier { entityId region }
				}
			}`, map[string]any{"r": []any{
				map[string]any{"__typename": "Supplier", "entityId": "sup1"},
				map[string]any{"__typename": "Product", "entityId": "prod2"},
				map[string]any{"__typename": "Product", "entityId": "prod1"},
			}})

			Convey("Then each entity comes back at its own position, in the order asked", func() {
				So(res.Errors, ShouldBeEmpty)
				items := res.Data.(map[string]any)["_entities"].([]any)
				So(items, ShouldHaveLength, 3)
				So(items[0].(map[string]any)["region"], ShouldEqual, "EU")
				So(items[1].(map[string]any)["name"], ShouldEqual, "Gadget")
				So(items[2].(map[string]any)["name"], ShouldEqual, "Widget")
			})
		})

		Convey("When the same entity appears twice in one batch", func() {
			res := exec(`query($r: [_Any!]!) {
				_entities(representations: $r) { ... on Product { entityId name } }
			}`, map[string]any{"r": []any{
				map[string]any{"__typename": "Product", "entityId": "prod1"},
				map[string]any{"__typename": "Product", "entityId": "prod1"},
			}})

			Convey("Then both positions are filled with the same answer", func() {
				So(res.Errors, ShouldBeEmpty)
				items := res.Data.(map[string]any)["_entities"].([]any)
				So(items, ShouldHaveLength, 2)
				So(items[0].(map[string]any)["name"], ShouldEqual, "Widget")
				So(items[1].(map[string]any)["name"], ShouldEqual, "Widget")
			})
		})

		Convey("When a representation carries a relationship selection", func() {
			res := exec(`query($r: [_Any!]!) {
				_entities(representations: $r) {
					... on Product {
						entityId
						suppliers { edges { node { entityId region } } }
					}
				}
			}`, map[string]any{"r": []any{
				map[string]any{"__typename": "Product", "entityId": "prod1"},
			}})

			Convey("Then the relationship resolves through the same batched path", func() {
				So(res.Errors, ShouldBeEmpty)
				node := res.Data.(map[string]any)["_entities"].([]any)[0].(map[string]any)
				edges := node["suppliers"].(map[string]any)["edges"].([]any)
				So(edges, ShouldHaveLength, 1)
				So(edges[0].(map[string]any)["node"].(map[string]any)["region"], ShouldEqual, "EU")
			})
		})

		Convey("When a representation names a type this subgraph does not have", func() {
			res := exec(`query($r: [_Any!]!) {
				_entities(representations: $r) { __typename }
			}`, map[string]any{"r": []any{
				map[string]any{"__typename": "Invoice", "entityId": "inv1"},
			}})

			Convey("Then it errors naming the type rather than answering null", func() {
				So(res.Errors, ShouldNotBeEmpty)
				So(res.Errors[0].Message, ShouldContainSubstring, "Invoice")
			})
		})

		Convey("When a representation omits the key field", func() {
			res := exec(`query($r: [_Any!]!) {
				_entities(representations: $r) { __typename }
			}`, map[string]any{"r": []any{
				map[string]any{"__typename": "Product"},
			}})

			Convey("Then it errors rather than reading every entity of the type", func() {
				So(res.Errors, ShouldNotBeEmpty)
				So(res.Errors[0].Message, ShouldContainSubstring, "entityId")
			})
		})

		Convey("When a batch exceeds the representation cap", func() {
			reps := make([]any, 501)
			for i := range reps {
				reps[i] = map[string]any{"__typename": "Product", "entityId": "prod1"}
			}
			res := exec(`query($r: [_Any!]!) {
				_entities(representations: $r) { __typename }
			}`, map[string]any{"r": reps})

			Convey("Then it is refused: one list argument otherwise escapes the cost guard", func() {
				So(res.Errors, ShouldNotBeEmpty)
				So(res.Errors[0].Message, ShouldContainSubstring, "too many representations")
			})
		})

		Convey("When a representation is written inline instead of passed as a variable", func() {
			res := exec(`{
				_entities(representations: [{__typename: "Product", entityId: "prod1"}]) {
					... on Product { entityId name }
				}
			}`, nil)

			Convey("Then the inline literal resolves exactly as the variable does", func() {
				So(res.Errors, ShouldBeEmpty)
				node := res.Data.(map[string]any)["_entities"].([]any)[0].(map[string]any)
				So(node["entityId"], ShouldEqual, "prod1")
				So(node["name"], ShouldEqual, "Widget")
			})
		})

		Convey("When an entity id this subgraph holds no values for is asked for", func() {
			res := exec(`query($r: [_Any!]!) {
				_entities(representations: $r) { ... on Product { entityId name } }
			}`, map[string]any{"r": []any{
				map[string]any{"__typename": "Product", "entityId": "nosuch"},
			}})

			Convey("Then the object comes back with null attributes, not an error", func() {
				So(res.Errors, ShouldBeEmpty)
				node := res.Data.(map[string]any)["_entities"].([]any)[0].(map[string]any)
				So(node["entityId"], ShouldEqual, "nosuch")
				So(node["name"], ShouldBeNil)
			})
		})
	})
}

// TestGraphQLFederationRespectsFieldACL proves federation is not a way around
// the field permissions every other read path enforces.
func TestGraphQLFederationRespectsFieldACL(t *testing.T) {
	Convey("Given a federated service and a caller who may not read one attribute", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory(flexitype.WithGraphQLFederation())
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		mk := func(name string) string {
			a, e := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: name,
				DisplayName: name, DataType: "string",
			})
			So(e, ShouldBeNil)
			return a.ID.String()
		}
		nameA := mk("name")
		costA := mk("cost")
		for _, v := range []struct{ attr, raw string }{{nameA, `"Widget"`}, {costA, `"secret"`}} {
			_, e := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: v.attr, EntityID: "prod1",
				TypeDefinitionID: product.ID.String(), Value: json.RawMessage(v.raw),
			})
			So(e, ShouldBeNil)
		}

		limited := uow.WithAccess(ctx, uow.Access{
			Attr: map[string]uow.Perm{"cost": uow.PermNone},
		})
		eng := svc.GraphQLEngine()
		exec := func(c context.Context, query string, vars map[string]any) *gql.Result {
			return eng.Execute(application.WithInteractors(c, svc.Interactors(c)), query, vars)
		}

		Convey("When that caller reads the subgraph SDL", func() {
			res := exec(limited, `{ _service { sdl } }`, nil)

			Convey("Then the unreadable attribute is not in the schema it composes", func() {
				So(res.Errors, ShouldBeEmpty)
				sdl := res.Data.(map[string]any)["_service"].(map[string]any)["sdl"].(string)
				So(sdl, ShouldContainSubstring, "name: String")
				So(strings.Contains(sdl, "cost:"), ShouldBeFalse)
			})
		})

		Convey("When that caller resolves the entity", func() {
			res := exec(limited, `query($r: [_Any!]!) {
				_entities(representations: $r) { ... on Product { entityId name } }
			}`, map[string]any{"r": []any{
				map[string]any{"__typename": "Product", "entityId": "prod1"},
			}})

			Convey("Then the readable attribute resolves and the hidden value never appears", func() {
				So(res.Errors, ShouldBeEmpty)
				node := res.Data.(map[string]any)["_entities"].([]any)[0].(map[string]any)
				So(node["name"], ShouldEqual, "Widget")
				So(node["cost"], ShouldBeNil)
			})
		})

		Convey("When that caller asks for the hidden attribute by name", func() {
			res := exec(limited, `query($r: [_Any!]!) {
				_entities(representations: $r) { ... on Product { cost } }
			}`, map[string]any{"r": []any{
				map[string]any{"__typename": "Product", "entityId": "prod1"},
			}})

			Convey("Then the field is unknown to that caller's schema", func() {
				So(res.Errors, ShouldNotBeEmpty)
				So(res.Errors[0].Message, ShouldContainSubstring, "cost")
			})
		})
	})
}
