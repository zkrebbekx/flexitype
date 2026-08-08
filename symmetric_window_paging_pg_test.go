package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestSymmetricWindowPagingPostgres is the regression for #505.
//
// A window returns COUNTERPART IDS, not links, and one pair can produce
// several rows: a symmetric relationship holding both A->B and B->A yields
// other=B from each arm of the union. The cursor is the opposite id alone, so
// those rows broke paging in both directions at once — B appeared twice
// inside a page, and when the pair straddled a page boundary the
// `other > cursor` predicate skipped the second B entirely, so a counterpart
// was silently never listed. The total counted both rows, so it disagreed
// with what any number of pages could return.
func TestSymmetricWindowPagingPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a symmetric relationship with a redundant reverse link", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		person, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "person", DisplayName: "Person",
		})
		So(err, ShouldBeNil)
		name, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: person.ID.String(), InternalName: "name",
			DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)
		for _, e := range []string{"a", "b", "c", "d"} {
			raw, _ := json.Marshal(e)
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: name.ID.String(), EntityID: e,
				TypeDefinitionID: person.ID.String(), Value: raw,
			})
			So(serr, ShouldBeNil)
		}

		peer, err := svc.Interactors(ctx).Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "peer_of", DisplayName: "Peer of", Kind: "symmetric",
			ParentTypeID: person.ID.String(), ChildTypeID: person.ID.String(),
		})
		So(err, ShouldBeNil)
		link := func(p, c string) {
			_, lerr := svc.Interactors(ctx).Relationships().Link(ctx, apprelationship.LinkInput{
				DefinitionID: peer.ID.String(), ParentEntity: p, ChildEntity: c,
			})
			So(lerr, ShouldBeNil)
		}
		// a is peered with b, c and d.
		link("a", "b")
		link("a", "c")
		link("a", "d")
		// The b pair ALSO stored in reverse. Link() refuses this today — it
		// canonicalizes the pair — so the row is written directly, which is
		// exactly how it exists in a deployment: written before that guard,
		// or by a migration.
		_, err = pool.Exec(`INSERT INTO flexitype_relationship
			(id, tenant_id, relationship_definition_id, parent_entity_id, child_entity_id, created_at, updated_at)
			VALUES ($1, $2, $3, 'b', 'a', now(), now())`,
			ulid.New().String(), valueobjects.DefaultTenant.String(), peer.ID.String())
		So(err, ShouldBeNil)

		// The window is what the GraphQL resolver pages a nested connection
		// through, which is where the defect surfaces.
		page := func(limit int, after string) ([]string, bool, int) {
			pages, lerr := svc.Interactors(ctx).Relationships().WindowedLinks(ctx, apprelationship.LinkWindowInput{
				DefinitionID: peer.ID.String(),
				Side:         domainrelationship.EitherSide,
				First:        limit, After: after, WantTotal: true,
			}, []string{"a"})
			So(lerr, ShouldBeNil)
			p := pages["a"]
			total := 0
			if p.Total != nil {
				total = *p.Total
			}
			return p.Others, p.HasMore, total
		}

		Convey("When the counterparts are paged one at a time", func() {
			seen := []string{}
			cursor := ""
			var total int
			for {
				ids, hasMore, tot := page(1, cursor)
				seen = append(seen, ids...)
				total = tot
				if !hasMore || len(ids) == 0 {
					break
				}
				cursor = db.EncodeKeyset(ids[len(ids)-1])
			}

			Convey("Then every peer appears exactly once, and none is skipped", func() {
				So(seen, ShouldResemble, []string{"b", "c", "d"})
			})

			Convey("And the total matches what the pages return", func() {
				So(total, ShouldEqual, len(seen))
			})
		})

		Convey("When the counterparts are read in one page", func() {
			ids, _, total := page(50, "")

			Convey("Then the duplicated pair is listed once", func() {
				So(ids, ShouldResemble, []string{"b", "c", "d"})
				So(total, ShouldEqual, 3)
			})
		})
	})
}
