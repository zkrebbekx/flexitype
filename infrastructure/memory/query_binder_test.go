package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// queryFixture is a product/part schema with a directed and a symmetric
// relationship, enough to exercise every binder branch.
type queryFixture struct {
	ctx  context.Context
	it   *application.Interactors
	root string
}

func newQueryFixture(searchIndex bool) *queryFixture {
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	var svc *flexitype.Service
	if searchIndex {
		svc = flexitype.NewInMemory(flexitype.WithSearchIndex())
	} else {
		svc = flexitype.NewInMemory()
	}
	it := svc.Interactors(ctx)

	product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "product", DisplayName: "Product",
	})
	So(err, ShouldBeNil)
	_, err = it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "bike", DisplayName: "Bike", ExtendsID: product.ID.String(),
	})
	So(err, ShouldBeNil)
	part, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "part", DisplayName: "Part",
	})
	So(err, ShouldBeNil)

	attr := func(typeID string, in appattribute.CreateInput) {
		in.TypeDefinitionID = typeID
		in.DisplayName = in.InternalName
		_, e := it.Attributes().Create(ctx, in)
		So(e, ShouldBeNil)
	}
	attr(product.ID.String(), appattribute.CreateInput{InternalName: "name", DataType: "string"})
	attr(product.ID.String(), appattribute.CreateInput{InternalName: "stock", DataType: "integer"})
	attr(product.ID.String(), appattribute.CreateInput{InternalName: "active", DataType: "bool"})
	attr(product.ID.String(), appattribute.CreateInput{InternalName: "weight", DataType: "quantity"})
	attr(part.ID.String(), appattribute.CreateInput{InternalName: "code", DataType: "string"})

	_, err = it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
		InternalName: "contains", DisplayName: "Contains",
		ParentTypeID: product.ID.String(), ChildTypeID: part.ID.String(),
	})
	So(err, ShouldBeNil)
	_, err = it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
		InternalName: "related_to", DisplayName: "Related To", Kind: "symmetric",
		ParentTypeID: product.ID.String(), ChildTypeID: product.ID.String(),
	})
	So(err, ShouldBeNil)

	return &queryFixture{ctx: ctx, it: it, root: "product"}
}

func (f *queryFixture) validate(q string) error {
	return f.it.Query().Validate(f.ctx, f.root, q)
}

func TestQueryBinderRejections(t *testing.T) {
	Convey("Given a product schema with typed attributes and relationships", t, func() {
		f := newQueryFixture(false)

		Convey("When a query names an attribute the type does not have", func() {
			err := f.validate(`colour = "red"`)

			Convey("Then binding fails with the attribute name and a position", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, `unknown attribute "colour"`)
				So(domainerrors.DetailsOf(err), ShouldContainKey, "position")
			})
		})

		Convey("When an ordering comparison is applied to an unordered attribute", func() {
			err := f.validate(`active > true`)

			Convey("Then it is refused naming the data type", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "does not support ordering comparisons")
			})
		})

		Convey("When length() is applied to a non-textual attribute", func() {
			err := f.validate(`length(stock) > 3`)

			Convey("Then it is refused naming the requirement", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "length() requires a textual attribute")
			})
		})

		Convey("When length() or count() is given a non-integer bound", func() {
			err := f.validate(`length(name) > "three"`)

			Convey("Then it is refused", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "expected a whole number")
			})
		})

		Convey("When min()/max() is applied to an unordered attribute", func() {
			err := f.validate(`max(active) = true`)

			Convey("Then it is refused naming the requirement", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "requires an ordered attribute")
			})
		})

		Convey("When a literal does not fit the attribute's data type", func() {
			err := f.validate(`stock = "many"`)

			Convey("Then it is refused naming the value and the type", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "is not a valid integer")
			})
		})

		Convey("When a string-matching operator is applied to a non-textual attribute", func() {
			err := f.validate(`contains(stock, "9")`)

			Convey("Then it is refused naming the data type", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "requires a textual attribute")
			})
		})

		Convey("When link.* is used outside a traversal", func() {
			err := f.validate(`link.qty = 1`)

			Convey("Then it is refused", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "only valid inside a relationship traversal")
			})
		})

		Convey("When a traversal names an unknown relationship", func() {
			err := f.validate(`child(supersedes){ code = "x" }`)

			Convey("Then it is refused naming the relationship", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "unknown relationship")
			})
		})

		Convey("When a symmetric relationship is traversed directionally", func() {
			err := f.validate(`child(related_to){ name = "x" }`)

			Convey("Then it points the caller at linked()", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "is symmetric")
				So(err.Error(), ShouldContainSubstring, "linked(")
			})
		})

		Convey("When link.* names an attribute the relationship does not declare", func() {
			err := f.validate(`child(contains){ link.qty = 1 }`)

			Convey("Then it is refused naming the link attribute", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "unknown link attribute")
			})
		})

		Convey("When matches() is used without the search index", func() {
			err := f.validate(`matches("bike")`)

			Convey("Then it is refused as a disabled feature", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "requires the search-index feature")
			})
		})

		Convey("When the query text does not parse", func() {
			err := f.validate(`name = `)

			Convey("Then the parse error carries a position", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(domainerrors.DetailsOf(err), ShouldContainKey, "position")
			})
		})

		Convey("When the root type does not exist", func() {
			err := f.it.Query().Validate(f.ctx, "widget", `name = "x"`)

			Convey("Then it is a not-found", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})
	})
}

func TestQueryBinderTypeField(t *testing.T) {
	Convey("Given a product type with a bike subtype", t, func() {
		f := newQueryFixture(false)

		Convey("When the type field is compared, listed and tested with isa", func() {
			Convey("Then =, !=, in and isa all bind", func() {
				So(f.validate(`type = product`), ShouldBeNil)
				So(f.validate(`type != bike`), ShouldBeNil)
				So(f.validate(`type in (product, bike)`), ShouldBeNil)
				So(f.validate(`type isa product`), ShouldBeNil)
			})
		})

		Convey("When the type field names a type that does not exist", func() {
			cmpErr := f.validate(`type = widget`)
			inErr := f.validate(`type in (product, widget)`)
			isaErr := f.validate(`type isa widget`)

			Convey("Then each is refused naming the unknown type", func() {
				for _, err := range []error{cmpErr, inErr, isaErr} {
					So(domainerrors.IsValidation(err), ShouldBeTrue)
					So(err.Error(), ShouldContainSubstring, `unknown type "widget"`)
				}
			})
		})

		Convey("When an unsupported operator is used on the type field", func() {
			err := f.validate(`type > product`)

			Convey("Then it names the operators the field supports", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "supports =, != and isa")
			})
		})

		Convey("When an aggregate function is applied to the type field", func() {
			err := f.validate(`count(type) > 1`)

			Convey("Then it is refused", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "do not apply to the type field")
			})
		})

		Convey("When isa is used on an ordinary attribute", func() {
			err := f.validate(`name isa product`)

			Convey("Then it is refused — isa is a type-field operator", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "isa applies to the type field only")
			})
		})
	})
}

func TestQueryBinderRangeAndIn(t *testing.T) {
	Convey("Given a product schema", t, func() {
		f := newQueryFixture(false)

		Convey("When range() bounds an ordered attribute", func() {
			err := f.validate(`range(stock, 1, 10)`)

			Convey("Then it binds", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When range() is applied to an unordered attribute", func() {
			err := f.validate(`range(active, 1, 10)`)

			Convey("Then it is refused naming the requirement", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "range() requires an ordered attribute")
			})
		})

		Convey("When a range bound does not fit the attribute's type", func() {
			loErr := f.validate(`range(stock, "low", 10)`)
			hiErr := f.validate(`range(stock, 1, "high")`)

			Convey("Then each bound is checked on its own", func() {
				So(domainerrors.IsValidation(loErr), ShouldBeTrue)
				So(domainerrors.IsValidation(hiErr), ShouldBeTrue)
			})
		})

		Convey("When range() names an unknown attribute", func() {
			err := f.validate(`range(colour, 1, 10)`)

			Convey("Then it is refused", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "unknown attribute")
			})
		})

		Convey("When an in-list member does not fit the attribute's type", func() {
			err := f.validate(`stock in (1, "two")`)

			Convey("Then it is refused", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "is not a valid integer")
			})
		})
	})
}

func TestQueryBinderQuantityUnits(t *testing.T) {
	Convey("Given a quantity attribute with no unit family", t, func() {
		f := newQueryFixture(false)

		Convey("When a quantity comparison omits its unit", func() {
			err := f.validate(`weight > 5`)

			Convey("Then it demands a unit and falls back to a generic hint", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "requires a unit")
				So(err.Error(), ShouldContainSubstring, "<unit>")
			})
		})

		Convey("When a quantity comparison carries a unit but the attribute has no family", func() {
			err := f.validate(`weight > 5 kg`)

			Convey("Then it is refused naming the attribute", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "has no unit family")
			})
		})

		Convey("When a quantity is compared against a non-numeric literal", func() {
			err := f.validate(`weight > "heavy"`)

			Convey("Then it is refused — a quantity needs a number and a unit", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "expects a number with a unit")
			})
		})

		Convey("When a unit suffix is used on a non-quantity attribute", func() {
			err := f.validate(`stock > 5 kg`)

			Convey("Then it is refused naming the attribute's actual type", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "only valid on quantity attributes")
			})
		})
	})
}

func TestQueryExecutePaging(t *testing.T) {
	Convey("Given products matching a query", t, func() {
		f := newQueryFixture(true)

		attrs, err := f.it.Attributes().List(f.ctx, appattribute.ListInput{InternalNames: []string{"name"}})
		So(err, ShouldBeNil)
		So(attrs.Items, ShouldHaveLength, 1)
		nameAttr := attrs.Items[0].ID.String()

		types, err := f.it.TypeDefinitions().GetByInternalName(f.ctx, "product")
		So(err, ShouldBeNil)

		for _, e := range []string{"p1", "p2", "p3"} {
			raw, err := json.Marshal("Trail Bike")
			So(err, ShouldBeNil)
			_, err = f.it.Values().Set(f.ctx, appvalue.SetInput{
				AttributeDefinitionID: nameAttr, EntityID: e,
				TypeDefinitionID: types.ID.String(), Value: raw,
			})
			So(err, ShouldBeNil)
		}

		Convey("When the query is run with a page smaller than the match set", func() {
			limit := 2
			first, err := f.it.Query().Execute(f.ctx, appquery.ExecuteInput{
				Type: "product", Query: `name = "Trail Bike"`,
				Page: db.PageArgs{Limit: &limit, WantTotal: true},
			})
			So(err, ShouldBeNil)

			Convey("Then it carries a cursor and a total, and the cursor resumes", func() {
				So(first.Items, ShouldHaveLength, 2)
				So(first.PageInfo.HasNextPage, ShouldBeTrue)
				So(*first.PageInfo.TotalCount, ShouldEqual, 3)

				second, err := f.it.Query().Execute(f.ctx, appquery.ExecuteInput{
					Type: "product", Query: `name = "Trail Bike"`,
					Page: db.PageArgs{Limit: &limit, Cursor: first.PageInfo.NextCursor},
				})
				So(err, ShouldBeNil)
				So(second.Items, ShouldHaveLength, 1)
				So(second.PageInfo.HasPreviousPage, ShouldBeTrue)
			})
		})

		Convey("When the page arguments are invalid", func() {
			zero := 0
			_, err := f.it.Query().Execute(f.ctx, appquery.ExecuteInput{
				Type: "product", Query: `name = "Trail Bike"`,
				Page: db.PageArgs{Limit: &zero},
			})

			Convey("Then the query is rejected before it runs", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When the query does not bind", func() {
			_, err := f.it.Query().Execute(f.ctx, appquery.ExecuteInput{
				Type: "product", Query: `colour = "red"`,
			})

			Convey("Then the binding error surfaces instead of results", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When matches() is used with the search index enabled", func() {
			out, err := f.it.Query().Execute(f.ctx, appquery.ExecuteInput{
				Type: "product", Query: `matches("Trail")`,
			})

			Convey("Then it binds and runs", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 3)
			})
		})
	})
}
