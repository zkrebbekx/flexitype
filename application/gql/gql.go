// Package gql serves a read-only GraphQL API whose schema mirrors a tenant's
// live type definitions: each entity type becomes an object, each attribute a
// field, and each relationship a nested Relay connection resolved through the
// batched repositories (no N+1). Top-level fields and relationship fields are
// Relay connections — edges/node/cursor plus pageInfo and totalCount — paged
// with first/after. The schema is built per tenant and per caller-permission
// set (an attribute the caller may not read is not a field, so introspection
// never leaks it) and is regenerated after any definition event. FQL is
// exposed as a filter argument on each top-level field.
package gql

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/gqlerrors"
	"github.com/graphql-go/graphql/language/ast"

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
	maxRelDepth    = 6   // nested relationship-connection hops
	defaultFirst   = 50  // connection page size when unspecified
	maxFirst       = 200 // hard cap on a connection page
	typeListLimit  = 500 // schema-build enumeration page size
	maxRelChildren = 500 // per-parent relationship fan-out cap
)

// Engine builds and caches per-tenant GraphQL schemas and executes queries.
// It is safe for concurrent use.
type Engine struct {
	mu          sync.Mutex
	generations map[string]uint64       // tenant -> schema generation
	cache       map[string]cachedSchema // tenant|access -> built schema
}

type cachedSchema struct {
	gen    uint64
	schema graphql.Schema
}

// NewEngine builds a GraphQL engine.
func NewEngine() *Engine {
	return &Engine{generations: map[string]uint64{}, cache: map[string]cachedSchema{}}
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

// Handle bumps the tenant's schema generation so the next query rebuilds.
func (e *Engine) Handle(_ context.Context, env events.Envelope) error {
	if env.TenantID == "" {
		return nil
	}
	e.mu.Lock()
	e.generations[env.TenantID]++
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
	schema, err := e.schemaFor(ctx, inter)
	if err != nil {
		return resultErr(err)
	}
	return graphql.Do(graphql.Params{
		Schema:         schema,
		RequestString:  query,
		VariableValues: variables,
		Context:        ctx,
	})
}

// schemaFor returns the tenant+access schema, rebuilding on a generation bump
// or a permission set not seen before.
func (e *Engine) schemaFor(ctx context.Context, inter *application.Interactors) (graphql.Schema, error) {
	tenant := uow.TenantFromContext(ctx).String()
	key := tenant + "|" + accessSignature(uow.AccessFromContext(ctx))

	e.mu.Lock()
	gen := e.generations[tenant]
	cached, ok := e.cache[key]
	e.mu.Unlock()
	if ok && cached.gen == gen {
		return cached.schema, nil
	}

	schema, err := buildSchema(ctx, inter)
	if err != nil {
		return graphql.Schema{}, err
	}
	e.mu.Lock()
	e.cache[key] = cachedSchema{gen: gen, schema: schema}
	e.mu.Unlock()
	return schema, nil
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
				"edges":      &graphql.Field{Type: graphql.NewList(edge)},
				"pageInfo":   &graphql.Field{Type: graphql.NewNonNull(pageInfo)},
				"totalCount": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
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

		ids, total, hasNext, offset, err := topLevelPage(p.Context, inter, typeInternal, filter, first, after)
		if err != nil {
			return nil, err
		}
		nodes, err := buildNodes(p.Context, inter, typeInternal, ids, nodeSels, metas)
		if err != nil {
			return nil, err
		}
		return connection(nodes, offset, total, hasNext), nil
	}
}

// topLevelPage resolves one page of entity ids for a top-level field: the FQL
// filter when given, otherwise every entity of the type (descendants
// included). It returns the ids, the total count, whether more remain, and the
// page's starting offset (for synthesizing edge cursors).
func topLevelPage(ctx context.Context, inter *application.Interactors, typeInternal, filter string, first int, after string) (ids []string, total int, hasNext bool, offset int, err error) {
	offset, err = cursorOffset(after)
	if err != nil {
		return nil, 0, false, 0, err
	}
	cursor := cursorPtr(after)
	if strings.TrimSpace(filter) == "" {
		td, terr := inter.TypeDefinitions().GetByInternalName(ctx, typeInternal)
		if terr != nil {
			return nil, 0, false, 0, terr
		}
		out, lerr := inter.Values().ListEntities(ctx, td.ID.String(), true, db.PageArgs{Limit: &first, Cursor: cursor})
		if lerr != nil {
			return nil, 0, false, 0, lerr
		}
		ids = make([]string, 0, len(out.Items))
		for _, e := range out.Items {
			ids = append(ids, e.EntityID)
		}
		return ids, out.PageInfo.TotalCount, out.PageInfo.HasNextPage, offset, nil
	}
	out, qerr := inter.Query().Execute(ctx, appquery.ExecuteInput{
		Type:  typeInternal,
		Query: filter,
		Page:  db.PageArgs{Limit: &first, Cursor: cursor},
	})
	if qerr != nil {
		return nil, 0, false, 0, qerr
	}
	ids = make([]string, 0, len(out.Items))
	for _, r := range out.Items {
		ids = append(ids, r.EntityID)
	}
	return ids, out.PageInfo.TotalCount, out.PageInfo.HasNextPage, offset, nil
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

	first := clampFirst(sel.args["first"])
	afterOffset, err := cursorOffset(argString(sel.args["after"]))
	if err != nil {
		return err
	}
	for _, eid := range entityIDs {
		all := childrenByParent[eid]
		total := len(all)
		lo := afterOffset
		if lo > total {
			lo = total
		}
		hi := lo + first
		if hi > total {
			hi = total
		}
		pageNodes := make([]map[string]any, 0, hi-lo)
		for _, cid := range all[lo:hi] {
			if cn := childByID[cid]; cn != nil {
				pageNodes = append(pageNodes, cn)
			}
		}
		nodeByID[eid][sel.name] = connection(pageNodes, lo, total, lo+len(pageNodes) < total)
	}
	return nil
}

// connection assembles a Relay connection map from a page of nodes, the page's
// starting offset in the full result, the total count and whether more remain.
func connection(nodes []map[string]any, offset, total int, hasNext bool) map[string]any {
	edges := make([]map[string]any, 0, len(nodes))
	for i, n := range nodes {
		edges = append(edges, map[string]any{
			"node":   n,
			"cursor": db.EncodeCursor(offset + i + 1),
		})
	}
	var startCursor, endCursor any
	if len(edges) > 0 {
		startCursor = edges[0]["cursor"]
		endCursor = edges[len(edges)-1]["cursor"]
	}
	return map[string]any{
		"edges":      edges,
		"totalCount": total,
		"pageInfo": map[string]any{
			"hasNextPage":     hasNext,
			"hasPreviousPage": offset > 0,
			"startCursor":     startCursor,
			"endCursor":       endCursor,
		},
	}
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

// cursorOffset decodes a Relay cursor to its offset; the empty cursor is the
// start of the result set.
func cursorOffset(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	return db.DecodeCursor(cursor)
}

func cursorPtr(cursor string) *string {
	if cursor == "" {
		return nil
	}
	return &cursor
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
