//go:build stress

// SaaS-shaped scale harness (companion to stress_test.go, same build tag).
//
// Models the deployment a SaaS adopter asked about: ONE tenant, ONE entity
// type, 20 attributes covering every one of the 14 data types, 10M entities —
// so 200M value rows — then measures with P50/P95:
//
//   - fetch: Values().ListByEntity over random entities (the "load one
//     entity's attributes" hot path)
//   - search: one FQL query per data type (equality where expressible,
//     presence for json/media, range for datetime/quantity), with and
//     without the total count
//
// CPU + heap pprof profiles cover the measured section only, never the seed.
//
// Run (full 10M — allow an hour, needs ~60 GB free on the Postgres volume):
//
//	FLEXITYPE_TEST_DSN=postgres://postgres:postgres@localhost:55433/stresstest?sslmode=disable \
//	  go test -tags stress -run TestStressSaaS -timeout 180m -v .
//
// Knobs: SAAS_ENTITIES (default 10_000_000), SAAS_FETCH_N (300),
// SAAS_SEARCH_N (12), STRESS_OUT (profile/output dir).
package flexitype_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// saasAttr pairs a seeded attribute with how the seeder fills its row.
type saasAttr struct {
	name string
	dt   string
	ref  attrRef
	// fill sets the typed value column(s) for entity sequence n.
	fill func(row []any, n int, base time.Time)
}

func TestStressSaaS(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	entities := envInt("SAAS_ENTITIES", 10_000_000)
	fetchN := envInt("SAAS_FETCH_N", 300)
	searchN := envInt("SAAS_SEARCH_N", 12)
	outDir := os.Getenv("STRESS_OUT")
	if outDir == "" {
		outDir = "stress-out"
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir out: %v", err)
	}
	t.Logf("saas config: entities=%d (=%d value rows) fetchN=%d searchN=%d out=%s",
		entities, entities*20, fetchN, searchN, outDir)

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	it := svc.Interactors(ctx)

	truncateAll(t, pool)

	// SAAS_KEEP_INDEXES=1 seeds with the FULL production index set — the
	// faithful configuration, and the semi-join probe in rare-match searches
	// needs definition_entity to be cheap. Without it (the default, sized for
	// the 10M/200M-row target where the write-path indexes alone exceed this
	// machine's free disk), only the indexes the measured read paths use are
	// kept, and rare-match searches over-read: each entity probe falls back
	// to the entity-wide index and filters all 20 values.
	dropIdx := []string{
		"idx_flexitype_attribute_value_trgm",
		"idx_flexitype_attribute_value_trgm_lower",
	}
	if os.Getenv("SAAS_KEEP_INDEXES") == "" {
		dropIdx = append(dropIdx,
			"idx_flexitype_attribute_value_definition",
			"idx_flexitype_attribute_value_definition_entity",
			"idx_flexitype_attribute_value_entity_attr",
			"idx_flexitype_attribute_value_scope",
			"idx_flexitype_attribute_value_media_key",
		)
	}
	for _, idx := range dropIdx {
		if _, err := pool.Exec("DROP INDEX IF EXISTS " + idx); err != nil {
			t.Fatalf("drop index %s: %v", idx, err)
		}
	}
	if _, err := pool.Exec("ALTER TABLE flexitype_attribute_value DISABLE TRIGGER USER"); err != nil {
		t.Fatalf("disable triggers: %v", err)
	}

	// ---- SEED: one type, one unit family, 20 attributes ----
	seedStart := time.Now()

	family, err := it.Units().Create(ctx, appunit.CreateInput{
		Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
	})
	if err != nil {
		t.Fatalf("create unit family: %v", err)
	}

	td, err := it.TypeDefinitions().Create(ctx,
		apptypedef.CreateInput{InternalName: "account", DisplayName: "Account"})
	if err != nil {
		t.Fatalf("create type: %v", err)
	}
	typeID := td.ID.String()

	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	tiers := []string{"bronze", "silver", "gold", "platinum"}
	segments := []string{"smb", "mid", "ent"}

	// 20 attributes: one per data type (14) + 6 doubles on the common types.
	attrs := []saasAttr{
		{name: "bool_flag", dt: "bool", fill: func(r []any, n int, _ time.Time) { setCol(r, "value_bool", n%2 == 0) }},
		{name: "name", dt: "string", fill: func(r []any, n int, _ time.Time) { setCol(r, "value_text", "s"+strconv.Itoa(n%1000)) }},
		{name: "count", dt: "integer", fill: func(r []any, n int, _ time.Time) { setCol(r, "value_int", int64(n%100)) }},
		{name: "score", dt: "float", fill: func(r []any, n int, _ time.Time) { setCol(r, "value_float", float64(n%10000)/10) }},
		{name: "price", dt: "decimal", fill: func(r []any, n int, _ time.Time) {
			setCol(r, "value_text", fmt.Sprintf("%d.%02d", n%1000, n%100))
		}},
		{name: "signup_date", dt: "date", fill: func(r []any, n int, b time.Time) {
			setCol(r, "value_time", b.AddDate(0, 0, n%365))
		}},
		{name: "open_at", dt: "time", fill: func(r []any, n int, _ time.Time) {
			s := n % 86400
			setCol(r, "value_time", time.Date(0, 1, 1, s/3600, (s/60)%60, s%60, 0, time.UTC))
		}},
		{name: "updated", dt: "datetime", fill: func(r []any, n int, b time.Time) {
			setCol(r, "value_time", b.Add(time.Duration(n)*time.Second))
		}},
		{name: "tier", dt: "enum", fill: func(r []any, n int, _ time.Time) { setCol(r, "value_text", tiers[n%4]) }},
		{name: "website", dt: "url", fill: func(r []any, n int, _ time.Time) {
			setCol(r, "value_text", "https://example.com/p"+strconv.Itoa(n%100000))
		}},
		{name: "contact", dt: "email", fill: func(r []any, n int, _ time.Time) {
			setCol(r, "value_text", "u"+strconv.Itoa(n%100000)+"@example.com")
		}},
		{name: "meta", dt: "json", fill: func(r []any, n int, _ time.Time) {
			setCol(r, "value_json", `{"k":`+strconv.Itoa(n%50)+`}`)
		}},
		{name: "avatar", dt: "media", fill: func(r []any, n int, _ time.Time) {
			setCol(r, "value_json",
				`{"object_key":"obj/`+strconv.Itoa(n)+`","mime":"image/png","size":1234}`)
		}},
		{name: "weight", dt: "quantity", fill: func(r []any, n int, _ time.Time) {
			g := n % 5000
			setCol(r, "value_float", float64(g))
			setCol(r, "value_json", `{"magnitude":"`+strconv.Itoa(g)+`","unit":"g"}`)
		}},
		{name: "name2", dt: "string", fill: func(r []any, n int, _ time.Time) { setCol(r, "value_text", "t"+strconv.Itoa(n%50)) }},
		{name: "count2", dt: "integer", fill: func(r []any, n int, _ time.Time) { setCol(r, "value_int", int64(n%1000)) }},
		{name: "score2", dt: "float", fill: func(r []any, n int, _ time.Time) { setCol(r, "value_float", float64(n%500)/4) }},
		{name: "flag2", dt: "bool", fill: func(r []any, n int, _ time.Time) { setCol(r, "value_bool", n%3 == 0) }},
		{name: "due_date", dt: "date", fill: func(r []any, n int, b time.Time) {
			setCol(r, "value_time", b.AddDate(0, 0, n%30))
		}},
		{name: "segment", dt: "enum", fill: func(r []any, n int, _ time.Time) { setCol(r, "value_text", segments[n%3]) }},
	}
	for i := range attrs {
		in := appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: attrs[i].name,
			DisplayName: attrs[i].name, DataType: attrs[i].dt,
		}
		switch attrs[i].dt {
		case "enum":
			members := tiers
			if attrs[i].name == "segment" {
				members = segments
			}
			ms := make([]string, len(members))
			for j, m := range members {
				ms[j] = `{"type":"enum","value":"` + m + `"}`
			}
			in.Constraints = json.RawMessage(`[{"kind":"one_of","values":[` + strings.Join(ms, ",") + `]}]`)
		case "quantity":
			in.UnitFamilyID = family.ID.String()
			in.DisplayUnit = "kg"
		}
		snap, err := it.Attributes().Create(ctx, in)
		if err != nil {
			t.Fatalf("create attr %s: %v", attrs[i].name, err)
		}
		attrs[i].ref = attrRef{id: snap.ID.String(), dataType: attrs[i].dt, version: snap.Version}
	}
	t.Logf("seed: type + %d attributes created", len(attrs))

	// ---- SEED: 20 value rows per entity via COPY ----
	start := time.Now()
	total := 0
	rowsInBatch := 0
	tx, err := pool.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	//nolint:staticcheck // pq.CopyIn is the required bulk-load path (see stress_test.go).
	stmt, err := tx.Prepare(pq.CopyIn("flexitype_attribute_value", valueCopyCols...))
	if err != nil {
		t.Fatalf("prepare copy: %v", err)
	}
	flush := func(reopen bool) {
		if _, err := stmt.Exec(); err != nil {
			t.Fatalf("copy flush: %v", err)
		}
		if err := stmt.Close(); err != nil {
			t.Fatalf("copy close: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("copy commit: %v", err)
		}
		if !reopen {
			return
		}
		tx, err = pool.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		//nolint:staticcheck // see above.
		stmt, err = tx.Prepare(pq.CopyIn("flexitype_attribute_value", valueCopyCols...))
		if err != nil {
			t.Fatalf("prepare copy: %v", err)
		}
	}
	for n := 0; n < entities; n++ {
		entityID := "e" + strconv.Itoa(n)
		ts := base.Add(time.Duration(n) * time.Second)
		for _, a := range attrs {
			row := baseRow(typeID, entityID, ts)
			setCol(row, "attribute_definition_id", a.ref.id)
			setCol(row, "data_type", a.ref.dataType)
			setCol(row, "definition_version", a.ref.version)
			a.fill(row, n, base)
			if _, err := stmt.Exec(row...); err != nil {
				t.Fatalf("copy exec: %v", err)
			}
			total++
			rowsInBatch++
		}
		if rowsInBatch >= copyBatchRows {
			flush(true)
			rowsInBatch = 0
			if total%(copyBatchRows*20) < 20 {
				el := time.Since(start)
				t.Logf("seed: %d rows in %s (%.0f rows/s, ~%s remaining)",
					total, el.Round(time.Second), float64(total)/el.Seconds(),
					time.Duration(float64(el)*float64(entities*20-total)/float64(total)).Round(time.Minute))
			}
		}
	}
	flush(false)

	if _, err := pool.Exec("ALTER TABLE flexitype_attribute_value ENABLE TRIGGER USER"); err != nil {
		t.Fatalf("enable triggers: %v", err)
	}
	t.Log("seed: backfilling flexitype_entity_summary...")
	if _, err := pool.Exec(
		`INSERT INTO flexitype_entity_summary
		     (tenant_id, type_definition_id, entity_id, value_count, last_updated_at)
		 SELECT tenant_id, type_definition_id, entity_id, count(*), max(updated_at)
		   FROM flexitype_attribute_value
		  WHERE archived_at IS NULL
		  GROUP BY tenant_id, type_definition_id, entity_id
		 ON CONFLICT (tenant_id, type_definition_id, entity_id) DO NOTHING`); err != nil {
		t.Fatalf("backfill summary: %v", err)
	}
	t.Log("seed: ANALYZE...")
	for _, tbl := range []string{"flexitype_attribute_value", "flexitype_entity_summary"} {
		if _, err := pool.Exec("ANALYZE " + tbl); err != nil {
			t.Fatalf("analyze: %v", err)
		}
	}
	t.Logf("SEED COMPLETE in %s: %d entities, %d value rows",
		time.Since(seedStart).Round(time.Second), entities, total)

	// ---- MEASURE (profiled) ----
	cpuPath := filepath.Join(outDir, "saas-cpu.pprof")
	heapPath := filepath.Join(outDir, "saas-heap.pprof")
	cpuFile, err := os.Create(cpuPath)
	if err != nil {
		t.Fatalf("create cpu profile: %v", err)
	}
	if err := pprof.StartCPUProfile(cpuFile); err != nil {
		t.Fatalf("start cpu profile: %v", err)
	}

	// Fetch: one entity's 20 attribute values, uniformly random entities.
	t.Log("SCENARIO: fetch entity attributes (ListByEntity, random entities)")
	rng := rand.New(rand.NewSource(1))
	fetch := timeN(fetchN, func() {
		id := "e" + strconv.Itoa(rng.Intn(entities))
		out, err := it.Values().ListByEntity(ctx, typeID, id)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if len(out) != len(attrs) {
			t.Fatalf("fetch: got %d values, want %d", len(out), len(attrs))
		}
	})
	fetch.report(t, fmt.Sprintf("(20 values/entity, %d entities)", entities))

	// Search: one query per data type. Selectivity is deterministic from the
	// seed formulas and reported next to each timing.
	t.Log("SCENARIO: search per data type (FQL, page of 20)")
	type searchCase struct {
		dt    string
		query string
	}
	// The datetime threshold is derived from the population (updated = base +
	// n seconds), so the ~1% selectivity holds at every SAAS_ENTITIES scale.
	dtCutoff := base.Add(time.Duration(entities) * 99 / 100 * time.Second).Format(time.RFC3339)
	cases := []searchCase{
		{"bool", `bool_flag = true`},                      // ~50%
		{"string", `name = "s5"`},                         // ~0.1%
		{"integer", `count = 5`},                          // ~1%
		{"integer-range", `range(count, 5, 14)`},          // ~10%
		{"float", `score > 999.0`},                        // ~0.1%
		{"decimal", `price = "12.12"`},                    // ~0.1%
		{"date", `signup_date = "2020-01-06"`},            // ~1/365
		{"time", `open_at = "01:23:20"`},                  // ~1/86400
		{"datetime", `updated > "` + dtCutoff + `"`},      // ~1%
		{"enum", `tier = gold`},                           // 25%
		{"url", `website = "https://example.com/p5"`},     // ~0.001%
		{"email", `contact = "u5@example.com"`},           // ~0.001%
		{"json", `has(meta)`},                             // presence — equality is not expressible in FQL
		{"media", `has(avatar)`},                          // presence — equality is not expressible in FQL
		{"quantity", `weight > 4.9 kg`},                   // ~2%
	}
	limit := 20
	for _, c := range cases {
		ti := timeN(searchN, func() {
			if _, err := it.Query().Execute(ctx, appquery.ExecuteInput{
				Type: "account", Query: c.query, Page: db.PageArgs{Limit: &limit},
			}); err != nil {
				t.Fatalf("search [%s] %q: %v", c.dt, c.query, err)
			}
		})
		ti.report(t, fmt.Sprintf("[%s] %s", c.dt, c.query))
	}

	// The same searches asking for the total count — the count is a separate
	// query over the full match set, so it is the expensive half.
	t.Log("SCENARIO: search per data type with total count")
	for _, c := range cases {
		var totalCount *int
		ti := timeN(3, func() {
			out, err := it.Query().Execute(ctx, appquery.ExecuteInput{
				Type: "account", Query: c.query, Page: db.PageArgs{Limit: &limit, WantTotal: true},
			})
			if err != nil {
				t.Fatalf("search+total [%s]: %v", c.dt, err)
			}
			totalCount = out.PageInfo.TotalCount
		})
		tot := "?"
		if totalCount != nil {
			tot = strconv.Itoa(*totalCount)
		}
		ti.report(t, fmt.Sprintf("[%s] %s → %s matches", c.dt, c.query, tot))
	}

	pprof.StopCPUProfile()
	_ = cpuFile.Close()

	runtime.GC()
	heapFile, err := os.Create(heapPath)
	if err != nil {
		t.Fatalf("create heap profile: %v", err)
	}
	if err := pprof.WriteHeapProfile(heapFile); err != nil {
		t.Fatalf("write heap profile: %v", err)
	}
	_ = heapFile.Close()

	cpuTop := pprofTop(t, cpuPath, "")
	allocTop := pprofTop(t, heapPath, "alloc_space")
	writeOut(t, filepath.Join(outDir, "saas-top-cpu.txt"), cpuTop)
	writeOut(t, filepath.Join(outDir, "saas-top-heap-alloc.txt"), allocTop)
	t.Logf("\n==== CPU top-20 ====\n%s", cpuTop)
	t.Logf("\n==== HEAP alloc_space top-20 ====\n%s", allocTop)
	t.Logf("profiles: %s , %s", cpuPath, heapPath)
}
