package flexitype_test

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// seedProductCatalog builds an identical schema and data set in the given
// service. It is the single source of truth shared by the memory/Postgres
// parity harness and the corpus-completeness guard, so both backends and
// both tests see byte-for-byte the same catalog. The service must be created
// WithSearchIndex so matches() has a populated projection.
//
// The catalog deliberately spans every FQL-divergent construct:
//   - a "product" type with scalar/textual/decimal/date/bool attributes,
//   - a NULL-heavy entity (e4: only a name) for min/max/count semantics,
//   - a localizable+scopable "blurb" for scoped (locale/channel) conditions,
//   - a "weight" quantity over a mass unit family for cross-unit comparisons,
//   - a directed supplied_by(product→supplier) relationship with a link
//     attribute, a based_in(supplier→country) hop for a NESTED traversal,
//     and a symmetric compatible_with(product↔product) for linked().
func seedProductCatalog(ctx context.Context, t *testing.T, svc *flexitype.Service) {
	t.Helper()
	it := svc.Interactors(ctx)

	// --- product type + its attributes -------------------------------------
	pt, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
	if err != nil {
		t.Fatalf("create type: %v", err)
	}
	mkAttr := func(typeID, name, dt string) string {
		a, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: dt,
		})
		if err != nil {
			t.Fatalf("create attr %s: %v", name, err)
		}
		return a.ID.String()
	}
	priceID := mkAttr(pt.ID.String(), "price", "integer")
	inStockID := mkAttr(pt.ID.String(), "in_stock", "bool")
	nameID := mkAttr(pt.ID.String(), "name", "string")
	ratingID := mkAttr(pt.ID.String(), "rating", "decimal")
	releasedID := mkAttr(pt.ID.String(), "released", "date")

	// A localizable+scopable attribute drives the scoped-condition parity.
	blurbA, err := it.Attributes().Create(ctx, appattribute.CreateInput{
		TypeDefinitionID: pt.ID.String(), InternalName: "blurb", DisplayName: "Blurb",
		DataType: "string", Localizable: true, Scopable: true,
	})
	if err != nil {
		t.Fatalf("create blurb: %v", err)
	}
	blurbID := blurbA.ID.String()

	// A quantity attribute over a mass family drives cross-unit comparisons.
	fam, err := it.Units().Create(ctx, appunit.CreateInput{
		Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000, "lb": 453.592},
	})
	if err != nil {
		t.Fatalf("create unit family: %v", err)
	}
	weightA, err := it.Attributes().Create(ctx, appattribute.CreateInput{
		TypeDefinitionID: pt.ID.String(), InternalName: "weight", DisplayName: "Weight",
		DataType: "quantity", UnitFamilyID: fam.ID.String(), DisplayUnit: "g",
	})
	if err != nil {
		t.Fatalf("create weight: %v", err)
	}
	weightID := weightA.ID.String()

	set := func(attrID, entity string, v any) {
		raw, _ := json.Marshal(v)
		if _, err := it.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: attrID, EntityID: entity, Value: json.RawMessage(raw),
		}); err != nil {
			t.Fatalf("set value: %v", err)
		}
	}
	setScoped := func(attrID, entity, locale, channel string, v any) {
		raw, _ := json.Marshal(v)
		if _, err := it.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: attrID, EntityID: entity, Locale: locale, Channel: channel,
			Value: json.RawMessage(raw),
		}); err != nil {
			t.Fatalf("set scoped value: %v", err)
		}
	}
	setQty := func(attrID, entity, magnitude, unit string) {
		raw := json.RawMessage(`{"magnitude":"` + magnitude + `","unit":"` + unit + `"}`)
		if _, err := it.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: attrID, EntityID: entity, Value: raw,
		}); err != nil {
			t.Fatalf("set quantity value: %v", err)
		}
	}

	// e1: cheap, in stock; e2: pricey, in stock; e3: cheap, out of stock;
	// e4: only a name (price/in_stock/rating/released/weight absent — NULL semantics).
	set(priceID, "e1", 10)
	set(inStockID, "e1", true)
	set(nameID, "e1", "Alpha")
	set(ratingID, "e1", "4.8")
	set(releasedID, "e1", "2024-06-01")
	set(priceID, "e2", 100)
	set(inStockID, "e2", true)
	set(nameID, "e2", "Beta")
	set(ratingID, "e2", "4.2")
	set(releasedID, "e2", "2023-01-15")
	set(priceID, "e3", 10)
	set(inStockID, "e3", false)
	set(nameID, "e3", "Gamma")
	set(ratingID, "e3", "4.5")
	set(releasedID, "e3", "2024-03-10")
	set(nameID, "e4", "Delta")

	// Scoped blurbs: e1 differs by locale within one channel; e2 differs by
	// channel within one locale. Base (unscoped) reads must see neither.
	setScoped(blurbID, "e1", "en", "web", "hello")
	setScoped(blurbID, "e1", "de", "web", "hallo")
	setScoped(blurbID, "e2", "en", "web", "world")
	setScoped(blurbID, "e2", "en", "print", "printworld")

	// Weights in mixed units so cross-unit folding is exercised: 1 kg, 2 kg,
	// 500 g. e4 has none (quantity NULL). 2.2 lb == 997.9024 g sits between
	// e3 (500) and e1 (1000), giving a fractional cross-unit boundary.
	setQty(weightID, "e1", "1000", "g")
	setQty(weightID, "e2", "2", "kg")
	setQty(weightID, "e3", "500", "g")

	// --- supplier type + directed supplied_by relationship -----------------
	sup, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "supplier", DisplayName: "Supplier"})
	if err != nil {
		t.Fatalf("create supplier: %v", err)
	}
	regionID := mkAttr(sup.ID.String(), "region", "string")

	// country type for the second (nested) hop.
	country, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "country", DisplayName: "Country"})
	if err != nil {
		t.Fatalf("create country: %v", err)
	}
	codeID := mkAttr(country.ID.String(), "code", "string")

	suppliedBy, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
		InternalName: "supplied_by", DisplayName: "Supplied by",
		ParentTypeID: pt.ID.String(), ChildTypeID: sup.ID.String(),
	})
	if err != nil {
		t.Fatalf("create supplied_by: %v", err)
	}
	// A link attribute lives on the relationship's hidden attribute-set type;
	// its values anchor to the link id, exercising link.<attr> conditions.
	sbSets, err := it.Relationships().AttributeSets(ctx, suppliedBy.ID.String())
	if err != nil || len(sbSets) == 0 {
		t.Fatalf("supplied_by attribute sets: %v", err)
	}
	sinceID := mkAttr(sbSets[0], "since", "integer")

	basedIn, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
		InternalName: "based_in", DisplayName: "Based in",
		ParentTypeID: sup.ID.String(), ChildTypeID: country.ID.String(),
	})
	if err != nil {
		t.Fatalf("create based_in: %v", err)
	}

	compatibleWith, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
		InternalName: "compatible_with", DisplayName: "Compatible with", Kind: "symmetric",
		ParentTypeID: pt.ID.String(), ChildTypeID: pt.ID.String(),
	})
	if err != nil {
		t.Fatalf("create compatible_with: %v", err)
	}

	// supplier + country data.
	set(regionID, "s1", "EU")
	set(regionID, "s2", "APAC")
	set(codeID, "c1", "US")
	set(codeID, "c2", "JP")

	link := func(defID, parent, child string) string {
		l, err := it.Relationships().Link(ctx, apprelationship.LinkInput{
			DefinitionID: defID, ParentEntity: parent, ChildEntity: child,
		})
		if err != nil {
			t.Fatalf("link %s->%s: %v", parent, child, err)
		}
		return l.ID.String()
	}
	// e1 supplied by s1 (based in c1/US); e2 supplied by s2 (based in c2/JP).
	l1 := link(suppliedBy.ID.String(), "e1", "s1")
	link(suppliedBy.ID.String(), "e2", "s2")
	link(basedIn.ID.String(), "s1", "c1")
	link(basedIn.ID.String(), "s2", "c2")
	// e1 <-> e2 compatible (symmetric; store canonicalizes the pair).
	link(compatibleWith.ID.String(), "e1", "e2")

	// Link attribute value on e1->s1's link only: since = 2020.
	if _, err := it.Values().Set(ctx, appvalue.SetInput{
		AttributeDefinitionID: sinceID, EntityID: l1, Value: json.RawMessage(`2020`),
	}); err != nil {
		t.Fatalf("set link attr: %v", err)
	}
}

// parityQuery is one corpus entry. Most queries run against the "product"
// root with no scope; scoped and parent-side queries override those.
type parityQuery struct {
	q string
	// typ is the root type; empty means "product".
	typ string
	// scope restricts scoped-attribute predicates to one locale/channel;
	// zero matches base (unscoped) values.
	scope valueobjects.Scope
}

// parityCorpus is exercised by BOTH the memory evaluator and the Postgres SQL
// compiler; the harness asserts identical entity sets. The completeness guard
// (fql_corpus_completeness_test.go) reflects over this same slice to prove
// every FQL construct is represented, so a new construct cannot silently skip
// parity.
var parityCorpus = []parityQuery{
	// comparisons + boolean logic + NULL / three-valued cases
	{q: `price = 10`},
	{q: `price > 10`},
	{q: `price >= 10 and in_stock = true`},
	{q: `price = 10 or price = 100`},
	{q: `not (in_stock = true)`},
	{q: `in_stock = true`},
	{q: `name = "Alpha"`},
	{q: `name != "Alpha"`},
	{q: `price < 50 and not (name = "Gamma")`},
	{q: `price > 5 and price < 200`},
	// set membership + range
	{q: `price in (10, 100)`},
	{q: `name in ("Alpha", "Delta")`},
	{q: `range(price, 5, 50)`},
	// presence (has) over NULL
	{q: `has(in_stock)`},
	{q: `not has(in_stock)`},
	{q: `has(rating)`},
	// string matching (case-sensitive and insensitive)
	{q: `contains(name, "lph")`},
	{q: `icontains(name, "ET")`},
	{q: `iequals(name, "alpha")`},
	// scalar length()
	{q: `length(name) = 5`},
	{q: `length(name) < 5`},
	// type scope
	{q: `type = "product"`},
	{q: `type isa "product"`},
	// decimals (numeric comparison, not string)
	{q: `rating > 4.5`},
	{q: `rating >= 4.2 and rating <= 4.5`},
	{q: `rating = 4.5`},
	// dates
	{q: `released > "2024-01-01"`},
	{q: `released <= "2024-03-10"`},

	// --- aggregate min/max/count: populated AND empty/NULL sets -------------
	// min/max over no rows compile to a scalar subquery returning SQL NULL,
	// so the comparison is UNKNOWN (not FALSE) and e4 stays excluded even
	// under NOT — the exact three-valued divergence the memory twin mirrors.
	{q: `min(price) > 50`},
	{q: `not (min(price) > 50)`}, // e4 (NULL) must NOT flip in under NOT
	{q: `max(price) < 50`},
	{q: `max(rating) >= 4.5`},
	// count() over no rows is 0 (a definite value, not NULL): e4 matches = 0.
	{q: `count(price) = 0`},
	{q: `count(price) >= 1`},

	// --- quantity comparisons across units ---------------------------------
	// The binder folds every unit-suffixed literal to the family base (g), so
	// these compare against 1000/2000/500 g regardless of the literal's unit.
	{q: `weight > 1 kg`},
	{q: `weight >= 1 kg`},
	{q: `weight < 2.2 lb`}, // 997.9024 g boundary: only e3 (500 g)
	{q: `weight > 900 g`},  // e1 (1000) and e2 (2000)
	{q: `has(weight)`},     // e4 has no weight (quantity NULL)
	{q: `not has(weight)`},

	// --- scoped (locale/channel) conditions --------------------------------
	// A scoped predicate matches only within the query's locale/channel;
	// base (zero) scope must see NONE of the localized blurbs.
	{q: `blurb = "hello"`, scope: valueobjects.Scope{Locale: "en", Channel: "web"}},
	{q: `blurb = "hallo"`, scope: valueobjects.Scope{Locale: "de", Channel: "web"}},
	{q: `blurb = "hello"`, scope: valueobjects.Scope{Locale: "de", Channel: "web"}}, // wrong scope: empty
	{q: `blurb = "world"`, scope: valueobjects.Scope{Locale: "en", Channel: "web"}},
	{q: `blurb = "printworld"`, scope: valueobjects.Scope{Locale: "en", Channel: "print"}},
	{q: `has(blurb)`, scope: valueobjects.Scope{Locale: "en", Channel: "web"}},
	{q: `has(blurb)`}, // base scope: no unscoped blurb exists anywhere

	// --- relationship traversals (child / linked / nested / parent) --------
	// compileTraversal (SQL) vs evalTraversal (memory): an EXISTS over live
	// counterparts. Direction and link-attribute correlation are the most
	// divergent surface, so they get the widest coverage.
	{q: `child(supplied_by) { region = "EU" }`},
	{q: `child(supplied_by) { region = "APAC" }`},
	{q: `child(supplied_by) { region = "US" }`}, // no such supplier: empty
	{q: `child(supplied_by) { link.since >= 2020 }`},
	{q: `child(supplied_by) { link.since > 3000 }`}, // link attr present but too small
	// nested: product -> supplier -> country
	{q: `child(supplied_by) { child(based_in) { code = "US" } }`},
	{q: `child(supplied_by) { child(based_in) { code = "JP" } }`},
	{q: `child(supplied_by) { child(based_in) { code = "XX" } }`}, // empty
	// symmetric linked(): matches from either endpoint.
	{q: `linked(compatible_with) { name = "Beta" }`},  // -> e1
	{q: `linked(compatible_with) { name = "Alpha" }`}, // -> e2
	// parent(): from the supplier side back up to the product.
	{q: `parent(supplied_by) { name = "Alpha" }`, typ: "supplier"},
	{q: `parent(supplied_by) { name = "Zeta" }`, typ: "supplier"}, // empty

	// --- matches(): full-text search over the entity search document -------
	// matches() is an admitted approximation: memory requires every query
	// word to appear as a whole word; Postgres runs to_tsvector('simple')
	// @@ plainto_tsquery('simple'). For plain, single alphabetic words with
	// no punctuation the two agree exactly, so the corpus stays on that safe
	// intersection rather than probing stemming/stopword edges.
	{q: `matches("Alpha")`},
	{q: `matches("alpha")`}, // case-insensitive
	{q: `matches("Delta")`},
	{q: `matches("Nonexistentword")`}, // empty in both
}

func runParityQuery(ctx context.Context, t *testing.T, svc *flexitype.Service, pq parityQuery) []string {
	t.Helper()
	typ := pq.typ
	if typ == "" {
		typ = "product"
	}
	out, err := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
		Type: typ, Query: pq.q, Page: db.PageArgs{}, Scope: pq.scope,
	})
	if err != nil {
		t.Fatalf("query %q (type %s): %v", pq.q, typ, err)
	}
	ids := make([]string, 0, len(out.Items))
	for _, r := range out.Items {
		ids = append(ids, r.EntityID)
	}
	sort.Strings(ids)
	return ids
}

// TestFQLMemoryPostgresParity runs the FQL corpus against the in-memory
// evaluator and the Postgres SQL compiler over identical data and asserts
// they return the same entity sets — catching any divergence across
// comparisons, three-valued logic, aggregates, quantity units, scoped
// values, relationship traversals and full-text search.
func TestFQLMemoryPostgresParity(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	// Search index on both backends so matches() has a projection to probe.
	pg := flexitype.New(pool, flexitype.WithSearchIndex())
	if err := pg.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given identical catalogs in the memory and Postgres backends", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

		mem := flexitype.NewInMemory(flexitype.WithSearchIndex())
		seedProductCatalog(ctx, t, mem)
		seedProductCatalog(ctx, t, pg)

		Convey("When each query in the corpus runs against both", func() {
			Convey("Then the two backends return identical entity sets", func() {
				for _, pq := range parityCorpus {
					memIDs := runParityQuery(ctx, t, mem, pq)
					pgIDs := runParityQuery(ctx, t, pg, pq)
					// Per-query context in the failure so a divergence names
					// the exact construct that broke parity.
					So(pgIDs, ShouldResemble, memIDs)
					if t.Failed() {
						t.Logf("PARITY DIVERGENCE on %q (type %q, scope %+v): memory=%v postgres=%v",
							pq.q, pq.typ, pq.scope, memIDs, pgIDs)
						return
					}
				}
			})
		})
	})
}
