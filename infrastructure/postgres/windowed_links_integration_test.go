package postgres_test

import (
	"context"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// countingQuerier wraps a pool and records every SelectContext query so a test
// can prove a windowed page is fetched in ONE statement rather than one per
// parent (or a broad load-all-then-slice scan). All other QueryExecer methods
// are promoted from the embedded pool unchanged.
type countingQuerier struct {
	*sqlx.DB
	mu      sync.Mutex
	selects []string
}

func (c *countingQuerier) SelectContext(ctx context.Context, dest any, query string, args ...any) error {
	c.mu.Lock()
	c.selects = append(c.selects, query)
	c.mu.Unlock()
	return c.DB.SelectContext(ctx, dest, query, args...)
}

func (c *countingQuerier) reset() {
	c.mu.Lock()
	c.selects = nil
	c.mu.Unlock()
}

func (c *countingQuerier) queries() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.selects...)
}

// TestWindowedLinksIntegration proves issue #203's repository contract against
// PostgreSQL: nested relationship connections are windowed PER parent in one
// row_number() query, filtered by relationship definition in SQL, fetching only
// first+1 rows per parent — never a parent's whole fan-out, and never a broad
// definition-agnostic scan re-run per relationship field.
func TestWindowedLinksIntegration(t *testing.T) {
	pool := openIntegrationDB(t)
	defer func() { _ = pool.Close() }()
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	if err := postgres.Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given products with a five-supplier fan-out and a second relationship", t, func() {
		pool.MustExec(`TRUNCATE flexitype_relationship, flexitype_relationship_definition,
			flexitype_attribute_value, flexitype_attribute_definition, flexitype_type_definition,
			flexitype_entity_summary, flexitype_schema_version CASCADE`)

		svc := flexitype.New(pool)
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		supplier, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "supplier", DisplayName: "Supplier"})
		So(err, ShouldBeNil)

		mkAttr := func(typeID, name string) string {
			a, e := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: "string",
			})
			So(e, ShouldBeNil)
			return a.ID.String()
		}
		prodName := mkAttr(product.ID.String(), "name")
		supName := mkAttr(supplier.ID.String(), "name")
		set := func(typeID, entity, attrID, val string) {
			_, e := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: entity, TypeDefinitionID: typeID, Value: []byte(`"` + val + `"`),
			})
			So(e, ShouldBeNil)
		}
		for _, p := range []string{"p1", "p2", "p3"} {
			set(product.ID.String(), p, prodName, p)
		}
		for _, s := range []string{"s1", "s2", "s3", "s4", "s5"} {
			set(supplier.ID.String(), s, supName, s)
		}

		suppliedBy, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "supplied_by", DisplayName: "Supplied by",
			ParentTypeID: product.ID.String(), ChildTypeID: supplier.ID.String(),
			ChildLabel: "suppliers", ParentLabel: "supplies",
		})
		So(err, ShouldBeNil)
		auditedBy, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "audited_by", DisplayName: "Audited by",
			ParentTypeID: product.ID.String(), ChildTypeID: supplier.ID.String(),
			ChildLabel: "auditors", ParentLabel: "audits",
		})
		So(err, ShouldBeNil)
		// A symmetric self-relation, so a "self" entity can sit on EITHER side of
		// a link — the EitherSide window unions both arms.
		similar, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "similar", DisplayName: "Similar", Kind: "symmetric",
			ParentTypeID: product.ID.String(), ChildTypeID: product.ID.String(),
		})
		So(err, ShouldBeNil)

		link := func(defID, parent, child string) {
			_, e := it.Relationships().Link(ctx, apprelationship.LinkInput{
				DefinitionID: defID, ParentEntity: parent, ChildEntity: child,
			})
			So(e, ShouldBeNil)
		}
		for _, s := range []string{"s1", "s2", "s3", "s4", "s5"} {
			link(suppliedBy.ID.String(), "p1", s)
		}
		link(suppliedBy.ID.String(), "p2", "s2")
		link(suppliedBy.ID.String(), "p2", "s4")
		link(auditedBy.ID.String(), "p1", "s5") // a different relationship
		// Symmetric peers: p1—p2 (p1 parent) and p2—p3 (p2 parent), so p2 is a
		// peer via BOTH sides.
		link(similar.ID.String(), "p1", "p2")
		link(similar.ID.String(), "p2", "p3")

		defID, err := valueobjects.ParseRelationshipDefinitionID(suppliedBy.ID.String())
		So(err, ShouldBeNil)
		selves := []valueobjects.EntityID{"p1", "p2", "p3"}

		// A repository over the counting pool: WindowedLinks routes through its
		// request-scoped window loader, whose batch fetch hits SelectContext.
		counting := &countingQuerier{DB: pool}
		repo := postgres.NewRelationshipRepository(counting)

		Convey("When windowing first:2 suppliers for all three products at once", func() {
			counting.reset()
			pages, err := repo.WindowedLinks(ctx, domainrelationship.LinkWindow{
				TenantID: valueobjects.DefaultTenant, DefinitionID: defID, Side: domainrelationship.ParentSide,
				Page: db.Page{Limit: 2},
			}, selves)
			So(err, ShouldBeNil)

			Convey("Then one windowed, definition-filtered query returns each parent's first+1", func() {
				qs := counting.queries()
				So(len(qs), ShouldEqual, 1) // one query for all parents, not one per parent
				So(qs[0], ShouldContainSubstring, "row_number() OVER (PARTITION BY self")
				So(qs[0], ShouldContainSubstring, "relationship_definition_id =")

				So(others(pages["p1"]), ShouldResemble, []string{"s1", "s2"})
				So(pages["p1"].HasMore, ShouldBeTrue) // five suppliers, more remain
				So(others(pages["p2"]), ShouldResemble, []string{"s2", "s4"})
				So(pages["p2"].HasMore, ShouldBeFalse)
				So(len(pages["p3"].Others), ShouldEqual, 0) // no links
			})
		})

		Convey("When the caller also requests the total", func() {
			counting.reset()
			pages, err := repo.WindowedLinks(ctx, domainrelationship.LinkWindow{
				TenantID: valueobjects.DefaultTenant, DefinitionID: defID, Side: domainrelationship.ParentSide,
				Page: db.Page{Limit: 2, WantTotal: true},
			}, selves)
			So(err, ShouldBeNil)

			Convey("Then a second grouped count reports the FULL fan-out, not the page size", func() {
				So(len(counting.queries()), ShouldEqual, 2) // window + grouped count
				So(pages["p1"].Total, ShouldNotBeNil)
				So(*pages["p1"].Total, ShouldEqual, 5)
				So(*pages["p2"].Total, ShouldEqual, 2)
				So(*pages["p3"].Total, ShouldEqual, 0)
				So(others(pages["p1"]), ShouldResemble, []string{"s1", "s2"}) // window still bounded
			})
		})

		Convey("When windowing the OTHER relationship for the same parent", func() {
			auditID, err := valueobjects.ParseRelationshipDefinitionID(auditedBy.ID.String())
			So(err, ShouldBeNil)
			counting.reset()
			pages, err := repo.WindowedLinks(ctx, domainrelationship.LinkWindow{
				TenantID: valueobjects.DefaultTenant, DefinitionID: auditID, Side: domainrelationship.ParentSide,
				Page: db.Page{Limit: 2},
			}, []valueobjects.EntityID{"p1"})
			So(err, ShouldBeNil)

			Convey("Then the SQL filter yields only that definition's child, not the suppliers", func() {
				So(len(counting.queries()), ShouldEqual, 1)
				So(others(pages["p1"]), ShouldResemble, []string{"s5"})
			})
		})

		Convey("When windowing a SYMMETRIC relationship where self sits on either side", func() {
			simID, err := valueobjects.ParseRelationshipDefinitionID(similar.ID.String())
			So(err, ShouldBeNil)
			counting.reset()
			pages, err := repo.WindowedLinks(ctx, domainrelationship.LinkWindow{
				TenantID: valueobjects.DefaultTenant, DefinitionID: simID, Side: domainrelationship.EitherSide,
				Page: db.Page{Limit: 5, WantTotal: true},
			}, selves)
			So(err, ShouldBeNil)

			Convey("Then each self's peers come from both link directions", func() {
				So(len(counting.queries()), ShouldEqual, 2)                   // window + count, both unioning the two arms
				So(others(pages["p1"]), ShouldResemble, []string{"p2"})       // p1 is parent of p2
				So(others(pages["p2"]), ShouldResemble, []string{"p1", "p3"}) // p2 is child of p1 AND parent of p3
				So(*pages["p2"].Total, ShouldEqual, 2)
				So(others(pages["p3"]), ShouldResemble, []string{"p2"}) // p3 is child of p2
			})
		})

		Convey("When resuming from a nested cursor", func() {
			cursor := db.EncodeKeyset("s2") // the endCursor of p1's first window
			counting.reset()
			pages, err := repo.WindowedLinks(ctx, domainrelationship.LinkWindow{
				TenantID: valueobjects.DefaultTenant, DefinitionID: defID, Side: domainrelationship.ParentSide,
				Page: db.Page{Limit: 2, Cursor: cursor},
			}, []valueobjects.EntityID{"p1"})
			So(err, ShouldBeNil)

			Convey("Then the keyset predicate resumes strictly after the cursor", func() {
				So(len(counting.queries()), ShouldEqual, 1)
				So(others(pages["p1"]), ShouldResemble, []string{"s3", "s4"})
				So(pages["p1"].HasMore, ShouldBeTrue)
			})
		})
	})
}

// others renders a page's opposite endpoints as plain strings for comparison.
func others(p domainrelationship.LinkPage) []string {
	out := make([]string, 0, len(p.Others))
	for _, o := range p.Others {
		out = append(out, o.String())
	}
	return out
}
