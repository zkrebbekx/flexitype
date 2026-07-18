package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestComputedAttributesParityPostgres re-runs the computed-attribute suite
// (infrastructure/memory/computed_test.go) against Postgres. A computed value
// is materialized by an internal-projection subscriber that runs synchronously
// inside the writing unit of work, so the derived margin becomes an ordinary,
// FQL-queryable value. This proves that projection, and the read-your-writes
// consistency of the read-back, hold over the real SQL value store — with each
// read-back done through a FRESH Interactors so the request-scoped dataloader
// never serves a value it cached before the recompute (the #207 gotcha).
func TestComputedAttributesParityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool, flexitype.WithSearchIndex())
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given price, cost and a computed margin = (price - cost) / price (Postgres)", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		attr := func(name, dt string) string {
			s, e := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: dt,
			})
			So(e, ShouldBeNil)
			return s.ID.String()
		}
		priceID := attr("price", "float")
		costID := attr("cost", "float")
		margin, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "margin", DisplayName: "Margin", DataType: "float",
			Computed: json.RawMessage(`{"kind":"formula","formula":"(price - cost) / price"}`),
		})
		So(err, ShouldBeNil)

		// Each Set is its own unit of work; the read-back below likewise runs on a
		// fresh Interactors so it reflects the just-materialized margin.
		setF := func(attrID string, v float64) {
			raw, _ := json.Marshal(v)
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: "p1", TypeDefinitionID: typeID, Value: raw,
			})
			So(serr, ShouldBeNil)
		}
		marginValue := func() (float64, bool) {
			vals, e := svc.Interactors(ctx).Values().ListByEntity(ctx, typeID, "p1")
			So(e, ShouldBeNil)
			for _, v := range vals {
				if v.AttributeDefinitionID.String() == margin.ID.String() {
					return v.Value.Float(), true
				}
			}
			return 0, false
		}

		setF(priceID, 100)
		setF(costID, 40)

		Convey("When both inputs are set", func() {
			Convey("Then margin materializes to 0.6", func() {
				m, ok := marginValue()
				So(ok, ShouldBeTrue)
				So(m, ShouldAlmostEqual, 0.6)
			})
		})

		Convey("When an input changes", func() {
			setF(costID, 20)
			Convey("Then margin recomputes to 0.8", func() {
				m, ok := marginValue()
				So(ok, ShouldBeTrue)
				So(m, ShouldAlmostEqual, 0.8)
			})
		})

		Convey("When a computed value is written through the values API", func() {
			raw, _ := json.Marshal(0.99)
			_, err := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: margin.ID.String(), EntityID: "p1", TypeDefinitionID: typeID, Value: raw,
			})
			Convey("Then it is rejected as read-only", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("When FQL filters on the computed value", func() {
			out, err := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{Type: "product", Query: "margin > 0.5"})
			So(err, ShouldBeNil)
			ids := []string{}
			for _, r := range out.Items {
				ids = append(ids, r.EntityID)
			}
			Convey("Then it matches the materialized value", func() {
				So(ids, ShouldContain, "p1")
			})
		})

		Convey("When a formula cycle aa -> bb -> aa is defined", func() {
			_, err := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: "aa", DisplayName: "A", DataType: "float",
				Computed: json.RawMessage(`{"kind":"formula","formula":"bb"}`),
			})
			So(err, ShouldBeNil)
			_, err = it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: "bb", DisplayName: "B", DataType: "float",
				Computed: json.RawMessage(`{"kind":"formula","formula":"aa"}`),
			})
			Convey("Then the cycle is rejected", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})
	})
}

// TestComputedMaterializesAfterCoalescedImportPostgres re-runs the coalesced
// import scenario over Postgres: each imported row emits two value events for
// one entity in a single commit, and the coalesced recompute must still
// materialize margin exactly once per entity with the correct value.
func TestComputedMaterializesAfterCoalescedImportPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool, flexitype.WithSearchIndex())
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a product type with a computed margin over price and cost (Postgres)", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		mk := func(name, dt string) {
			_, e := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: dt,
			})
			So(e, ShouldBeNil)
		}
		mk("price", "float")
		mk("cost", "float")
		margin, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "margin", DisplayName: "Margin", DataType: "float",
			Computed: json.RawMessage(`{"kind":"formula","formula":"(price - cost) / price"}`),
		})
		So(err, ShouldBeNil)

		Convey("When two entities are imported, each setting price and cost in one commit", func() {
			rep, err := svc.Interactors(ctx).Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: typeID,
				KeyColumn:        "id",
				Mapping:          map[string]string{"price": "price", "cost": "cost"},
				Columns:          []string{"id", "price", "cost"},
				Rows: [][]string{
					{"p1", "100", "40"}, // margin = 0.6
					{"p2", "200", "50"}, // margin = 0.75
				},
				Mode: appvalue.ImportBestEffort,
			})
			So(err, ShouldBeNil)
			So(rep.RowsWritten, ShouldEqual, 2)

			readMargin := func(entity string) (float64, bool) {
				vals, e := svc.Interactors(ctx).Values().ListByEntity(ctx, typeID, entity)
				So(e, ShouldBeNil)
				for _, v := range vals {
					if v.AttributeDefinitionID.String() == margin.ID.String() {
						return v.Value.Float(), true
					}
				}
				return 0, false
			}

			Convey("Then each entity's margin materializes to the right value", func() {
				m1, ok1 := readMargin("p1")
				So(ok1, ShouldBeTrue)
				So(m1, ShouldAlmostEqual, 0.6)
				m2, ok2 := readMargin("p2")
				So(ok2, ShouldBeTrue)
				So(m2, ShouldAlmostEqual, 0.75)
			})
		})
	})
}

// TestComputedUpdateCycleAndEdgesParityPostgres re-runs the recompute edge
// cases over Postgres: a formula updated into a self-reference is rejected,
// integer results round to nearest, an overflowing result clears the stored
// value instead of leaving it stale, and a bool operand participates as 0/1.
// Each read-back uses a fresh Interactors so the materialized value is observed,
// never a stale dataloader hit.
func TestComputedUpdateCycleAndEdgesParityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool, flexitype.WithSearchIndex())
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a computed attribute and numeric inputs (Postgres)", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		mk := func(name, dt, computed string) string {
			in := appattribute.CreateInput{TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: dt}
			if computed != "" {
				in.Computed = json.RawMessage(computed)
			}
			s, e := it.Attributes().Create(ctx, in)
			So(e, ShouldBeNil)
			return s.ID.String()
		}
		setV := func(attrID string, v any) {
			raw, _ := json.Marshal(v)
			_, e := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{AttributeDefinitionID: attrID, EntityID: "p1", TypeDefinitionID: typeID, Value: raw})
			So(e, ShouldBeNil)
		}
		read := func(attrID string) (valueobjects.Value, bool) {
			vals, e := svc.Interactors(ctx).Values().ListByEntity(ctx, typeID, "p1")
			So(e, ShouldBeNil)
			for _, v := range vals {
				if v.AttributeDefinitionID.String() == attrID {
					return v.Value, true
				}
			}
			return valueobjects.Value{}, false
		}

		xx := mk("xx", "float", "")
		aa := mk("aa", "float", `{"kind":"formula","formula":"xx + 1"}`)

		Convey("When aa's formula is updated into a self-reference", func() {
			_, err := it.Attributes().Update(ctx, appattribute.UpdateInput{
				ID: aa, DisplayName: "A",
				Computed: json.RawMessage(`{"kind":"formula","formula":"aa + 1"}`),
			})
			Convey("Then it is rejected as a cycle", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("An integer computed value rounds to nearest", func() {
			half := mk("half", "integer", `{"kind":"formula","formula":"xx / 2"}`)
			setV(xx, 5.0) // 2.5 -> 3
			v, ok := read(half)
			So(ok, ShouldBeTrue)
			So(v.Int(), ShouldEqual, 3)
		})

		Convey("An overflowing (infinite) result clears the value rather than storing it stale", func() {
			big := mk("big", "float", `{"kind":"formula","formula":"xx * xx"}`)
			setV(xx, 2.0) // big = 4
			v, ok := read(big)
			So(ok, ShouldBeTrue)
			So(v.Float(), ShouldAlmostEqual, 4.0)

			setV(xx, 1e308) // xx*xx overflows to +Inf
			_, ok = read(big)
			So(ok, ShouldBeFalse) // cleared, not left at 4
		})

		Convey("A bool operand participates in arithmetic as 0/1", func() {
			active := mk("active", "bool", "")
			score := mk("score", "float", "")
			weighted := mk("weighted", "float", `{"kind":"formula","formula":"active * score"}`)
			setV(score, 10.0)
			setV(active, true)
			v, ok := read(weighted)
			So(ok, ShouldBeTrue)
			So(v.Float(), ShouldAlmostEqual, 10.0)

			setV(active, false)
			v, ok = read(weighted)
			So(ok, ShouldBeTrue)
			So(v.Float(), ShouldAlmostEqual, 0.0)
		})
	})
}
