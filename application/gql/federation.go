package gql

import (
	"fmt"
	"sort"
	"strings"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"

	"github.com/zkrebbekx/flexitype/application"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// Federation turns the endpoint into an Apollo-Federation-compatible subgraph.
//
// A gateway composes a subgraph from three things: a `_service { sdl }` field
// carrying the subgraph's schema as SDL, an `_entities` resolver that returns
// objects for representations another subgraph supplies, and a `@key` on every
// entity type. Without them composition fails outright, so an adopter already
// running a federated graph could not add this service at all — the natural
// modelling for an attribute service, where dynamic attributes appear as
// fields on a type another subgraph owns, was closed to them.
//
// Every entity type is keyed by `entityId`, which is the id the caller
// supplies and the same id every other surface uses. The gateway therefore
// resolves attributes for an entity it already knows by id, with no shared
// state beyond that id.
//
// The support is opt-in (`WithFederation`). A federated schema carries three
// fields no standalone client asks for, and `_entities` is a batch read that a
// non-federated deployment has no reason to expose.

// maxRepresentations bounds one `_entities` call.
//
// The batch is a single list argument, so it escapes the field-count cost
// guard entirely: one small document can ask for tens of thousands of
// entities. The cap is per call; a gateway that needs more pages the join
// itself, which is the shape it already uses against other subgraphs.
const maxRepresentations = 500

// federationSDL renders the subgraph SDL that `_service { sdl }` returns.
//
// The SDL is generated rather than introspected because the federation
// directives are not part of the executable schema: graphql-go would not
// report `@key` through introspection, and the gateway reads exactly this
// string. It carries the developer-facing schema — the entity types, their
// connections and the root fields — with `@key` on each entity type, and
// omits the federation additions (`_entities`, `_service`, `_Any`,
// `_Entity`), as the specification requires.
func federationSDL(metas map[string]typeMeta) string {
	names := make([]string, 0, len(metas))
	for name := range metas {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("type PageInfo {\n")
	b.WriteString("  hasNextPage: Boolean!\n")
	b.WriteString("  hasPreviousPage: Boolean!\n")
	b.WriteString("  startCursor: String\n")
	b.WriteString("  endCursor: String\n")
	b.WriteString("}\n")

	for _, name := range names {
		tm := metas[name]
		obj := typeName(name)
		fmt.Fprintf(&b, "\ntype %s @key(fields: \"entityId\") {\n", obj)
		b.WriteString("  entityId: String!\n")
		for _, field := range sortedKeys(tm.attrByField) {
			fmt.Fprintf(&b, "  %s: %s\n", field, sdlType(tm.attrByField[field]))
		}
		for _, field := range sortedKeys(tm.relByField) {
			rm := tm.relByField[field]
			if _, ok := metas[rm.otherType]; !ok {
				continue
			}
			fmt.Fprintf(&b, "  %s(first: Int, after: String): %sConnection\n",
				field, typeName(rm.otherType))
		}
		b.WriteString("}\n")
		fmt.Fprintf(&b, "\ntype %sEdge {\n  node: %s\n  cursor: String!\n}\n", obj, obj)
		fmt.Fprintf(&b, "\ntype %sConnection {\n  edges: [%sEdge]\n  pageInfo: PageInfo!\n  totalCount: Int\n}\n",
			obj, obj)
	}

	b.WriteString("\ntype Query {\n")
	b.WriteString("  _schemaTypes: [String!]\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  %s(first: Int, after: String, filter: String): %sConnection\n",
			name, typeName(name))
	}
	b.WriteString("}\n")
	return b.String()
}

// sdlType renders one attribute's SDL type. It mirrors gqlType, which builds
// the executable type, so the SDL a gateway composes against and the schema
// this service executes never disagree.
func sdlType(am attrMeta) string {
	base := "String"
	switch am.dataType {
	case valueobjects.DataTypeBool:
		base = "Boolean"
	case valueobjects.DataTypeInteger:
		base = "Int"
	case valueobjects.DataTypeFloat:
		base = "Float"
	}
	if am.multi {
		return "[" + base + "]"
	}
	return base
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// anyScalar is federation's `_Any`: an untyped map carrying at least
// `__typename` and the key fields.
var anyScalar = graphql.NewScalar(graphql.ScalarConfig{
	Name:         "_Any",
	Description:  "An entity representation: __typename plus the key fields.",
	Serialize:    func(v any) any { return v },
	ParseValue:   func(v any) any { return v },
	ParseLiteral: parseAnyLiteral,
})

// parseAnyLiteral converts an inline object literal into a map. A gateway
// normally sends representations as a variable, which never reaches this path,
// but a hand-written query may inline them.
func parseAnyLiteral(v ast.Value) any {
	switch val := v.(type) {
	case *ast.ObjectValue:
		out := map[string]any{}
		for _, f := range val.Fields {
			out[f.Name.Value] = parseAnyLiteral(f.Value)
		}
		return out
	case *ast.ListValue:
		out := make([]any, 0, len(val.Values))
		for _, item := range val.Values {
			out = append(out, parseAnyLiteral(item))
		}
		return out
	case *ast.StringValue:
		return val.Value
	case *ast.BooleanValue:
		return val.Value
	case *ast.IntValue:
		return val.Value
	case *ast.FloatValue:
		return val.Value
	case *ast.EnumValue:
		return val.Value
	default:
		return nil
	}
}

// serviceObject is federation's `_Service`, holding the SDL.
func serviceObject() *graphql.Object {
	return graphql.NewObject(graphql.ObjectConfig{
		Name: "_Service",
		Fields: graphql.Fields{
			"sdl": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		},
	})
}

// addFederationFields adds `_service` and `_entities` to the root, and returns
// the `_Entity` union over every entity type.
//
// The union's member list is the tenant's readable types, so a type the
// caller cannot read is not a member and cannot be asked for — the field ACL
// governs federation exactly as it governs every other read path.
func addFederationFields(
	rootFields graphql.Fields,
	metas map[string]typeMeta,
	objects map[string]*graphql.Object,
) {
	sdl := federationSDL(metas)

	rootFields["_service"] = &graphql.Field{
		Type:        graphql.NewNonNull(serviceObject()),
		Description: "Federation: this subgraph's SDL.",
		Resolve: func(graphql.ResolveParams) (any, error) {
			return map[string]any{"sdl": sdl}, nil
		},
	}

	// A tenant with no types has no union members. GraphQL forbids an empty
	// union, so `_entities` is omitted rather than built invalid: there is
	// nothing for a gateway to resolve, and `_service` still composes.
	if len(objects) == 0 {
		return
	}

	byName := make(map[string]string, len(objects)) // GraphQL name -> internal
	members := make([]*graphql.Object, 0, len(objects))
	for _, internal := range sortedKeys(objects) {
		members = append(members, objects[internal])
		byName[typeName(internal)] = internal
	}

	entity := graphql.NewUnion(graphql.UnionConfig{
		Name:        "_Entity",
		Types:       members,
		Description: "Federation: every entity type this subgraph resolves.",
		ResolveType: func(p graphql.ResolveTypeParams) *graphql.Object {
			node, ok := p.Value.(map[string]any)
			if !ok {
				return nil
			}
			name, _ := node["__typename"].(string)
			return objects[byName[name]]
		},
	})

	rootFields["_entities"] = &graphql.Field{
		Type:        graphql.NewNonNull(graphql.NewList(entity)),
		Description: "Federation: resolve entities from another subgraph's representations.",
		Args: graphql.FieldConfigArgument{
			"representations": &graphql.ArgumentConfig{
				Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(anyScalar))),
			},
		},
		Resolve: resolveEntities(metas, byName),
	}
}

// resolveEntities answers `_entities`.
//
// The representations arrive in one list, in the gateway's order, and the
// answer must come back in exactly that order — position is the only thing
// that ties a result to its representation. Resolution groups them by type so
// each type is read in one batch, then places each node back at its own index.
//
// An id this service holds no values for comes back as an object whose
// attribute fields are null. flexitype does not own entity existence — the
// entity id is supplied by the caller and owned by another system, which is
// the case federation exists for — so "no values here" is the honest answer,
// not "no such entity".
func resolveEntities(metas map[string]typeMeta, byName map[string]string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		inter := application.FromContext(p.Context)
		if inter == nil {
			return nil, fmt.Errorf("no interactors on context")
		}
		raw, _ := p.Args["representations"].([]any)
		// These are caller mistakes, so they are raised as domain validation
		// errors: the sanitizer masks anything else as "internal error", and a
		// gateway debugging its own representations would learn nothing.
		if len(raw) > maxRepresentations {
			return nil, domainerrors.NewValidation(fmt.Sprintf(
				"too many representations: %d exceeds the limit of %d",
				len(raw), maxRepresentations))
		}

		type slot struct {
			internal string
			entityID string
		}
		slots := make([]slot, len(raw))
		idsByType := map[string][]string{}
		for i, item := range raw {
			rep, ok := item.(map[string]any)
			if !ok {
				return nil, domainerrors.NewValidation(fmt.Sprintf(
					"representation %d is not an object", i))
			}
			name, _ := rep["__typename"].(string)
			internal, known := byName[name]
			if !known {
				return nil, domainerrors.NewValidation(fmt.Sprintf(
					"representation %d names unknown type %q", i, name))
			}
			id, _ := rep["entityId"].(string)
			if id == "" {
				return nil, domainerrors.NewValidation(fmt.Sprintf(
					"representation %d is missing entityId", i))
			}
			slots[i] = slot{internal: internal, entityID: id}
			idsByType[internal] = append(idsByType[internal], id)
		}

		sels := selectionsFromInfo(p.Info)
		out := make([]any, len(slots))
		for _, internal := range sortedKeys(idsByType) {
			if relationshipDepth(sels, internal, metas) > maxRelDepth {
				return nil, domainerrors.NewValidation(fmt.Sprintf(
					"query exceeds max relationship depth of %d", maxRelDepth))
			}
			nodes, err := buildNodes(p.Context, inter, internal, dedupe(idsByType[internal]), sels, metas)
			if err != nil {
				return nil, err
			}
			byID := make(map[string]map[string]any, len(nodes))
			for _, n := range nodes {
				n["__typename"] = typeName(internal)
				byID[n["entityId"].(string)] = n
			}
			for i, s := range slots {
				if s.internal != internal {
					continue
				}
				if node, ok := byID[s.entityID]; ok {
					out[i] = node
				}
			}
		}
		return out, nil
	}
}

// dedupe keeps the first occurrence of each id, in order.
//
// A gateway may name the same entity twice in one batch — two parents holding
// the same reference. Reading it once and placing it at both positions saves
// the second read and keeps the two answers identical.
func dedupe(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
