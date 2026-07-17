//go:build stress

// Package flexitype_test also carries a build-tagged scale stress + profiling
// harness (issue #212). It is compiled and run ONLY with `-tags stress`, so it
// never executes in normal CI. It seeds a large dataset directly via COPY (the
// public API would take days at this scale), then times and profiles the core
// read paths — basic CRUD, entity listing, FQL and GraphQL — capturing CPU and
// heap pprof profiles and printing a top-N summary of each.
//
// Run (full default scale — 10M entities, needs a beefy box):
//
//	FLEXITYPE_TEST_DSN=postgres://... \
//	  go test -tags stress -run TestStress -timeout 60m .
//
// Run (reduced, box-friendly baseline — a few minutes):
//
//	FLEXITYPE_TEST_DSN=postgres://postgres:postgres@localhost:55433/stresstest?sslmode=disable \
//	  STRESS_ENTITIES=1000000 STRESS_VALUES_PER_ENTITY=3 \
//	  go test -tags stress -run TestStress -timeout 60m .
//
// Scale knobs (env, with defaults): STRESS_TYPES=200, STRESS_ATTRS=1000,
// STRESS_DEPTH=10, STRESS_ENTITIES=10000000, STRESS_VALUES_PER_ENTITY=3.
// STRESS_OUT overrides the profile/output directory.
//
// House style is goconvey Given/When/Then, but this is a load harness rather
// than a behavioural unit test: a plain testing.T with timing and logging is
// the right shape here, so it is deliberately NOT expressed as G/W/T.
package flexitype_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// ---- configuration ----

type stressConfig struct {
	Types           int
	Attrs           int
	Depth           int
	Entities        int
	ValuesPerEntity int
	OutDir          string
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	// Allow Go-style underscores (e.g. 10_000_000) for readability.
	clean := make([]byte, 0, len(v))
	for i := 0; i < len(v); i++ {
		if v[i] != '_' {
			clean = append(clean, v[i])
		}
	}
	n, err := strconv.Atoi(string(clean))
	if err != nil || n < 0 {
		return def
	}
	return n
}

func stressConfigFromEnv() stressConfig {
	out := os.Getenv("STRESS_OUT")
	if out == "" {
		out = "/private/tmp/claude-501/-Users-zkrebbekx-DevTools-git/37aba606-664b-41bc-ad5a-f6e02e9bb82f/scratchpad"
	}
	return stressConfig{
		Types:           envInt("STRESS_TYPES", 200),
		Attrs:           envInt("STRESS_ATTRS", 1000),
		Depth:           envInt("STRESS_DEPTH", 10),
		Entities:        envInt("STRESS_ENTITIES", 10_000_000),
		ValuesPerEntity: envInt("STRESS_VALUES_PER_ENTITY", 3),
		OutDir:          out,
	}
}

// ---- type-graph + attribute model ----

// canonical attribute internal names, present on every root (and therefore
// inherited by every entity type in the tenant). They give every leaf a known,
// queryable field of each storage class so the FQL/GraphQL scenarios can bind
// deterministically regardless of the random extra-attribute distribution.
const (
	cIntA = "cint_a" // integer, value_int = n % 100  -> `cint_a = 5` selects ~1%
	cIntB = "cint_b" // integer, value_int = n % 1000
	cFlt  = "cflt"   // float
	cBool = "cbool"  // bool
	cDate = "cdate"  // date
	cStr  = "cstr"   // string
	cDec  = "cdec"   // decimal (stored in value_text)
)

type attrRef struct {
	id       string
	dataType string
	version  int
}

// rootCanon holds the canonical attribute ids created on one root type.
type rootCanon struct {
	intA, intB, flt, bl, dt, str, dec attrRef
}

type typeNode struct {
	id           string
	internalName string
	depth        int
	rootIndex    int
	children     int
}

type typeGraph struct {
	nodes  []typeNode
	roots  []int       // indices of root nodes
	canon  []rootCanon // per-root canonical attribute refs (len == len(roots))
	leaves []int       // indices of nodes with no children
}

// ---- the harness ----

func TestStress(t *testing.T) {
	pool := openTestDB(t) // skips when FLEXITYPE_TEST_DSN is unset
	defer func() { _ = pool.Close() }()

	cfg := stressConfigFromEnv()
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		t.Fatalf("mkdir out: %v", err)
	}
	t.Logf("stress config: types=%d attrs=%d depth=%d entities=%d values/entity=%d out=%s",
		cfg.Types, cfg.Attrs, cfg.Depth, cfg.Entities, cfg.ValuesPerEntity, cfg.OutDir)

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

	// Start clean: truncate every flexitype_* table.
	truncateAll(t, pool)

	// The trigram GIN indexes on value_text (FQL contains/icontains) are the
	// most expensive to maintain under a bulk COPY and are not exercised by
	// any timed scenario here, so drop them on the throwaway DB to keep the
	// seed fast. The btree indexes that the =/range/has/aggregation paths use
	// are left intact, so the profile stays faithful.
	for _, idx := range []string{
		"idx_flexitype_attribute_value_trgm",
		"idx_flexitype_attribute_value_trgm_lower",
	} {
		if _, err := pool.Exec("DROP INDEX IF EXISTS " + idx); err != nil {
			t.Fatalf("drop index %s: %v", idx, err)
		}
	}

	// The entity-summary projection is maintained by a per-row trigger on
	// flexitype_attribute_value (migration 000019). Under a bulk COPY that fires
	// once per value row and would dominate the seed, so disable it for the load
	// and rebuild the projection in one grouped INSERT afterwards — the same
	// trade-off as dropping the trigram GIN indexes above.
	if _, err := pool.Exec(
		"ALTER TABLE flexitype_attribute_value DISABLE TRIGGER flexitype_entity_summary_maintain"); err != nil {
		t.Fatalf("disable entity-summary trigger: %v", err)
	}

	// ---- SEED ----
	seedStart := time.Now()
	graph := buildTypeGraph(ctx, t, svc, cfg)
	t.Logf("seed: %d types created (%d roots, %d leaves, max depth %d)",
		len(graph.nodes), len(graph.roots), len(graph.leaves), maxDepth(graph))
	seedAttributes(ctx, t, svc, cfg, graph)
	t.Logf("seed: %d attribute definitions created", cfg.Attrs)

	valueRows := bulkSeedEntities(t, pool, cfg, graph)

	// Re-enable the trigger (so post-seed CRUD scenarios keep the projection in
	// sync) and backfill the summary once over the freshly loaded values.
	if _, err := pool.Exec(
		"ALTER TABLE flexitype_attribute_value ENABLE TRIGGER flexitype_entity_summary_maintain"); err != nil {
		t.Fatalf("enable entity-summary trigger: %v", err)
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
		t.Fatalf("backfill entity summary: %v", err)
	}

	t.Log("seed: ANALYZE...")
	for _, tbl := range []string{
		"flexitype_type_definition",
		"flexitype_attribute_definition",
		"flexitype_attribute_value",
		"flexitype_entity_summary",
	} {
		if _, err := pool.Exec("ANALYZE " + tbl); err != nil {
			t.Fatalf("analyze %s: %v", tbl, err)
		}
	}

	var dbValueCount int
	if err := pool.Get(&dbValueCount, "SELECT count(*) FROM flexitype_attribute_value"); err != nil {
		t.Fatalf("count values: %v", err)
	}
	seedWall := time.Since(seedStart)
	t.Logf("SEED COMPLETE in %s: entities=%d valueRows(inserted)=%d valueRows(db)=%d",
		seedWall.Round(time.Millisecond), cfg.Entities, valueRows, dbValueCount)

	// Choose the deepest leaf as the scenario target: deep inheritance chain
	// (walked on every bind / effective-attribute resolution) plus a large
	// entity population (heavy per-page aggregation).
	deepLeaf := deepestLeaf(graph)
	leafPos := indexOf(graph.leaves, deepLeaf)
	var leafEntities int
	if len(graph.leaves) > 0 {
		leafEntities = countForLeaf(cfg.Entities, len(graph.leaves), leafPos)
	}
	leaf := graph.nodes[deepLeaf]
	rootName := graph.nodes[graph.roots[leaf.rootIndex]].internalName
	// An entity guaranteed to live on the deep leaf (n ≡ leafPos mod #leaves).
	deepEntityID := "e" + strconv.Itoa(leafPos)
	t.Logf("scenario target: type=%s (depth %d, ~%d entities), root=%s, sample entity=%s",
		leaf.internalName, leaf.depth, leafEntities, rootName, deepEntityID)

	// ---- PROFILE + SCENARIOS ----
	cpuPath := filepath.Join(cfg.OutDir, "flexitype-stress-cpu.pprof")
	heapPath := filepath.Join(cfg.OutDir, "flexitype-stress-heap.pprof")

	cpuFile, err := os.Create(cpuPath)
	if err != nil {
		t.Fatalf("create cpu profile: %v", err)
	}
	if err := pprof.StartCPUProfile(cpuFile); err != nil {
		t.Fatalf("start cpu profile: %v", err)
	}

	runCRUD(ctx, t, svc, graph, leaf)
	runDeepRead(ctx, t, svc, leaf, deepEntityID)
	runListEntities(ctx, t, svc, leaf)
	runFQL(ctx, t, svc, leaf, rootName)
	runGraphQL(ctx, t, svc, leaf)

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

	// ---- pprof top-N summaries ----
	cpuTop := pprofTop(t, cpuPath, "")
	allocTop := pprofTop(t, heapPath, "alloc_space")
	inuseTop := pprofTop(t, heapPath, "inuse_space")

	writeOut(t, filepath.Join(cfg.OutDir, "top-cpu.txt"), cpuTop)
	writeOut(t, filepath.Join(cfg.OutDir, "top-heap-alloc.txt"), allocTop)
	writeOut(t, filepath.Join(cfg.OutDir, "top-heap-inuse.txt"), inuseTop)

	t.Logf("\n==== CPU top-20 ====\n%s", cpuTop)
	t.Logf("\n==== HEAP alloc_space top-20 ====\n%s", allocTop)
	t.Logf("\n==== HEAP inuse_space top-20 ====\n%s", inuseTop)
	t.Logf("profiles: %s , %s", cpuPath, heapPath)
}

// ---- seeding: type graph ----

// buildTypeGraph creates cfg.Types entity types via the real API: ~min(20,
// Types) roots, and the remainder chained under roots via extends so that
// several chains reach cfg.Depth levels. Each root also gets the canonical
// attribute set, inherited by its whole subtree.
func buildTypeGraph(ctx context.Context, t *testing.T, svc *flexitype.Service, cfg stressConfig) typeGraph {
	it := svc.Interactors(ctx)
	numRoots := 20
	if cfg.Types < numRoots {
		numRoots = cfg.Types
	}
	if numRoots < 1 {
		numRoots = 1
	}

	g := typeGraph{}
	mkType := func(idx int, extendsID string, depth, rootIndex int) int {
		in := apptypedef.CreateInput{
			InternalName: "t" + strconv.Itoa(idx),
			DisplayName:  "Type " + strconv.Itoa(idx),
			ExtendsID:    extendsID,
		}
		snap, err := it.TypeDefinitions().Create(ctx, in)
		if err != nil {
			t.Fatalf("create type %d: %v", idx, err)
		}
		g.nodes = append(g.nodes, typeNode{
			id: snap.ID.String(), internalName: in.InternalName, depth: depth, rootIndex: rootIndex,
		})
		return len(g.nodes) - 1
	}

	// Roots (depth 1) + their canonical attributes.
	for r := 0; r < numRoots; r++ {
		ri := mkType(r, "", 1, r)
		g.roots = append(g.roots, ri)
		g.canon = append(g.canon, makeCanonical(ctx, t, it, g.nodes[ri].id))
	}

	// Chain the rest under roots. Grow each chain's tip until it reaches
	// cfg.Depth; afterwards branch fresh leaves off that chain's root.
	tips := make([]int, numRoots)
	copy(tips, g.roots)
	for idx := numRoots; idx < cfg.Types; idx++ {
		ci := (idx - numRoots) % numRoots
		tip := tips[ci]
		parent := tip
		if g.nodes[tip].depth >= cfg.Depth {
			parent = g.roots[ci] // chain full: branch off the root (depth 2)
		}
		child := mkType(idx, g.nodes[parent].id, g.nodes[parent].depth+1, ci)
		g.nodes[parent].children++
		if parent == tip {
			tips[ci] = child
		}
	}

	for i := range g.nodes {
		if g.nodes[i].children == 0 {
			g.leaves = append(g.leaves, i)
		}
	}
	return g
}

// makeCanonical creates the 7 canonical attributes on a root type.
func makeCanonical(ctx context.Context, t *testing.T, it *application.Interactors, typeID string) rootCanon {
	mk := func(name, dt string) attrRef {
		snap, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: dt,
		})
		if err != nil {
			t.Fatalf("create canonical attr %s: %v", name, err)
		}
		return attrRef{id: snap.ID.String(), dataType: dt, version: snap.Version}
	}
	return rootCanon{
		intA: mk(cIntA, "integer"),
		intB: mk(cIntB, "integer"),
		flt:  mk(cFlt, "float"),
		bl:   mk(cBool, "bool"),
		dt:   mk(cDate, "date"),
		str:  mk(cStr, "string"),
		dec:  mk(cDec, "decimal"),
	}
}

// seedAttributes distributes the remaining (non-canonical) attribute budget
// across every type, cycling data types, for schema breadth. These extras are
// not written as values; they widen the effective-attribute sets that the bind
// / GraphQL-schema-build paths must walk.
func seedAttributes(ctx context.Context, t *testing.T, svc *flexitype.Service, cfg stressConfig, g typeGraph) {
	it := svc.Interactors(ctx)
	canonicalCount := len(g.roots) * 7
	extra := cfg.Attrs - canonicalCount
	if extra <= 0 || len(g.nodes) == 0 {
		return
	}
	dtypes := []string{"string", "integer", "float", "bool", "decimal", "date"}
	for i := 0; i < extra; i++ {
		node := g.nodes[i%len(g.nodes)]
		dt := dtypes[i%len(dtypes)]
		if _, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: node.id,
			InternalName:     "x" + strconv.Itoa(i),
			DisplayName:      "X" + strconv.Itoa(i),
			DataType:         dt,
		}); err != nil {
			t.Fatalf("create extra attr %d: %v", i, err)
		}
	}
}

// ---- seeding: entities via COPY ----

var valueCopyCols = []string{
	"id", "tenant_id", "type_definition_id", "attribute_definition_id", "entity_id",
	"data_type", "value_bool", "value_int", "value_float", "value_text", "value_time",
	"value_json", "definition_version", "created_at", "updated_at",
}

const copyBatchRows = 100_000

// bulkSeedEntities inserts cfg.Entities entities via pq.CopyIn in batches. Each
// entity is assigned round-robin to a leaf type and gets cfg.ValuesPerEntity
// value rows drawn from its (inherited) canonical attributes, with values
// generated deterministically so predicate selectivity is known.
func bulkSeedEntities(t *testing.T, pool interface {
	Begin() (*sql.Tx, error)
}, cfg stressConfig, g typeGraph) int {
	if len(g.leaves) == 0 || cfg.Entities <= 0 || cfg.ValuesPerEntity <= 0 {
		return 0
	}
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	start := time.Now()

	var txn *sql.Tx
	var stmt *sql.Stmt
	openBatch := func() {
		var err error
		txn, err = pool.Begin()
		if err != nil {
			t.Fatalf("begin copy txn: %v", err)
		}
		//nolint:staticcheck // pq.CopyIn (the COPY query builder) is the required bulk-load path for this harness (issue #212).
		stmt, err = txn.Prepare(pq.CopyIn("flexitype_attribute_value", valueCopyCols...))
		if err != nil {
			t.Fatalf("prepare copy: %v", err)
		}
	}
	flush := func() {
		if _, err := stmt.Exec(); err != nil { // no-arg Exec flushes the COPY
			t.Fatalf("copy flush: %v", err)
		}
		if err := stmt.Close(); err != nil {
			t.Fatalf("copy close: %v", err)
		}
		if err := txn.Commit(); err != nil {
			t.Fatalf("copy commit: %v", err)
		}
	}

	total := 0
	rowsInBatch := 0
	emit := func(row []any) {
		if rowsInBatch == 0 {
			openBatch()
		}
		if _, err := stmt.Exec(row...); err != nil {
			t.Fatalf("copy exec: %v", err)
		}
		rowsInBatch++
		total++
		if rowsInBatch >= copyBatchRows {
			flush()
			rowsInBatch = 0
			t.Logf("seed: %d value rows in %s (%.0f rows/s)",
				total, time.Since(start).Round(time.Millisecond),
				float64(total)/time.Since(start).Seconds())
		}
	}

	numLeaves := len(g.leaves)
	for n := 0; n < cfg.Entities; n++ {
		leafIdx := g.leaves[n%numLeaves]
		leaf := g.nodes[leafIdx]
		canon := g.canon[leaf.rootIndex]
		entityID := "e" + strconv.Itoa(n)
		ts := base.Add(time.Duration(n) * time.Second)
		// Derive values from the within-leaf sequence index k, NOT from n: a
		// leaf holds entities where n ≡ pos (mod numLeaves), so any function of
		// n mod m (m sharing a factor with numLeaves) would be constant across
		// the leaf and break predicate selectivity. k = n/numLeaves ranges over
		// all integers within the leaf, so k%100 == 5 selects ~1% of it.
		k := n / numLeaves

		for v := 0; v < cfg.ValuesPerEntity; v++ {
			var ref attrRef
			row := baseRow(leaf.id, entityID, ts)
			switch v {
			case 0:
				ref = canon.intA
				setCol(row, "value_int", int64(k%100))
			case 1:
				ref = canon.intB
				setCol(row, "value_int", int64(k%1000))
			default:
				switch (k + v) % 5 {
				case 0:
					ref = canon.flt
					setCol(row, "value_float", float64(k%1000)/10.0)
				case 1:
					ref = canon.bl
					setCol(row, "value_bool", k%2 == 0)
				case 2:
					ref = canon.dt
					setCol(row, "value_time", base.AddDate(0, 0, k%365))
				case 3:
					ref = canon.str
					setCol(row, "value_text", "s"+strconv.Itoa(k%50))
				case 4:
					ref = canon.dec
					setCol(row, "value_text", fmt.Sprintf("%d.%02d", k%100, k%100))
				}
			}
			setCol(row, "attribute_definition_id", ref.id)
			setCol(row, "data_type", ref.dataType)
			setCol(row, "definition_version", ref.version)
			emit(row)
		}
	}
	if rowsInBatch > 0 {
		flush()
	}
	return total
}

// baseRow builds a value row with the always-present columns filled and every
// typed value column NULL; callers set the one column matching the data type.
func baseRow(typeID, entityID string, ts time.Time) []any {
	row := make([]any, len(valueCopyCols))
	setCol(row, "id", ulid.New().String())
	setCol(row, "tenant_id", string(valueobjects.DefaultTenant))
	setCol(row, "type_definition_id", typeID)
	setCol(row, "entity_id", entityID)
	setCol(row, "created_at", ts)
	setCol(row, "updated_at", ts)
	return row
}

var valueColIndex = func() map[string]int {
	m := make(map[string]int, len(valueCopyCols))
	for i, c := range valueCopyCols {
		m[c] = i
	}
	return m
}()

func setCol(row []any, col string, v any) {
	row[valueColIndex[col]] = v
}

// ---- timed scenarios ----

type timing struct {
	name string
	durs []time.Duration
}

func (ti timing) report(t *testing.T, extra string) {
	if len(ti.durs) == 0 {
		return
	}
	sorted := append([]time.Duration(nil), ti.durs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p50 := sorted[len(sorted)*50/100]
	p95 := sorted[min(len(sorted)-1, len(sorted)*95/100)]
	if len(sorted) == 1 {
		t.Logf("  %-34s single=%s  %s", ti.name, sorted[0].Round(time.Microsecond), extra)
		return
	}
	t.Logf("  %-34s n=%d p50=%s p95=%s min=%s max=%s  %s",
		ti.name, len(sorted),
		p50.Round(time.Microsecond), p95.Round(time.Microsecond),
		sorted[0].Round(time.Microsecond), sorted[len(sorted)-1].Round(time.Microsecond), extra)
}

func timeN(n int, fn func()) timing {
	ti := timing{durs: make([]time.Duration, 0, n)}
	for i := 0; i < n; i++ {
		s := time.Now()
		fn()
		ti.durs = append(ti.durs, time.Since(s))
	}
	return ti
}

func runCRUD(ctx context.Context, t *testing.T, svc *flexitype.Service, g typeGraph, leaf typeNode) {
	t.Log("SCENARIO: CRUD (create / read / update / delete a single entity)")
	it := svc.Interactors(ctx)
	canon := g.canon[leaf.rootIndex]
	const entity = "crud-entity"

	// Create: set two values on a fresh entity of the deep leaf type. We pin
	// TypeDefinitionID to the leaf so the value rows carry the leaf's id (the
	// canonical attributes are declared on the root).
	create := timeN(20, func() {
		for i, ref := range []attrRef{canon.intA, canon.intB} {
			if _, err := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: ref.id,
				TypeDefinitionID:      leaf.id,
				EntityID:              entity,
				Value:                 json.RawMessage(strconv.Itoa(7 + i)),
			}); err != nil {
				t.Fatalf("crud create set: %v", err)
			}
		}
	})
	create.report(t, "(2 Set/iter)")

	read := timeN(50, func() {
		if _, err := it.Values().ListByEntity(ctx, leaf.id, entity); err != nil {
			t.Fatalf("crud read: %v", err)
		}
	})
	read.report(t, "(ListByEntity)")

	update := timeN(50, func() {
		if _, err := it.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: canon.intA.id,
			TypeDefinitionID:      leaf.id,
			EntityID:              entity,
			Value:                 json.RawMessage("42"),
		}); err != nil {
			t.Fatalf("crud update: %v", err)
		}
	})
	update.report(t, "(Set existing)")

	del := timeN(20, func() {
		if _, err := it.Values().RemoveEntity(ctx, leaf.id, entity); err != nil {
			t.Fatalf("crud delete: %v", err)
		}
		// Re-create so the next delete iteration has something to remove.
		if _, err := it.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: canon.intA.id, TypeDefinitionID: leaf.id,
			EntityID: entity, Value: json.RawMessage("1"),
		}); err != nil {
			t.Fatalf("crud delete re-seed: %v", err)
		}
	})
	del.report(t, "(RemoveEntity+reseed)")
	_, _ = it.Values().RemoveEntity(ctx, leaf.id, entity)
}

func runDeepRead(ctx context.Context, t *testing.T, svc *flexitype.Service, leaf typeNode, entityID string) {
	t.Log("SCENARIO: deep-hierarchy single-entity read")
	it := svc.Interactors(ctx)

	read := timeN(30, func() {
		if _, err := it.Values().ListByEntity(ctx, leaf.id, entityID); err != nil {
			t.Fatalf("deep read: %v", err)
		}
	})
	read.report(t, fmt.Sprintf("(depth %d, entity %s)", leaf.depth, entityID))

	// EffectiveAttributes is the true deep-hierarchy cost (walks the extends
	// chain); time it separately.
	var effCount int
	eff := timeN(30, func() {
		effs, err := it.TypeDefinitions().EffectiveAttributes(ctx, leaf.id)
		if err != nil {
			t.Fatalf("effective attrs: %v", err)
		}
		effCount = len(effs)
	})
	eff.report(t, fmt.Sprintf("(EffectiveAttributes, %d attrs)", effCount))
}

func runListEntities(ctx context.Context, t *testing.T, svc *flexitype.Service, leaf typeNode) {
	t.Log("SCENARIO: entity listing (per-page aggregation hot path)")
	it := svc.Interactors(ctx)
	limit := 20

	var firstInfo db.PageInfo
	first := timeN(5, func() {
		out, err := it.Values().ListEntities(ctx, leaf.id, false, db.PageArgs{Limit: &limit})
		if err != nil {
			t.Fatalf("list entities first page: %v", err)
		}
		firstInfo = out.PageInfo
	})
	first.report(t, fmt.Sprintf("(first page, hasNext=%v)", firstInfo.HasNextPage))

	// Walk a couple of cursor pages.
	cursor := firstInfo.NextCursor
	for p := 0; p < 2 && cursor != nil; p++ {
		cur := cursor
		var nextInfo db.PageInfo
		page := timeN(3, func() {
			out, err := it.Values().ListEntities(ctx, leaf.id, false, db.PageArgs{Limit: &limit, Cursor: cur})
			if err != nil {
				t.Fatalf("list entities cursor page: %v", err)
			}
			nextInfo = out.PageInfo
		})
		page.report(t, fmt.Sprintf("(cursor page %d)", p+1))
		cursor = nextInfo.NextCursor
	}
}

func runFQL(ctx context.Context, t *testing.T, svc *flexitype.Service, leaf typeNode, rootName string) {
	t.Log("SCENARIO: FQL (it.Query().Execute)")
	it := svc.Interactors(ctx)

	type fqlCase struct {
		label     string
		queryType string
		query     string
	}
	cases := []fqlCase{
		{"comparison =", leaf.internalName, cIntA + " = 5"},
		{"range", leaf.internalName, "range(" + cIntA + ", 5, 14)"},
		{"presence has()", leaf.internalName, "has(" + cBool + ")"},
		{"compound boolean", leaf.internalName, cIntA + " = 5 and " + cIntB + " > 500"},
		{"type isa (subtree)", rootName, "type isa \"" + rootName + "\""},
	}
	limit := 20
	for _, c := range cases {
		var total *int
		var count int
		ti := timeN(5, func() {
			out, err := it.Query().Execute(ctx, appquery.ExecuteInput{
				Type:  c.queryType,
				Query: c.query,
				Page:  db.PageArgs{Limit: &limit, WantTotal: true},
			})
			if err != nil {
				t.Fatalf("fql %q: %v", c.query, err)
			}
			count = len(out.Items)
			total = out.PageInfo.TotalCount
		})
		tot := "?"
		if total != nil {
			tot = strconv.Itoa(*total)
		}
		ti.report(t, fmt.Sprintf("[%s] %q page=%d total=%s", c.label, c.query, count, tot))
	}
}

func runGraphQL(ctx context.Context, t *testing.T, svc *flexitype.Service, leaf typeNode) {
	t.Log("SCENARIO: GraphQL (top-level connection, first:20, nested selection)")
	it := svc.Interactors(ctx)
	gctx := application.WithInteractors(ctx, it)
	engine := svc.GraphQLEngine()
	if engine == nil {
		t.Fatal("no GraphQL engine")
	}

	query := fmt.Sprintf(`{
      %s(first: 20) {
        totalCount
        edges {
          cursor
          node { entityId %s %s %s }
        }
        pageInfo { hasNextPage endCursor }
      }
    }`, leaf.internalName, cIntA, cIntB, cBool)

	// The first Execute also builds (and caches) the tenant schema across all
	// types — time it on its own, then time steady-state execution.
	firstStart := time.Now()
	res := engine.Execute(gctx, query, nil)
	firstDur := time.Since(firstStart)
	if len(res.Errors) > 0 {
		t.Fatalf("graphql errors (schema build + exec): %v", res.Errors)
	}
	t.Logf("  %-34s single=%s (includes per-tenant schema build over all types)",
		"graphql first exec", firstDur.Round(time.Microsecond))

	steady := timeN(10, func() {
		r := engine.Execute(gctx, query, nil)
		if len(r.Errors) > 0 {
			t.Fatalf("graphql errors: %v", r.Errors)
		}
	})
	steady.report(t, "(cached schema)")
}

// ---- pprof helpers ----

func pprofTop(t *testing.T, profile, sampleIndex string) string {
	args := []string{"tool", "pprof", "-top", "-nodecount=20"}
	if sampleIndex != "" {
		args = append(args, "-sample_index="+sampleIndex)
	}
	args = append(args, profile)
	cmd := exec.Command("go", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("go tool pprof %v failed: %v\n%s", args, err, out)
		return string(out)
	}
	return string(out)
}

func writeOut(t *testing.T, path, content string) {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Logf("write %s: %v", path, err)
	}
}

// ---- small graph utilities ----

func maxDepth(g typeGraph) int {
	m := 0
	for _, n := range g.nodes {
		if n.depth > m {
			m = n.depth
		}
	}
	return m
}

func deepestLeaf(g typeGraph) int {
	best := g.leaves[0]
	for _, li := range g.leaves {
		if g.nodes[li].depth > g.nodes[best].depth {
			best = li
		}
	}
	return best
}

func indexOf(s []int, v int) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// countForLeaf counts how many of the first `entities` indices land on the
// leaf at position `pos` under round-robin (n % numLeaves) assignment.
func countForLeaf(entities, numLeaves, pos int) int {
	if numLeaves == 0 {
		return 0
	}
	full := entities / numLeaves
	rem := entities % numLeaves
	if pos < rem {
		return full + 1
	}
	return full
}
