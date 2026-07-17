package query

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// fakeLinkAttrs returns a fixed link-attribute set. linkAttrsFor calls only
// ListByTypeDefinition, so the rest of the repository interface is embedded
// (nil) and never invoked.
type fakeLinkAttrs struct {
	domainattribute.Repository
	defs []*domainattribute.Definition
}

func (f fakeLinkAttrs) ListByTypeDefinition(context.Context, valueobjects.TypeDefinitionID, db.Page) ([]*domainattribute.Definition, int, error) {
	return f.defs, len(f.defs), nil
}

func linkAttr(name string) *domainattribute.Definition {
	return domainattribute.Rehydrate(domainattribute.Snapshot{
		InternalName: name,
		DisplayName:  name,
		DataType:     valueobjects.DataTypeString,
	})
}

func TestLinkAttrsForAppliesFieldACL(t *testing.T) {
	Convey("Given a relationship whose link attributes include a restricted one", t, func() {
		// ExtendsID nil: linkAttrsFor returns after the first (only) page.
		def := domainrelationship.DefinitionSnapshot{InternalName: "employs"}
		b := &binder{
			attrs: fakeLinkAttrs{defs: []*domainattribute.Definition{linkAttr("title"), linkAttr("salary")}},
			// salary is denied; title is unlisted and therefore readable.
			access: uow.Access{Attr: map[string]uow.Perm{"salary": uow.PermNone}},
		}

		out, err := b.linkAttrsFor(context.Background(), def)
		So(err, ShouldBeNil)

		Convey("Then the readable link attribute is visible to the binder", func() {
			_, ok := out["title"]
			So(ok, ShouldBeTrue)
		})
		Convey("And the restricted link attribute is invisible — nothing to filter on, so no binary-search oracle", func() {
			_, ok := out["salary"]
			So(ok, ShouldBeFalse)
		})
	})

	Convey("Given an admin principal", t, func() {
		def := domainrelationship.DefinitionSnapshot{InternalName: "employs"}
		b := &binder{
			attrs:  fakeLinkAttrs{defs: []*domainattribute.Definition{linkAttr("salary")}},
			access: uow.Access{Admin: true},
		}
		out, err := b.linkAttrsFor(context.Background(), def)
		So(err, ShouldBeNil)
		Convey("Then every link attribute is visible", func() {
			_, ok := out["salary"]
			So(ok, ShouldBeTrue)
		})
	})
}
