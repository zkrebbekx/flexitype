package typedef

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// failingRepo wraps a repository and fails the reads the hierarchy walkers
// depend on, so their error propagation can be observed. A walker that
// swallowed a read failure would silently return a TRUNCATED lineage, and an
// effective-attribute set built from it would be missing inherited attributes.
type failingRepo struct {
	domaintypedef.Repository
	getErr   error
	childErr error
}

func (r *failingRepo) Get(ctx context.Context, id valueobjects.TypeDefinitionID) (*domaintypedef.TypeDefinition, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.Repository.Get(ctx, id)
}

func (r *failingRepo) ListChildren(ctx context.Context, parentID valueobjects.TypeDefinitionID) ([]*domaintypedef.TypeDefinition, error) {
	if r.childErr != nil {
		return nil, r.childErr
	}
	return r.Repository.ListChildren(ctx, parentID)
}

// linkExtends rewrites a stored type's parent, bypassing the creation-time
// validation that normally makes a cycle impossible.
func linkExtends(repo *fakeRepo, child, parent valueobjects.TypeDefinitionID) {
	snap := repo.items[child.String()]
	pid := parent
	snap.ExtendsID = &pid
	repo.items[child.String()] = snap
}

func TestHierarchyWalkGuards(t *testing.T) {
	Convey("Given a hierarchy that bypassed creation-time validation", t, func() {
		ctx := context.Background()

		Convey("When the extends chain forms a cycle", func() {
			repo := newFakeRepo()
			a := mustType(t, repo, "type_a", nil)
			b := mustType(t, repo, "type_b", a)
			// Close the loop: a now extends b, which extends a.
			linkExtends(repo, a.ID(), b.ID())

			start, err := repo.Get(ctx, a.ID())
			So(err, ShouldBeNil)

			_, err = Ancestors(ctx, repo, start)

			Convey("Then the walk stops and reports a cycle instead of spinning", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "cycle")
			})
		})

		Convey("When a type extends itself", func() {
			repo := newFakeRepo()
			a := mustType(t, repo, "type_a", nil)
			linkExtends(repo, a.ID(), a.ID())

			start, err := repo.Get(ctx, a.ID())
			So(err, ShouldBeNil)

			_, err = Ancestors(ctx, repo, start)

			Convey("Then the self-reference is caught as a cycle", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When the chain is deeper than the supported limit", func() {
			repo := newFakeRepo()
			current := mustType(t, repo, "level_0", nil)
			for i := 1; i <= maxHierarchyDepth+2; i++ {
				current = mustType(t, repo, fmt.Sprintf("level_%d", i), current)
			}

			_, err := Ancestors(ctx, repo, current)

			Convey("Then the walk is bounded and reports the depth limit", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "depth")
			})
		})
	})
}

func TestHierarchyWalkPropagatesRepositoryFailures(t *testing.T) {
	Convey("Given a hierarchy whose repository is failing", t, func() {
		ctx := context.Background()
		repo := newFakeRepo()
		root := mustType(t, repo, "product", nil)
		leaf := mustType(t, repo, "bike", root)

		Convey("When loading a parent fails mid-walk", func() {
			failing := &failingRepo{Repository: repo, getErr: errors.New("database is down")}

			Convey("Then every walker surfaces the failure rather than a truncated lineage", func() {
				_, err := Ancestors(ctx, failing, leaf)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "database is down")

				_, err = Chain(ctx, failing, leaf)
				So(err, ShouldNotBeNil)

				_, err = Root(ctx, failing, leaf)
				So(err, ShouldNotBeNil)

				_, err = IsAncestorOrSelf(ctx, failing, leaf, root.ID())
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When listing subtypes fails", func() {
			failing := &failingRepo{Repository: repo, childErr: errors.New("database is down")}
			_, err := Descendants(ctx, failing, root)

			Convey("Then the failure propagates rather than yielding a partial tree", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "database is down")
			})
		})
	})
}

func TestIsAncestorOrSelfMatchesItself(t *testing.T) {
	Convey("Given a type definition", t, func() {
		ctx := context.Background()
		repo := newFakeRepo()
		root := mustType(t, repo, "product", nil)

		Convey("When it is compared against its own id", func() {
			ok, err := IsAncestorOrSelf(ctx, repo, root, root.ID())

			Convey("Then it matches without walking the hierarchy at all", func() {
				So(err, ShouldBeNil)
				So(ok, ShouldBeTrue)
			})
		})
	})
}

func TestDescendantsTerminatesOnCyclicHierarchy(t *testing.T) {
	Convey("Given a cyclic subtype graph that bypassed creation-time validation", t, func() {
		ctx := context.Background()
		repo := newFakeRepo()
		a := mustType(t, repo, "type_a", nil)
		b := mustType(t, repo, "type_b", a)
		// Close the loop: a extends b, so walking down from a reaches b, and
		// walking down from b reaches a again.
		linkExtends(repo, a.ID(), b.ID())

		start, err := repo.Get(ctx, a.ID())
		So(err, ShouldBeNil)

		Convey("When descendants are walked", func() {
			got, err := Descendants(ctx, repo, start)

			Convey("Then the walk terminates and visits each type at most once", func() {
				So(err, ShouldBeNil)

				seen := map[string]int{}
				for _, d := range got {
					seen[d.InternalName()]++
				}
				// b is a genuine descendant; a is the starting type and must not
				// be re-emitted when the cycle leads back to it.
				So(seen["type_b"], ShouldEqual, 1)
				So(seen["type_a"], ShouldEqual, 0)
				So(b.InternalName(), ShouldEqual, "type_b")
			})
		})
	})
}
