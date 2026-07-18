package flexitype_test

import (
	"context"
	"encoding/json"
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

// gqlDataPG navigates a GraphQL result's data as nested maps (root-package twin
// of the memory suite's gqlData helper).
func gqlDataPG(res interface{}) map[string]interface{} {
	m, ok := res.(map[string]interface{})
	So(ok, ShouldBeTrue)
	return m
}

// TestGraphQLWindowedRelationshipConnectionsParityPostgres re-runs the
// per-parent windowed relationship-connection suite
// (infrastructure/memory/graphql_window_test.go) against Postgres, driving the
// read-only GraphQL engine exactly like the HTTP layer (an Interactors on the
// context). It proves the first:N windowing SQL returns the same nodes/pageInfo
// per parent, resolves each relationship FIELD against only its own definition,
// gathers symmetric peers from both link sides, and resumes from a nested
// cursor with no gaps or repeats — over the real relationship store.
func TestGraphQLWindowedRelationshipConnectionsParityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	if err := flexitype.New(pool).Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given products, suppliers and two product→supplier relationships with a fan-out (Postgres)", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		// A FRESH service (hence a fresh GraphQL engine with an empty per-tenant
		// schema cache) is built inside each Convey leaf, mirroring the memory
		// twin which constructs flexitype.NewInMemory() per leaf. Sharing one
		// engine across leaves would leave its compiled schema pointing at the
		// previous leaf's relationship-definition IDs — truncateAll recreates the
		// definitions with new IDs via raw SQL, which fires no invalidation event
		// — and a nested relationship field would then resolve against a stale
		// definition and come back empty. That is a test-harness artifact of
		// raw-truncating under a live engine, not a memory-vs-Postgres divergence.
		svc := flexitype.New(pool)
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		supplier, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "supplier", DisplayName: "Supplier"})
		So(err, ShouldBeNil)

		mkAttr := func(typeID, name string) string {
			a, e := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: "string",
			})
			So(e, ShouldBeNil)
			return a.ID.String()
		}
		prodName := mkAttr(product.ID.String(), "name")
		supName := mkAttr(supplier.ID.String(), "name")

		set := func(typeID, entity, attrID, val string) {
			raw, _ := json.Marshal(val)
			_, e := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: entity, TypeDefinitionID: typeID, Value: raw,
			})
			So(e, ShouldBeNil)
		}

		// Two DISTINCT relationships between the same pair of types, so a product
		// object carries two relationship fields — the case that used to re-run
		// the (definition-agnostic) all-links query once per field.
		suppliedBy, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "supplied_by", DisplayName: "Supplied by",
			ParentTypeID: product.ID.String(), ChildTypeID: supplier.ID.String(),
			ChildLabel: "suppliers", ParentLabel: "supplies",
		})
		So(err, ShouldBeNil)
		auditedBy, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "audited_by", DisplayName: "Audited by",
			ParentTypeID: product.ID.String(), ChildTypeID: supplier.ID.String(),
			ChildLabel: "auditors", ParentLabel: "audits",
		})
		So(err, ShouldBeNil)
		// A symmetric self-relation on product: the windowed resolver must gather
		// peers from BOTH sides of each link.
		similar, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "similar", DisplayName: "Similar", Kind: "symmetric",
			ParentTypeID: product.ID.String(), ChildTypeID: product.ID.String(),
		})
		So(err, ShouldBeNil)

		// Three products; five suppliers with ascending ids (s1..s5). String
		// order is the windowing/keyset order, so first:2 over p1 must be s1,s2.
		for _, p := range []string{"p1", "p2", "p3"} {
			set(product.ID.String(), p, prodName, p)
		}
		for _, s := range []string{"s1", "s2", "s3", "s4", "s5"} {
			set(supplier.ID.String(), s, supName, s)
		}

		link := func(defID, parent, child string) {
			_, e := svc.Interactors(ctx).Relationships().Link(ctx, apprelationship.LinkInput{
				DefinitionID: defID, ParentEntity: parent, ChildEntity: child,
			})
			So(e, ShouldBeNil)
		}
		// p1 has five suppliers, p2 has two, p3 has none — a fan-out that must
		// NOT be fully materialized for a first:2 page.
		for _, s := range []string{"s1", "s2", "s3", "s4", "s5"} {
			link(suppliedBy.ID.String(), "p1", s)
		}
		link(suppliedBy.ID.String(), "p2", "s2")
		link(suppliedBy.ID.String(), "p2", "s4")
		// A single, DIFFERENT auditor for p1 under the other definition.
		link(auditedBy.ID.String(), "p1", "s5")
		// Symmetric peers: p1—p2 and p2—p3, so p2 is a peer via both sides.
		link(similar.ID.String(), "p1", "p2")
		link(similar.ID.String(), "p2", "p3")

		eng := svc.GraphQLEngine()
		exec := func(query string) *gql.Result {
			// A fresh Interactors per Execute, on the context, exactly like the HTTP layer.
			return eng.Execute(application.WithInteractors(ctx, svc.Interactors(ctx)), query, nil)
		}
		productsByID := func(res *gql.Result) map[string]map[string]interface{} {
			So(res.Errors, ShouldBeEmpty)
			conn := gqlDataPG(res.Data)["product"].(map[string]interface{})
			out := map[string]map[string]interface{}{}
			for _, e := range conn["edges"].([]interface{}) {
				node := e.(map[string]interface{})["node"].(map[string]interface{})
				out[node["entityId"].(string)] = node
			}
			return out
		}
		childIDs := func(node map[string]interface{}, field string) []string {
			conn := node[field].(map[string]interface{})
			var ids []string
			for _, e := range conn["edges"].([]interface{}) {
				ids = append(ids, e.(map[string]interface{})["node"].(map[string]interface{})["entityId"].(string))
			}
			return ids
		}

		Convey("When paging both nested connections first:2 over every product in one query", func() {
			res := exec(`{ product {
				edges { node {
					entityId
					suppliers(first: 2) { totalCount pageInfo { hasNextPage endCursor } edges { node { entityId } } }
					auditors(first: 2)  { totalCount pageInfo { hasNextPage } edges { node { entityId } } }
				} }
			} }`)

			Convey("Then each parent returns its own N-item window with the right total and hasNextPage", func() {
				byID := productsByID(res)

				// p1: five suppliers -> first two by id, more remains, total 5.
				p1sup := byID["p1"]["suppliers"].(map[string]interface{})
				So(p1sup["totalCount"], ShouldEqual, 5)
				So(p1sup["pageInfo"].(map[string]interface{})["hasNextPage"], ShouldBeTrue)
				So(childIDs(byID["p1"], "suppliers"), ShouldResemble, []string{"s1", "s2"})

				// The auditors field is filtered to its OWN definition: only s5,
				// and the suppliers field never leaked it.
				p1aud := byID["p1"]["auditors"].(map[string]interface{})
				So(p1aud["totalCount"], ShouldEqual, 1)
				So(p1aud["pageInfo"].(map[string]interface{})["hasNextPage"], ShouldBeFalse)
				So(childIDs(byID["p1"], "auditors"), ShouldResemble, []string{"s5"})

				// p2: exactly two suppliers -> the whole set, no next page.
				p2sup := byID["p2"]["suppliers"].(map[string]interface{})
				So(p2sup["totalCount"], ShouldEqual, 2)
				So(p2sup["pageInfo"].(map[string]interface{})["hasNextPage"], ShouldBeFalse)
				So(childIDs(byID["p2"], "suppliers"), ShouldResemble, []string{"s2", "s4"})

				// p3: no links at all -> an empty, zero-total connection.
				p3sup := byID["p3"]["suppliers"].(map[string]interface{})
				So(p3sup["totalCount"], ShouldEqual, 0)
				So(p3sup["pageInfo"].(map[string]interface{})["hasNextPage"], ShouldBeFalse)
				So(len(p3sup["edges"].([]interface{})), ShouldEqual, 0)
			})
		})

		Convey("When windowing a symmetric relationship, peers come from both link sides", func() {
			res := exec(`{ product {
				edges { node { entityId similar(first: 5) { totalCount edges { node { entityId } } } } }
			} }`)

			Convey("Then a self on either side of a link still sees the opposite endpoint", func() {
				byID := productsByID(res)
				So(childIDs(byID["p1"], "similar"), ShouldResemble, []string{"p2"})
				So(childIDs(byID["p2"], "similar"), ShouldResemble, []string{"p1", "p3"})
				So(byID["p2"]["similar"].(map[string]interface{})["totalCount"], ShouldEqual, 2)
				So(childIDs(byID["p3"], "similar"), ShouldResemble, []string{"p2"})
			})
		})

		Convey("When resuming a nested connection from its endCursor", func() {
			first := exec(`{ product {
				edges { node { entityId suppliers(first: 2) { pageInfo { endCursor } edges { node { entityId } } } } }
			} }`)
			cursor := productsByID(first)["p1"]["suppliers"].(map[string]interface{})["pageInfo"].(map[string]interface{})["endCursor"].(string)
			So(cursor, ShouldNotBeEmpty)

			res := exec(`{ product {
				edges { node { entityId suppliers(first: 2, after: "` + cursor + `") { pageInfo { hasNextPage } edges { node { entityId } } } } }
			} }`)

			Convey("Then the next window continues after the cursor with no gaps or repeats", func() {
				p1 := productsByID(res)["p1"]
				So(childIDs(p1, "suppliers"), ShouldResemble, []string{"s3", "s4"})
				So(p1["suppliers"].(map[string]interface{})["pageInfo"].(map[string]interface{})["hasNextPage"], ShouldBeTrue)
			})
		})
	})
}
