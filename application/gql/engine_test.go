package gql

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

func TestEngineSubscriberContract(t *testing.T) {
	Convey("Given the engine acting as an internal-projection subscriber", t, func() {
		e := NewEngine()

		Convey("When it is asked to identify itself", func() {
			Convey("Then it reports a stable subscriber name", func() {
				So(e.Name(), ShouldEqual, "graphql-schema")
			})
		})

		Convey("When its event subscriptions are inspected", func() {
			types := e.EventTypes()

			Convey("Then it subscribes to every definition-changing event", func() {
				So(len(types), ShouldEqual, 12)
				So(types, ShouldContain, events.Type("flexitype.type_definition.created"))
				So(types, ShouldContain, events.Type("flexitype.attribute_definition.archived"))
			})
		})

		Convey("When a definition event arrives for a tenant", func() {
			e.mu.Lock()
			e.memo["acme"] = versionMemo{version: 7, fetchedAt: e.now()}
			e.mu.Unlock()

			err := e.Handle(context.Background(), events.Envelope{TenantID: "acme"})

			Convey("Then that tenant's version memo is dropped so it re-reads at once", func() {
				So(err, ShouldBeNil)
				e.mu.Lock()
				_, ok := e.memo["acme"]
				e.mu.Unlock()
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When an event arrives with no tenant", func() {
			e.mu.Lock()
			e.memo["acme"] = versionMemo{version: 7, fetchedAt: e.now()}
			e.mu.Unlock()

			err := e.Handle(context.Background(), events.Envelope{})

			Convey("Then it is ignored and no memo is disturbed", func() {
				So(err, ShouldBeNil)
				e.mu.Lock()
				_, ok := e.memo["acme"]
				e.mu.Unlock()
				So(ok, ShouldBeTrue)
			})
		})
	})
}

func TestResultErr(t *testing.T) {
	Convey("Given an engine-level failure that must not reach resolution", t, func() {
		Convey("When it is turned into a GraphQL result", func() {
			res := resultErr(errors.New("boom"))

			Convey("Then it carries the message as a formatted GraphQL error and no data", func() {
				So(res.Errors, ShouldHaveLength, 1)
				So(res.Errors[0].Message, ShouldEqual, "boom")
				So(res.Data, ShouldBeNil)
			})
		})
	})
}

// TestCheckQueryCostFragmentExpansion covers the expansion cases cost_test.go
// does not: a spread that multiplies selections, an inline fragment, and a
// spread naming a fragment that was never defined.
func TestCheckQueryCostFragmentExpansion(t *testing.T) {
	Convey("Given the query-cost guard", t, func() {
		Convey("When selections are amplified through a fragment spread", func() {
			var b strings.Builder
			b.WriteString("fragment F on Product {")
			for i := 0; i < 60; i++ {
				b.WriteString(" f")
				b.WriteString(strconv.Itoa(i))
				b.WriteString(": id")
			}
			b.WriteString(" }\n{")
			// Spreading the fragment many times multiplies the counted selections.
			for i := 0; i < 60; i++ {
				b.WriteString(" p")
				b.WriteString(strconv.Itoa(i))
				b.WriteString(": products { ...F }")
			}
			b.WriteString(" }")

			Convey("Then the expanded selection count is what gets rejected", func() {
				So(checkQueryCost(b.String()), ShouldNotBeNil)
			})
		})

		Convey("When an inline fragment nests selections", func() {
			q := "{ node { ... on Product { id name } } }"

			Convey("Then its selections are counted without error for a small document", func() {
				So(checkQueryCost(q), ShouldBeNil)
			})
		})

		Convey("When a spread names a fragment that does not exist", func() {
			Convey("Then the guard tolerates it and defers to graphql.Do", func() {
				So(checkQueryCost("{ products { ...Missing } }"), ShouldBeNil)
			})
		})
	})
}

func TestAccessSignature(t *testing.T) {
	Convey("Given callers with different permission sets", t, func() {
		Convey("When the caller is an admin", func() {
			Convey("Then every admin shares one schema-cache key", func() {
				So(accessSignature(uow.Access{Admin: true}), ShouldEqual, "admin")
				So(accessSignature(uow.Access{Admin: true, Attr: map[string]uow.Perm{"a": "read"}}),
					ShouldEqual, "admin")
			})
		})

		Convey("When the caller restricts no attributes", func() {
			Convey("Then it uses the open key", func() {
				So(accessSignature(uow.Access{}), ShouldEqual, "open")
				So(accessSignature(uow.Access{Attr: map[string]uow.Perm{}}), ShouldEqual, "open")
			})
		})

		Convey("When the caller restricts specific attributes", func() {
			sig := accessSignature(uow.Access{Attr: map[string]uow.Perm{
				"price": "none", "name": "read",
			}})

			Convey("Then the signature is deterministic regardless of map order", func() {
				So(sig, ShouldEqual, "name:read,price:none")

				// Same set, built in the other order, must key identically or the
				// cache would fragment per request.
				again := accessSignature(uow.Access{Attr: map[string]uow.Perm{
					"name": "read", "price": "none",
				}})
				So(again, ShouldEqual, sig)
			})
		})

		Convey("When two callers have different permission sets", func() {
			a := accessSignature(uow.Access{Attr: map[string]uow.Perm{"price": "none"}})
			b := accessSignature(uow.Access{Attr: map[string]uow.Perm{"price": "read"}})

			Convey("Then they key to different schemas so introspection stays scoped", func() {
				So(a, ShouldNotEqual, b)
			})
		})
	})
}
