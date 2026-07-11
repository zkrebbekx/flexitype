package typedef

import (
	"context"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// maxHierarchyDepth bounds chain walks; deeper hierarchies are a modelling
// smell and a cycle that slipped past creation-time checks must not spin.
const maxHierarchyDepth = 10

// Ancestors walks the extends chain upward from t (exclusive), nearest
// parent first, root last.
func Ancestors(ctx context.Context, repo domaintypedef.Repository, t *domaintypedef.TypeDefinition) ([]*domaintypedef.TypeDefinition, error) {
	var out []*domaintypedef.TypeDefinition
	seen := map[string]bool{t.ID().String(): true}

	current := t
	for depth := 0; depth < maxHierarchyDepth; depth++ {
		parentID := current.ExtendsID()
		if parentID == nil {
			return out, nil
		}
		if seen[parentID.String()] {
			return nil, domainerrors.NewValidation("type hierarchy forms a cycle", "type", parentID.String())
		}
		seen[parentID.String()] = true

		parent, err := repo.Get(ctx, *parentID)
		if err != nil {
			return nil, err
		}
		out = append(out, parent)
		current = parent
	}
	return nil, domainerrors.NewValidation("type hierarchy exceeds the supported depth")
}

// Chain returns t followed by its ancestors: the full lineage a subtype's
// effective schema is assembled from.
func Chain(ctx context.Context, repo domaintypedef.Repository, t *domaintypedef.TypeDefinition) ([]*domaintypedef.TypeDefinition, error) {
	ancestors, err := Ancestors(ctx, repo, t)
	if err != nil {
		return nil, err
	}
	return append([]*domaintypedef.TypeDefinition{t}, ancestors...), nil
}

// Descendants walks the subtype tree downward from t (exclusive),
// breadth-first.
func Descendants(ctx context.Context, repo domaintypedef.Repository, t *domaintypedef.TypeDefinition) ([]*domaintypedef.TypeDefinition, error) {
	var out []*domaintypedef.TypeDefinition
	seen := map[string]bool{t.ID().String(): true}

	frontier := []valueobjects.TypeDefinitionID{t.ID()}
	for depth := 0; depth < maxHierarchyDepth && len(frontier) > 0; depth++ {
		var next []valueobjects.TypeDefinitionID
		for _, id := range frontier {
			children, err := repo.ListChildren(ctx, id)
			if err != nil {
				return nil, err
			}
			for _, child := range children {
				if seen[child.ID().String()] {
					continue
				}
				seen[child.ID().String()] = true
				out = append(out, child)
				next = append(next, child.ID())
			}
		}
		frontier = next
	}
	return out, nil
}

// Root returns the top of t's hierarchy (t itself when it extends nothing).
// Write paths lock the root so concurrent schema changes on different
// levels of one hierarchy serialize.
func Root(ctx context.Context, repo domaintypedef.Repository, t *domaintypedef.TypeDefinition) (*domaintypedef.TypeDefinition, error) {
	ancestors, err := Ancestors(ctx, repo, t)
	if err != nil {
		return nil, err
	}
	if len(ancestors) == 0 {
		return t, nil
	}
	return ancestors[len(ancestors)-1], nil
}

// IsAncestorOrSelf reports whether candidate is t or one of t's ancestors.
func IsAncestorOrSelf(ctx context.Context, repo domaintypedef.Repository, t *domaintypedef.TypeDefinition, candidate valueobjects.TypeDefinitionID) (bool, error) {
	if t.ID().Equals(candidate) {
		return true, nil
	}
	ancestors, err := Ancestors(ctx, repo, t)
	if err != nil {
		return false, err
	}
	for _, a := range ancestors {
		if a.ID().Equals(candidate) {
			return true, nil
		}
	}
	return false, nil
}
