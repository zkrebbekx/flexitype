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
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// TestMatchesFailsClosedForFieldRestrictedPrincipals is the regression for
// the matches() field-ACL bypass (#473).
//
// The entity search document concatenates every textual value with no
// per-attribute identity, so the binder cannot filter a match by the field
// ACL the way it filters named attributes: contains(internal_notes, ...)
// resolves the name and is refused, but matches("recall") searched the same
// restricted text and returned the entity — a word-by-word disclosure
// oracle. Until the index carries per-attribute documents, matches() fails
// closed for any principal whose policy hides at least one attribute.
func TestMatchesFailsClosedForFieldRestrictedPrincipals(t *testing.T) {
	Convey("Given an entity whose restricted notes hold confidential text", t, func() {
		admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory(flexitype.WithSearchIndex())
		ia := svc.Interactors(admin)

		product, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		attrID := map[string]string{}
		for _, name := range []string{"name", "internal_notes"} {
			a, aerr := ia.Attributes().Create(admin, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: name,
				DisplayName: name, DataType: "string",
			})
			So(aerr, ShouldBeNil)
			attrID[name] = a.ID.String()
		}
		set := func(name, v string) {
			raw, _ := json.Marshal(v)
			_, serr := ia.Values().Set(admin, appvalue.SetInput{
				AttributeDefinitionID: attrID[name], EntityID: "e1",
				TypeDefinitionID: product.ID.String(), Value: raw,
			})
			So(serr, ShouldBeNil)
		}
		set("name", "widget")
		set("internal_notes", "confidential recall pending")

		run := func(ctx context.Context, q string) ([]string, error) {
			out, qerr := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
				Type: "product", Query: q, Page: db.PageArgs{},
			})
			if qerr != nil {
				return nil, qerr
			}
			ids := make([]string, 0, len(out.Items))
			for _, r := range out.Items {
				ids = append(ids, r.EntityID)
			}
			return ids, nil
		}

		Convey("When a principal denied the notes searches for a restricted word", func() {
			restricted := uow.WithAccess(admin, uow.Access{Attr: map[string]uow.Perm{
				"internal_notes": uow.PermNone,
			}})
			_, err := run(restricted, `matches("recall")`)

			Convey("Then matches() is refused as validation and names no attribute", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "field-restricted")
				So(err.Error(), ShouldNotContainSubstring, "internal_notes")
			})
		})

		Convey("When an allow-list principal with nothing granted searches", func() {
			denyAll := uow.WithAccess(admin, uow.DenyAll())
			_, err := run(denyAll, `matches("widget")`)

			Convey("Then matches() is refused the same way", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("When a principal is read-limited but nothing is hidden", func() {
			readOnly := uow.WithAccess(admin, uow.Access{Attr: map[string]uow.Perm{
				"internal_notes": uow.PermRead,
			}})
			ids, err := run(readOnly, `matches("recall")`)

			Convey("Then matches() still works: read-only hides no value", func() {
				So(err, ShouldBeNil)
				So(ids, ShouldResemble, []string{"e1"})
			})
		})

		Convey("When an admin searches", func() {
			ids, err := run(admin, `matches("recall")`)

			Convey("Then matches() is unchanged", func() {
				So(err, ShouldBeNil)
				So(ids, ShouldResemble, []string{"e1"})
			})
		})
	})
}
