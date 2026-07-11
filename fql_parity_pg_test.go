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
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// seedProductCatalog builds an identical "product" schema and set of
// entities in the given service and returns nothing — callers query it.
func seedProductCatalog(ctx context.Context, t *testing.T, svc *flexitype.Service) {
	t.Helper()
	it := svc.Interactors(ctx)
	pt, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
	if err != nil {
		t.Fatalf("create type: %v", err)
	}
	mkAttr := func(name, dt string) string {
		a, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: pt.ID.String(), InternalName: name, DisplayName: name, DataType: dt,
		})
		if err != nil {
			t.Fatalf("create attr %s: %v", name, err)
		}
		return a.ID.String()
	}
	priceID := mkAttr("price", "integer")
	inStockID := mkAttr("in_stock", "bool")
	nameID := mkAttr("name", "string")

	set := func(attrID, entity string, v any) {
		raw, _ := json.Marshal(v)
		if _, err := it.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: attrID, EntityID: entity, Value: json.RawMessage(raw),
		}); err != nil {
			t.Fatalf("set value: %v", err)
		}
	}
	// e1: cheap, in stock; e2: pricey, in stock; e3: cheap, out of stock;
	// e4: only a name (price/in_stock absent — exercises NULL semantics).
	set(priceID, "e1", 10)
	set(inStockID, "e1", true)
	set(nameID, "e1", "Alpha")
	set(priceID, "e2", 100)
	set(inStockID, "e2", true)
	set(nameID, "e2", "Beta")
	set(priceID, "e3", 10)
	set(inStockID, "e3", false)
	set(nameID, "e3", "Gamma")
	set(nameID, "e4", "Delta")
}

func runQuery(ctx context.Context, t *testing.T, svc *flexitype.Service, q string) []string {
	t.Helper()
	out, err := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
		Type: "product", Query: q, Page: db.PageArgs{},
	})
	if err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	ids := make([]string, 0, len(out.Items))
	for _, r := range out.Items {
		ids = append(ids, r.EntityID)
	}
	sort.Strings(ids)
	return ids
}

// TestFQLMemoryPostgresParity runs a corpus of FQL queries against the
// in-memory evaluator and the Postgres SQL compiler over identical data and
// asserts they return the same entity sets — catching any divergence
// (including the SQL NULL / three-valued-logic edge cases).
func TestFQLMemoryPostgresParity(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	pg := flexitype.New(pool)
	if err := pg.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncateAllRoot := func() {
		var stmt string
		if err := pool.Get(&stmt, `SELECT 'TRUNCATE ' || string_agg(format('%I', tablename), ', ') || ' CASCADE'
			FROM pg_tables WHERE schemaname='public' AND tablename LIKE 'flexitype_%'
			  AND tablename <> 'flexitype_schema_migrations'`); err != nil {
			t.Fatalf("truncate build: %v", err)
		}
		pool.MustExec(stmt)
	}

	corpus := []string{
		`price = 10`,
		`price > 10`,
		`price >= 10 and in_stock = true`,
		`price = 10 or price = 100`,
		`not (in_stock = true)`,
		`in_stock = true`,
		`name = "Alpha"`,
		`name != "Alpha"`,
		`price < 50 and not (name = "Gamma")`,
		`price > 5 and price < 200`,
	}

	Convey("Given identical catalogs in the memory and Postgres backends", t, func() {
		truncateAllRoot()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

		mem := flexitype.NewInMemory()
		seedProductCatalog(ctx, t, mem)
		seedProductCatalog(ctx, t, pg)

		Convey("When each query in the corpus runs against both", func() {
			Convey("Then the two backends return identical entity sets", func() {
				for _, q := range corpus {
					memIDs := runQuery(ctx, t, mem, q)
					pgIDs := runQuery(ctx, t, pg, q)
					So(pgIDs, ShouldResemble, memIDs)
				}
			})
		})
	})
}
