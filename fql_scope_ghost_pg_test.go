package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// This file holds the regression harness for two query defects. Both tests
// run the same catalog and the same queries against the memory evaluator and
// the Postgres SQL compiler, in the style of fql_parity_pg_test.go, and
// assert the expected entity set on each backend plus cross-backend parity.
//
// Issue #474: valueScope pinned BOTH locale and channel on any attribute
// with (Localizable || Scopable). The write path stores '' in a dimension
// the attribute does not carry, so a query that supplies both dimensions
// excluded every value of a single-dimension attribute.
//
// Issue #475: the traversal had no counterpart-liveness guard. A dangling
// relationship made a value-less "ghost" counterpart match count()=0,
// `not has()` and negated type conditions — the last one with a backend
// divergence (Postgres NULL type excluded, memory "" type included).

// seedSingleDimensionScopes builds a "product" type with one
// localizable-only attribute (description), one scopable-only attribute
// (promo) and one localizable+scopable control (blurb).
//
//   - p1: description@locale=en, promo@channel=web, blurb@en/web
//   - p2: description@locale=de only
func seedSingleDimensionScopes(ctx context.Context, t *testing.T, svc *flexitype.Service) {
	t.Helper()
	it := svc.Interactors(ctx)

	pt, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
	if err != nil {
		t.Fatalf("create type: %v", err)
	}
	mkScoped := func(name string, localizable, scopable bool) string {
		a, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: pt.ID.String(), InternalName: name, DisplayName: name,
			DataType: "string", Localizable: localizable, Scopable: scopable,
		})
		if err != nil {
			t.Fatalf("create attr %s: %v", name, err)
		}
		return a.ID.String()
	}
	descID := mkScoped("description", true, false)
	promoID := mkScoped("promo", false, true)
	blurbID := mkScoped("blurb", true, true)

	setScoped := func(attrID, entity, locale, channel string, v string) {
		raw, _ := json.Marshal(v)
		if _, err := it.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: attrID, EntityID: entity, Locale: locale, Channel: channel,
			Value: json.RawMessage(raw),
		}); err != nil {
			t.Fatalf("set scoped value: %v", err)
		}
	}
	setScoped(descID, "p1", "en", "", "wide grip")
	setScoped(descID, "p2", "de", "", "breiter griff")
	setScoped(promoID, "p1", "", "web", "sale")
	setScoped(blurbID, "p1", "en", "web", "hello")
}

// TestFQLSingleDimensionScopeParity is the regression for issue #474: a
// query that supplies both locale and channel must still match attributes
// that carry only one of the two dimensions.
func TestFQLSingleDimensionScopeParity(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	pg := flexitype.New(pool)
	if err := pg.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cases := []struct {
		pq   parityQuery
		want []string
	}{
		// A localizable-only attribute matches on locale alone; the query's
		// channel must not exclude its channel='' rows.
		{parityQuery{q: `description = "wide grip"`, scope: valueobjects.Scope{Locale: "en", Channel: "web"}}, []string{"p1"}},
		{parityQuery{q: `has(description)`, scope: valueobjects.Scope{Locale: "en", Channel: "web"}}, []string{"p1"}},
		// A scopable-only attribute matches on channel alone; the query's
		// locale must not exclude its locale='' rows.
		{parityQuery{q: `promo = "sale"`, scope: valueobjects.Scope{Locale: "en", Channel: "web"}}, []string{"p1"}},
		{parityQuery{q: `promo = "sale"`, scope: valueobjects.Scope{Locale: "de", Channel: "web"}}, []string{"p1"}},
		// The dimension the attribute DOES carry still narrows.
		{parityQuery{q: `description = "wide grip"`, scope: valueobjects.Scope{Locale: "de", Channel: "web"}}, []string{}},
		{parityQuery{q: `promo = "sale"`, scope: valueobjects.Scope{Locale: "en", Channel: "print"}}, []string{}},
		// Control: a localizable+scopable attribute still pins both.
		{parityQuery{q: `blurb = "hello"`, scope: valueobjects.Scope{Locale: "en", Channel: "web"}}, []string{"p1"}},
		{parityQuery{q: `blurb = "hello"`, scope: valueobjects.Scope{Locale: "en", Channel: "print"}}, []string{}},
	}

	Convey("Given single-dimension scoped attributes in both backends", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

		mem := flexitype.NewInMemory()
		seedSingleDimensionScopes(ctx, t, mem)
		seedSingleDimensionScopes(ctx, t, pg)

		Convey("When a query supplies both locale and channel", func() {
			Convey("Then each backend pins only the dimensions the attribute carries, and both agree", func() {
				for _, tc := range cases {
					memIDs := runParityQuery(ctx, t, mem, tc.pq)
					pgIDs := runParityQuery(ctx, t, pg, tc.pq)
					So(memIDs, ShouldResemble, tc.want)
					So(pgIDs, ShouldResemble, tc.want)
					if t.Failed() {
						t.Logf("SCOPE REGRESSION on %q (scope %+v): want=%v memory=%v postgres=%v",
							tc.pq.q, tc.pq.scope, tc.want, memIDs, pgIDs)
						return
					}
				}
			})
		})
	})
}

// seedGhostCounterparts builds product --contains--> part links and then
// removes the only value of one counterpart on each side:
//
//   - p1 (name=P1) contains c1 and c2; c1's code was removed (ghost child).
//   - p2 contains c3 (code=C3); p2's name was removed (ghost parent).
//
// The links stay live: Values().Remove archives the value only, so the
// relationship outlives the entity.
func seedGhostCounterparts(ctx context.Context, t *testing.T, svc *flexitype.Service) {
	t.Helper()
	it := svc.Interactors(ctx)

	pt, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	part, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "part", DisplayName: "Part"})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	mkAttr := func(typeID, name string) string {
		a, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: "string",
		})
		if err != nil {
			t.Fatalf("create attr %s: %v", name, err)
		}
		return a.ID.String()
	}
	nameID := mkAttr(pt.ID.String(), "name")
	codeID := mkAttr(part.ID.String(), "code")

	contains, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
		InternalName: "contains", DisplayName: "Contains",
		ParentTypeID: pt.ID.String(), ChildTypeID: part.ID.String(),
	})
	if err != nil {
		t.Fatalf("create contains: %v", err)
	}

	set := func(attrID, entity, v string) string {
		raw, _ := json.Marshal(v)
		snap, err := it.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: attrID, EntityID: entity, Value: json.RawMessage(raw),
		})
		if err != nil {
			t.Fatalf("set value: %v", err)
		}
		return snap.ID.String()
	}
	set(nameID, "p1", "P1")
	c1Val := set(codeID, "c1", "C1")
	set(codeID, "c2", "C2")
	p2Val := set(nameID, "p2", "P2")
	set(codeID, "c3", "C3")

	link := func(parent, child string) {
		if _, err := it.Relationships().Link(ctx, apprelationship.LinkInput{
			DefinitionID: contains.ID.String(), ParentEntity: parent, ChildEntity: child,
		}); err != nil {
			t.Fatalf("link %s->%s: %v", parent, child, err)
		}
	}
	link("p1", "c1")
	link("p1", "c2")
	link("p2", "c3")

	// Remove the ghosts' only values AFTER linking, so each link dangles.
	if _, err := it.Values().Remove(ctx, c1Val); err != nil {
		t.Fatalf("remove c1 value: %v", err)
	}
	if _, err := it.Values().Remove(ctx, p2Val); err != nil {
		t.Fatalf("remove p2 value: %v", err)
	}
}

// TestFQLTraversalGhostParity is the regression for issue #475: a traversal
// must not match a counterpart whose last live value is gone, in either
// direction, on either backend.
func TestFQLTraversalGhostParity(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	pg := flexitype.New(pool)
	if err := pg.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cases := []struct {
		pq   parityQuery
		want []string
	}{
		// The ghost child c1 must not satisfy value-absence predicates.
		{parityQuery{q: `child(contains) { count(code) = 0 }`}, []string{}},
		{parityQuery{q: `child(contains) { not has(code) }`}, []string{}},
		// The type-negation divergence: Postgres yielded NULL for the
		// ghost's type (excluded), memory yielded "" (included). Both must
		// exclude the ghost.
		{parityQuery{q: `child(contains) { type != "part" }`}, []string{}},
		// A ghost never matches a value predicate; its value is archived.
		{parityQuery{q: `child(contains) { code = "C1" }`}, []string{}},
		// Live counterparts keep matching.
		{parityQuery{q: `child(contains) { code = "C2" }`}, []string{"p1"}},
		{parityQuery{q: `child(contains) { type = "part" }`}, []string{"p1"}},
		// The parent direction: c3's parent p2 is a ghost and must be
		// skipped; c2's parent p1 is live and must match.
		{parityQuery{q: `parent(contains) { has(name) }`, typ: "part"}, []string{"c2"}},
		{parityQuery{q: `parent(contains) { not has(name) }`, typ: "part"}, []string{}},
	}

	Convey("Given dangling relationships to value-less ghost counterparts in both backends", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

		mem := flexitype.NewInMemory()
		seedGhostCounterparts(ctx, t, mem)
		seedGhostCounterparts(ctx, t, pg)

		Convey("When traversals run across the dangling links", func() {
			Convey("Then ghosts never match, live counterparts still match, and both backends agree", func() {
				for _, tc := range cases {
					memIDs := runParityQuery(ctx, t, mem, tc.pq)
					pgIDs := runParityQuery(ctx, t, pg, tc.pq)
					So(memIDs, ShouldResemble, tc.want)
					So(pgIDs, ShouldResemble, tc.want)
					if t.Failed() {
						t.Logf("GHOST REGRESSION on %q (type %q): want=%v memory=%v postgres=%v",
							tc.pq.q, tc.pq.typ, tc.want, memIDs, pgIDs)
						return
					}
				}
			})
		})
	})
}
