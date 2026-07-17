// Package gql serves a read-only GraphQL API whose schema mirrors a tenant's
// live type definitions: each entity type becomes an object, each attribute a
// field, and each relationship a nested Relay connection resolved through the
// batched repositories (no N+1). Top-level fields and relationship fields are
// Relay connections — edges/node/cursor plus pageInfo and totalCount — paged
// with first/after. The schema is built per tenant and per caller-permission
// set (an attribute the caller may not read is not a field, so introspection
// never leaks it) and is rebuilt whenever the tenant's persisted schema version
// changes, so a definition change made on any replica propagates to every
// replica (issue #192). FQL is exposed as a filter argument on each top-level
// field.
package gql

import (
	"container/list"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/gqlerrors"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/source"

	"github.com/zkrebbekx/flexitype/application"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// Limits bound query cost.
const (
	maxRelDepth    = 6                // nested relationship-connection hops
	defaultFirst   = 50               // connection page size when unspecified
	maxFirst       = 200              // hard cap on a connection page
	typeListLimit  = 500              // schema-build enumeration page size
	maxRelChildren = 500              // per-parent relationship fan-out cap
	maxQueryFields = 2000             // total field selections in one document
	execTimeout    = 30 * time.Second // whole-query execution budget
)

// checkQueryCost bounds a document's total field-selection count before any
// resolution runs. The relationship-depth cap limits nesting, but not width:
// a field aliased K times per level, or thousands of aliased top-level
// connections, expands into K+K^2+... field nodes — each a full FQL/DB
// execution. Counting selections (expanding fragment spreads) rejects such an
// amplified document up front. A syntactically invalid query passes here and is
// reported by graphql.Do.
func checkQueryCost(query string) error {
	doc, err := parser.Parse(parser.ParseParams{Source: source.NewSource(&source.Source{Body: []byte(query)})})
	if err != nil {
		return nil
	}
	frags := map[string]*ast.FragmentDefinition{}
	for _, d := range doc.Definitions {
		if f, ok := d.(*ast.FragmentDefinition); ok {
			frags[f.Name.Value] = f
		}
	}
	total := 0
	var walk func(sel *ast.SelectionSet, depth int)
	walk = func(sel *ast.SelectionSet, depth int) {
		// depth guards against cyclic fragment spreads; total>max short-circuits.
		if sel == nil || depth > 64 || total > maxQueryFields {
			return
		}
		for _, s := range sel.Selections {
			switch n := s.(type) {
			case *ast.Field:
				total++
				walk(n.SelectionSet, depth+1)
			case *ast.InlineFragment:
				walk(n.SelectionSet, depth+1)
			case *ast.FragmentSpread:
				if f := frags[n.Name.Value]; f != nil {
					walk(f.SelectionSet, depth+1)
				}
			}
			if total > maxQueryFields {
				return
			}
		}
	}
	for _, d := range doc.Definitions {
		if op, ok := d.(*ast.OperationDefinition); ok {
			walk(op.SelectionSet, 0)
		}
	}
	if total > maxQueryFields {
		return fmt.Errorf("query too complex: over %d total field selections", maxQueryFields)
	}
	return nil
}

// Engine builds and caches per-tenant GraphQL schemas and executes queries.
// It is safe for concurrent use.
//
// A tenant's schema is derived from its live definitions; each cached schema is
// keyed by tenant+access and tagged with the tenant's PERSISTED schema version
// (flexitype_schema_version, bumped by a trigger on any definition change —
// issue #192). Deriving staleness from that persisted version, rather than an
// in-process event counter, is what makes the cache correct across replicas:
// event delivery is once-per-cluster, but every replica reads the same
// persisted version. A short-TTL per-tenant memo keeps the common path off the
// database, and a definition event invalidates the writing replica's memo so it
// observes its own change at once. The schema cache is LRU-bounded so it cannot
// grow without limit.
type Engine struct {
	mu       sync.Mutex
	memo     map[string]versionMemo   // tenant -> last-read persisted version
	memoTTL  time.Duration            // how long a memo entry is trusted
	cacheCap int                      // max cached schemas (LRU-evicted past it)
	ll       *list.List               // LRU recency order; front = most recent
	cache    map[string]*list.Element // tenant|access -> *cacheEntry element
	now      func() time.Time         // clock; swappable in tests
}

// versionMemo caches a tenant's persisted schema version to spare a database
// read on every query; it is trusted for memoTTL, then re-read.
type versionMemo struct {
	version   uint64
	fetchedAt time.Time
}

// cacheEntry is one built schema tagged with the persisted version it was built
// against; it lives in the LRU list.
type cacheEntry struct {
	key    string
	gen    uint64
	schema graphql.Schema
}

// Defaults balance a cross-replica staleness window against database load and
// memory: the memo re-reads a tenant's version at most every defaultMemoTTL, and
// at most defaultCacheCap (tenant|access) schemas are retained.
const (
	defaultMemoTTL  = 5 * time.Second
	defaultCacheCap = 256
)

// EngineOption configures an Engine.
type EngineOption func(*Engine)

// WithMemoTTL sets how long a tenant's persisted schema version is trusted
// before it is re-read. Zero forces a read on every query (used in tests to
// observe a cross-replica change deterministically).
func WithMemoTTL(d time.Duration) EngineOption { return func(e *Engine) { e.memoTTL = d } }

// WithCacheCap bounds the number of cached (tenant|access) schemas; the
// least-recently-used entry is evicted once the cap is exceeded. Non-positive
// values are ignored.
func WithCacheCap(n int) EngineOption {
	return func(e *Engine) {
		if n > 0 {
			e.cacheCap = n
		}
	}
}

// NewEngine builds a GraphQL engine. The zero-argument form uses the defaults
// (5s version memo, 256-schema LRU) so existing call sites keep working.
func NewEngine(opts ...EngineOption) *Engine {
	e := &Engine{
		memo:     map[string]versionMemo{},
		memoTTL:  defaultMemoTTL,
		cacheCap: defaultCacheCap,
		ll:       list.New(),
		cache:    map[string]*list.Element{},
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Name identifies the dispatcher subscriber.
func (e *Engine) Name() string { return "graphql-schema" }

// EventTypes are the definition events that invalidate a tenant's schema.
func (e *Engine) EventTypes() []events.Type {
	return []events.Type{
		domaintypedef.EventCreated, domaintypedef.EventUpdated,
		domaintypedef.EventArchived, domaintypedef.EventRestored,
		domainattribute.EventCreated, domainattribute.EventUpdated,
		domainattribute.EventArchived, domainattribute.EventRestored,
		domainrelationship.EventDefinitionCreated, domainrelationship.EventDefinitionUpdated,
		domainrelationship.EventDefinitionArchived, domainrelationship.EventDefinitionRestored,
	}
}

// Handle is a fast-path hint, not the correctness mechanism: on a definition
// event it drops the tenant's version memo so the replica that made the change
// re-reads the (now bumped) persisted version on its next query and rebuilds
// immediately, without waiting for the memo TTL. Replicas that never receive
// the event still converge, once their memo expires, via the persisted version
// alone — so correctness never depends on event delivery (issue #192).
func (e *Engine) Handle(_ context.Context, env events.Envelope) error {
	if env.TenantID == "" {
		return nil
	}
	e.mu.Lock()
	delete(e.memo, env.TenantID)
	e.mu.Unlock()
	return nil
}

// Result is a GraphQL execution result: data plus any errors, ready to
// serialize as the GraphQL response body.
type Result = graphql.Result

// Execute runs a GraphQL query against the caller's tenant schema. The context
// must already carry the tenant, actor and access (the API middleware sets
// them); the interactors are read from it.
func (e *Engine) Execute(ctx context.Context, query string, variables map[string]any) *Result {
	inter := application.FromContext(ctx)
	if inter == nil {
		return resultErr(fmt.Errorf("no interactors on context"))
	}
	if err := checkQueryCost(query); err != nil {
		return resultErr(err)
	}
	schema, err := e.schemaFor(ctx, inter)
	if err != nil {
		return resultErr(err)
	}
	// Bound total execution: a costly query cannot run indefinitely holding a
	// goroutine and database connections.
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	return graphql.Do(graphql.Params{
		Schema:         schema,
		RequestString:  query,
		VariableValues: variables,
		Context:        ctx,
	})
}

// schemaFor returns the tenant+access schema, rebuilding when the tenant's
// persisted schema version has moved past the cached entry's, or when this
// permission set has not been seen. The version is read through the
// request-scoped interactors (the same handle buildSchema uses) and memoised
// per tenant for a short TTL to keep the hot path off the database.
func (e *Engine) schemaFor(ctx context.Context, inter *application.Interactors) (graphql.Schema, error) {
	tenant := uow.TenantFromContext(ctx).String()
	key := tenant + "|" + accessSignature(uow.AccessFromContext(ctx))

	gen, gotVersion := e.versionFor(ctx, inter, tenant)

	e.mu.Lock()
	cached, ok := e.cacheGet(key)
	e.mu.Unlock()
	switch {
	case ok && gotVersion && cached.gen == gen:
		// Cache hit at the current version.
		return cached.schema, nil
	case ok && !gotVersion:
		// The version read failed: never fail the query over a stale-check miss
		// — reuse the last good schema for this key rather than rebuild blindly.
		return cached.schema, nil
	}

	schema, err := buildSchema(ctx, inter)
	if err != nil {
		return graphql.Schema{}, err
	}
	e.mu.Lock()
	e.cachePut(key, gen, schema)
	e.mu.Unlock()
	return schema, nil
}

// versionFor returns the tenant's persisted schema version, memoised for
// memoTTL. The bool is false only when the version had to be read (memo empty or
// expired) and that read failed; callers then fall back to the cached schema
// rather than surface the error.
func (e *Engine) versionFor(ctx context.Context, inter *application.Interactors, tenant string) (uint64, bool) {
	now := e.now()
	e.mu.Lock()
	m, ok := e.memo[tenant]
	fresh := ok && e.memoTTL > 0 && now.Sub(m.fetchedAt) < e.memoTTL
	e.mu.Unlock()
	if fresh {
		return m.version, true
	}

	v, err := inter.SchemaVersion(ctx)
	if err != nil {
		// Leave any prior memo untouched; report the miss so schemaFor falls
		// back to the cached schema.
		return 0, false
	}
	e.mu.Lock()
	e.memo[tenant] = versionMemo{version: v, fetchedAt: now}
	e.mu.Unlock()
	return v, true
}

// cacheGet returns key's cache entry and marks it most-recently-used. The
// caller holds e.mu.
func (e *Engine) cacheGet(key string) (cacheEntry, bool) {
	el, ok := e.cache[key]
	if !ok {
		return cacheEntry{}, false
	}
	e.ll.MoveToFront(el)
	return *el.Value.(*cacheEntry), true
}

// cachePut inserts or refreshes key's schema, marks it most-recently-used and
// evicts the least-recently-used entry once the cap is exceeded. The caller
// holds e.mu.
func (e *Engine) cachePut(key string, gen uint64, schema graphql.Schema) {
	if el, ok := e.cache[key]; ok {
		ent := el.Value.(*cacheEntry)
		ent.gen = gen
		ent.schema = schema
		e.ll.MoveToFront(el)
		return
	}
	e.cache[key] = e.ll.PushFront(&cacheEntry{key: key, gen: gen, schema: schema})
	if e.cacheCap > 0 && e.ll.Len() > e.cacheCap {
		if oldest := e.ll.Back(); oldest != nil {
			e.ll.Remove(oldest)
			delete(e.cache, oldest.Value.(*cacheEntry).key)
		}
	}
}

// accessSignature keys the schema cache by the caller's readable-attribute set
// so introspection stays permission-scoped without rebuilding per request.
func accessSignature(a uow.Access) string {
	if a.Admin {
		return "admin"
	}
	if len(a.Attr) == 0 {
		return "open"
	}
	parts := make([]string, 0, len(a.Attr))
	for name, perm := range a.Attr {
		parts = append(parts, name+":"+string(perm))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func resultErr(err error) *graphql.Result {
	return &graphql.Result{Errors: []gqlerrors.FormattedError{{Message: err.Error()}}}
}

// ---- schema construction ----

type attrMeta struct {
	attrID   string
	dataType valueobjects.DataType
	multi    bool
}

type relMeta struct {
	defID          string
	otherType      string // internal name of the type at the other end
	entityIsParent bool   // this type sits on the parent side
	symmetric      bool
}

type typeMeta struct {
	internalName string
	attrByField  map[string]attrMeta
	relByField   map[string]relMeta
}

// connectionArgs are the Relay forward-pagination arguments shared by every
// connection field.
func connectionArgs(withFilter bool) graphql.FieldConfigArgument {
	args := graphql.FieldConfigArgument{
		"first": &graphql.ArgumentConfig{Type: graphql.Int, Description: "page size (default 50, max 200)"},
		"after": &graphql.ArgumentConfig{Type: graphql.String, Description: "opaque cursor: return items after it"},
	}
	if withFilter {
		args["filter"] = &graphql.ArgumentConfig{Type: graphql.String, Description: "FQL filter expression"}
	}
	return args
}

// buildSchema reads the caller's live, readable schema and assembles a GraphQL
// schema mirroring it, with Relay connections.
func buildSchema(ctx context.Context, inter *application.Interactors) (graphql.Schema, error) {
	types, err := listEntityTypes(ctx, inter)
	if err != nil {
		return graphql.Schema{}, err
	}
	typeByID := make(map[string]string, len(types))
	for _, t := range types {
		typeByID[t.ID.String()] = t.InternalName
	}

	metas := make(map[string]typeMeta, len(types))
	for _, t := range types {
		effs, err := inter.TypeDefinitions().EffectiveAttributes(ctx, t.ID.String())
		if err != nil {
			return graphql.Schema{}, err
		}
		tm := typeMeta{internalName: t.InternalName, attrByField: map[string]attrMeta{}, relByField: map[string]relMeta{}}
		for _, e := range effs {
			a := e.Attribute
			tm.attrByField[a.InternalName] = attrMeta{attrID: a.ID.String(), dataType: a.DataType, multi: a.MultiValued}
		}
		metas[t.InternalName] = tm
	}

	if err := addRelationshipFields(ctx, inter, typeByID, metas); err != nil {
		return graphql.Schema{}, err
	}

	pageInfo := pageInfoObject()

	// Objects reference each other (and their own connections), so build the
	// object, edge and connection types with lazy thunks over shared maps.
	objects := make(map[string]*graphql.Object, len(metas))
	connections := make(map[string]*graphql.Object, len(metas))
	for name := range metas {
		objects[name] = graphql.NewObject(graphql.ObjectConfig{
			Name:   typeName(name),
			Fields: fieldsThunk(name, metas, connections),
		})
	}
	for name := range metas {
		obj := objects[name]
		edge := graphql.NewObject(graphql.ObjectConfig{
			Name: typeName(name) + "Edge",
			Fields: graphql.Fields{
				"node":   &graphql.Field{Type: obj},
				"cursor": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
			},
		})
		connections[name] = graphql.NewObject(graphql.ObjectConfig{
			Name: typeName(name) + "Connection",
			Fields: graphql.Fields{
				"edges":    &graphql.Field{Type: graphql.NewList(edge)},
				"pageInfo": &graphql.Field{Type: graphql.NewNonNull(pageInfo)},
				// Nullable: the total is computed only when this field is selected.
				"totalCount": &graphql.Field{Type: graphql.Int, Description: "total matching rows; computed only when requested"},
			},
		})
	}

	rootFields := graphql.Fields{
		// Always present so the Query type is non-empty even for an empty
		// tenant, and as a lightweight schema-discovery aid.
		"_schemaTypes": &graphql.Field{
			Type: graphql.NewList(graphql.NewNonNull(graphql.String)),
			Resolve: func(graphql.ResolveParams) (any, error) {
				names := make([]string, 0, len(metas))
				for n := range metas {
					names = append(names, n)
				}
				sort.Strings(names)
				return names, nil
			},
		},
	}
	for name := range metas {
		internal := name
		rootFields[internal] = &graphql.Field{
			Type:    connections[internal],
			Args:    connectionArgs(true),
			Resolve: rootResolve(internal, metas),
		}
	}

	return graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{Name: "Query", Fields: rootFields}),
	})
}

// pageInfoObject builds the shared Relay PageInfo type.
func pageInfoObject() *graphql.Object {
	return graphql.NewObject(graphql.ObjectConfig{
		Name: "PageInfo",
		Fields: graphql.Fields{
			"hasNextPage":     &graphql.Field{Type: graphql.NewNonNull(graphql.Boolean)},
			"hasPreviousPage": &graphql.Field{Type: graphql.NewNonNull(graphql.Boolean)},
			"startCursor":     &graphql.Field{Type: graphql.String},
			"endCursor":       &graphql.Field{Type: graphql.String},
		},
	})
}

func listEntityTypes(ctx context.Context, inter *application.Interactors) ([]domaintypedef.Snapshot, error) {
	var out []domaintypedef.Snapshot
	var cursor *string
	for {
		page, err := inter.TypeDefinitions().List(ctx, apptypedef.ListInput{Page: db.PageArgs{Limit: intPtr(typeListLimit), Cursor: cursor}})
		if err != nil {
			return nil, err
		}
		for _, t := range page.Items {
			if t.Kind == domaintypedef.KindEntity && t.ArchivedAt == nil {
				out = append(out, t)
			}
		}
		if !page.PageInfo.HasNextPage {
			break
		}
		cursor = page.PageInfo.NextCursor
	}
	return out, nil
}

// addRelationshipFields adds a nested-selectable connection field on each
// endpoint type for every live relationship definition.
func addRelationshipFields(ctx context.Context, inter *application.Interactors, typeByID map[string]string, metas map[string]typeMeta) error {
	var cursor *string
	for {
		page, err := inter.Relationships().ListDefinitions(ctx, apprelationship.DefinitionListInput{Page: db.PageArgs{Limit: intPtr(typeListLimit), Cursor: cursor}})
		if err != nil {
			return err
		}
		for _, d := range page.Items {
			if d.ArchivedAt != nil {
				continue
			}
			parent := typeByID[d.ParentTypeID.String()]
			child := typeByID[d.ChildTypeID.String()]
			if d.Kind == domainrelationship.KindSymmetric {
				name := sanitizeName(firstNonEmpty(d.InternalName))
				addRelField(metas, parent, name, relMeta{defID: d.ID.String(), otherType: child, symmetric: true})
				continue
			}
			addRelField(metas, parent, sanitizeName(firstNonEmpty(d.ChildLabel, d.InternalName)),
				relMeta{defID: d.ID.String(), otherType: child, entityIsParent: true})
			addRelField(metas, child, sanitizeName(firstNonEmpty(d.ParentLabel, d.InternalName)),
				relMeta{defID: d.ID.String(), otherType: parent, entityIsParent: false})
		}
		if !page.PageInfo.HasNextPage {
			break
		}
		cursor = page.PageInfo.NextCursor
	}
	return nil
}

// addRelField registers a relationship field, avoiding collisions with
// attribute fields or earlier relationship fields on the same type.
func addRelField(metas map[string]typeMeta, typeInternal, field string, rm relMeta) {
	tm, ok := metas[typeInternal]
	if !ok || field == "" || rm.otherType == "" {
		return
	}
	if _, taken := tm.attrByField[field]; taken {
		field += "_rel"
	}
	if _, taken := tm.relByField[field]; taken {
		return
	}
	tm.relByField[field] = rm
}

// fieldsThunk lazily builds an object's fields once every connection exists.
func fieldsThunk(typeInternal string, metas map[string]typeMeta, connections map[string]*graphql.Object) graphql.FieldsThunk {
	return func() graphql.Fields {
		tm := metas[typeInternal]
		fields := graphql.Fields{
			"entityId": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		}
		for field, am := range tm.attrByField {
			fields[field] = &graphql.Field{Type: gqlType(am.dataType, am.multi)}
		}
		for field, rm := range tm.relByField {
			if conn, ok := connections[rm.otherType]; ok {
				fields[field] = &graphql.Field{Type: conn, Args: connectionArgs(false)}
			}
		}
		return fields
	}
}

// ---- resolution (batched: no N+1) ----

// rootResolve runs the FQL filter (or lists all) for a top-level connection,
// then materializes the whole selected subtree in batched passes.
func rootResolve(typeInternal string, metas map[string]typeMeta) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		inter := application.FromContext(p.Context)
		if inter == nil {
			return nil, fmt.Errorf("no interactors on context")
		}
		connSels := selectionsFromInfo(p.Info)
		nodeSels := nodeSelections(connSels)
		if relationshipDepth(nodeSels, typeInternal, metas) > maxRelDepth {
			return nil, fmt.Errorf("query exceeds max relationship depth of %d", maxRelDepth)
		}
		filter, _ := p.Args["filter"].(string)
		first := clampFirst(p.Args["first"])
		after, _ := p.Args["after"].(string)
		wantTotal := selectionHasField(connSels, "totalCount")

		ids, cursorByID, info, err := topLevelPage(p.Context, inter, typeInternal, filter, first, after, wantTotal)
		if err != nil {
			return nil, err
		}
		nodes, err := buildNodes(p.Context, inter, typeInternal, ids, nodeSels, metas)
		if err != nil {
			return nil, err
		}
		edges := make([]map[string]any, 0, len(nodes))
		for _, n := range nodes {
			edges = append(edges, map[string]any{"node": n, "cursor": cursorByID[n["entityId"].(string)]})
		}
		return connection(edges, info.HasNextPage, after != "", info.TotalCount), nil
	}
}

// topLevelPage resolves one page of a top-level field via the FQL filter, or —
// when the filter is empty — every entity of the type (descendants included).
// The page is keyset-paginated; each entity's opaque cursor rides back in
// cursorByID, and the total is present in the PageInfo only when requested.
func topLevelPage(ctx context.Context, inter *application.Interactors, typeInternal, filter string, first int, after string, wantTotal bool) (ids []string, cursorByID map[string]string, info db.PageInfo, err error) {
	pa := db.PageArgs{Limit: &first, WantTotal: wantTotal}
	if after != "" {
		pa.Cursor = &after
	}
	cursorByID = map[string]string{}
	if strings.TrimSpace(filter) == "" {
		td, terr := inter.TypeDefinitions().GetByInternalName(ctx, typeInternal)
		if terr != nil {
			return nil, nil, db.PageInfo{}, terr
		}
		out, lerr := inter.Values().ListEntities(ctx, td.ID.String(), true, pa)
		if lerr != nil {
			return nil, nil, db.PageInfo{}, lerr
		}
		ids = make([]string, 0, len(out.Items))
		for _, e := range out.Items {
			ids = append(ids, e.EntityID)
			cursorByID[e.EntityID] = db.EncodeKeyset(e.LastUpdatedAt.UTC().Format(time.RFC3339Nano), e.EntityID)
		}
		return ids, cursorByID, out.PageInfo, nil
	}
	out, qerr := inter.Query().Execute(ctx, appquery.ExecuteInput{Type: typeInternal, Query: filter, Page: pa})
	if qerr != nil {
		return nil, nil, db.PageInfo{}, qerr
	}
	ids = make([]string, 0, len(out.Items))
	for _, r := range out.Items {
		ids = append(ids, r.EntityID)
		cursorByID[r.EntityID] = db.EncodeKeyset(r.LastUpdatedAt.UTC().Format(time.RFC3339Nano), r.EntityID)
	}
	return ids, cursorByID, out.PageInfo, nil
}

// buildNodes materializes a set of entities of one type into node maps,
// batch-loading their values and — for each selected relationship field —
// their related entities as a nested connection. Each level runs a bounded
// number of queries regardless of the entity count, so there is no N+1.
func buildNodes(ctx context.Context, inter *application.Interactors, typeInternal string, entityIDs []string, nodeSels []selection, metas map[string]typeMeta) ([]map[string]any, error) {
	tm, ok := metas[typeInternal]
	if !ok || len(entityIDs) == 0 {
		return []map[string]any{}, nil
	}

	vals, err := inter.Values().ListByEntities(ctx, entityIDs)
	if err != nil {
		return nil, err
	}
	byEntity := make(map[string]map[string][]valueobjects.Value, len(entityIDs))
	for _, v := range vals {
		eid := v.EntityID.String()
		m := byEntity[eid]
		if m == nil {
			m = map[string][]valueobjects.Value{}
			byEntity[eid] = m
		}
		aid := v.AttributeDefinitionID.String()
		m[aid] = append(m[aid], v.Value)
	}

	nodes := make([]map[string]any, 0, len(entityIDs))
	nodeByID := make(map[string]map[string]any, len(entityIDs))
	for _, eid := range entityIDs {
		node := map[string]any{"entityId": eid}
		vm := byEntity[eid]
		for field, am := range tm.attrByField {
			node[field] = projectValues(vm[am.attrID], am)
		}
		nodeByID[eid] = node
		nodes = append(nodes, node)
	}

	for _, sel := range nodeSels {
		rm, isRel := tm.relByField[sel.name]
		if !isRel {
			continue
		}
		if err := attachRelationship(ctx, inter, sel, rm, entityIDs, nodeByID, metas); err != nil {
			return nil, err
		}
	}
	return nodes, nil
}

// attachRelationship batch-loads one relationship for every parent entity and
// attaches a paged connection of related nodes to each parent node.
func attachRelationship(ctx context.Context, inter *application.Interactors, sel selection, rm relMeta, entityIDs []string, nodeByID map[string]map[string]any, metas map[string]typeMeta) error {
	links, err := inter.Relationships().LinksByEntities(ctx, entityIDs)
	if err != nil {
		return err
	}
	want := make(map[string]bool, len(entityIDs))
	for _, id := range entityIDs {
		want[id] = true
	}
	childrenByParent := map[string][]string{}
	var allChildren []string
	seen := map[string]bool{}
	for _, l := range links {
		if l.DefinitionID.String() != rm.defID {
			continue
		}
		pid := l.ParentEntityID.String()
		cid := l.ChildEntityID.String()
		var self, other string
		switch {
		case rm.symmetric:
			switch {
			case want[pid]:
				self, other = pid, cid
			case want[cid]:
				self, other = cid, pid
			default:
				continue
			}
		case rm.entityIsParent:
			if !want[pid] {
				continue
			}
			self, other = pid, cid
		default:
			if !want[cid] {
				continue
			}
			self, other = cid, pid
		}
		if len(childrenByParent[self]) >= maxRelChildren {
			continue
		}
		childrenByParent[self] = append(childrenByParent[self], other)
		if !seen[other] {
			seen[other] = true
			allChildren = append(allChildren, other)
		}
	}

	// Build every related node once (batched), then slice per parent.
	childNodes, err := buildNodes(ctx, inter, rm.otherType, allChildren, nodeSelections(sel.sub), metas)
	if err != nil {
		return err
	}
	childByID := make(map[string]map[string]any, len(childNodes))
	for _, cn := range childNodes {
		if id, ok := cn["entityId"].(string); ok {
			childByID[id] = cn
		}
	}

	// Nested connections page in memory over the batch-loaded children, keyset
	// on the child entity id so the shape matches the top-level connections.
	first := clampFirst(sel.args["first"])
	afterID := ""
	if raw := argString(sel.args["after"]); raw != "" {
		if vals, derr := db.DecodeKeyset(raw); derr == nil && len(vals) == 1 {
			afterID = vals[0]
		}
	}
	wantTotal := selectionHasField(sel.sub, "totalCount")
	for _, eid := range entityIDs {
		all := append([]string(nil), childrenByParent[eid]...)
		sort.Strings(all)
		start := 0
		for start < len(all) && all[start] <= afterID {
			if afterID == "" {
				break
			}
			start++
		}
		end := start + first
		if end > len(all) {
			end = len(all)
		}
		edges := make([]map[string]any, 0, end-start)
		for _, cid := range all[start:end] {
			if cn := childByID[cid]; cn != nil {
				edges = append(edges, map[string]any{"node": cn, "cursor": db.EncodeKeyset(cid)})
			}
		}
		var total *int
		if wantTotal {
			t := len(all)
			total = &t
		}
		nodeByID[eid][sel.name] = connection(edges, end < len(all), afterID != "", total)
	}
	return nil
}

// connection assembles a Relay connection from pre-built edges. total is nil
// unless the caller selected the totalCount field.
func connection(edges []map[string]any, hasNext, hasPrev bool, total *int) map[string]any {
	var startCursor, endCursor any
	if len(edges) > 0 {
		startCursor = edges[0]["cursor"]
		endCursor = edges[len(edges)-1]["cursor"]
	}
	conn := map[string]any{
		"edges": edges,
		"pageInfo": map[string]any{
			"hasNextPage":     hasNext,
			"hasPreviousPage": hasPrev,
			"startCursor":     startCursor,
			"endCursor":       endCursor,
		},
	}
	if total != nil {
		conn["totalCount"] = *total
	} else {
		conn["totalCount"] = nil
	}
	return conn
}

func projectValues(vals []valueobjects.Value, am attrMeta) any {
	if am.multi {
		out := make([]any, 0, len(vals))
		for _, v := range vals {
			out = append(out, projectScalar(v, am.dataType))
		}
		return out
	}
	if len(vals) == 0 {
		return nil
	}
	return projectScalar(vals[0], am.dataType)
}

func projectScalar(v valueobjects.Value, dt valueobjects.DataType) any {
	switch dt {
	case valueobjects.DataTypeBool:
		return v.Bool()
	case valueobjects.DataTypeInteger:
		return int(v.Int())
	case valueobjects.DataTypeFloat:
		return v.Float()
	default:
		return v.String()
	}
}

func gqlType(dt valueobjects.DataType, multi bool) graphql.Output {
	base := gqlScalar(dt)
	if multi {
		return graphql.NewList(base)
	}
	return base
}

func gqlScalar(dt valueobjects.DataType) *graphql.Scalar {
	switch dt {
	case valueobjects.DataTypeBool:
		return graphql.Boolean
	case valueobjects.DataTypeInteger:
		return graphql.Int
	case valueobjects.DataTypeFloat:
		return graphql.Float
	default:
		return graphql.String
	}
}

// ---- selection-set walking + helpers ----

type selection struct {
	name string
	args map[string]any
	sub  []selection
}

func selectionsFromInfo(info graphql.ResolveInfo) []selection {
	if len(info.FieldASTs) == 0 {
		return nil
	}
	return collectSelections(info.FieldASTs[0].SelectionSet, info.Fragments, info.VariableValues)
}

func collectSelections(set *ast.SelectionSet, fragments map[string]ast.Definition, vars map[string]any) []selection {
	if set == nil {
		return nil
	}
	var out []selection
	for _, sel := range set.Selections {
		switch s := sel.(type) {
		case *ast.Field:
			out = append(out, selection{
				name: s.Name.Value,
				args: argValues(s.Arguments, vars),
				sub:  collectSelections(s.SelectionSet, fragments, vars),
			})
		case *ast.InlineFragment:
			out = append(out, collectSelections(s.SelectionSet, fragments, vars)...)
		case *ast.FragmentSpread:
			if def, ok := fragments[s.Name.Value].(*ast.FragmentDefinition); ok {
				out = append(out, collectSelections(def.SelectionSet, fragments, vars)...)
			}
		}
	}
	return out
}

// nodeSelections digs the node-level field selections out of a connection's
// selection set: connection → edges → node → <fields>.
func nodeSelections(connSels []selection) []selection {
	for _, s := range connSels {
		if s.name != "edges" {
			continue
		}
		for _, e := range s.sub {
			if e.name == "node" {
				return e.sub
			}
		}
	}
	return nil
}

// relationshipDepth counts the deepest chain of nested relationship
// connections in a node's selection, independent of the edges/node wrappers.
func relationshipDepth(nodeSels []selection, typeInternal string, metas map[string]typeMeta) int {
	tm, ok := metas[typeInternal]
	if !ok {
		return 0
	}
	d := 0
	for _, s := range nodeSels {
		rm, isRel := tm.relByField[s.name]
		if !isRel {
			continue
		}
		child := 1 + relationshipDepth(nodeSelections(s.sub), rm.otherType, metas)
		if child > d {
			d = child
		}
	}
	return d
}

func argValues(args []*ast.Argument, vars map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	out := make(map[string]any, len(args))
	for _, a := range args {
		out[a.Name.Value] = argValue(a.Value, vars)
	}
	return out
}

func argValue(v ast.Value, vars map[string]any) any {
	switch n := v.(type) {
	case *ast.IntValue:
		if i, err := strconv.Atoi(n.Value); err == nil {
			return i
		}
	case *ast.StringValue:
		return n.Value
	case *ast.BooleanValue:
		return n.Value
	case *ast.Variable:
		return vars[n.Name.Value]
	}
	return nil
}

func argString(v any) string {
	s, _ := v.(string)
	return s
}

func clampFirst(v any) int {
	n, ok := v.(int)
	if !ok || n <= 0 {
		return defaultFirst
	}
	if n > maxFirst {
		return maxFirst
	}
	return n
}

// selectionHasField reports whether a field with the given name is directly
// selected — used to compute the total only when totalCount is requested.
func selectionHasField(sels []selection, name string) bool {
	for _, s := range sels {
		if s.name == name {
			return true
		}
	}
	return false
}

// typeName renders a type's GraphQL object name (PascalCase of the internal
// name) so it is distinct from the lowercase root query fields.
func typeName(internal string) string {
	parts := strings.Split(internal, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	if b.Len() == 0 {
		return "Type"
	}
	return b.String()
}

// sanitizeName coerces a label into a valid GraphQL field name.
func sanitizeName(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9' && i > 0:
			b.WriteRune(r)
		case r == ' ' || r == '-':
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return ""
	}
	if c := out[0]; c >= '0' && c <= '9' {
		out = "_" + out
	}
	return strings.ToLower(out)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func intPtr(n int) *int { return &n }
