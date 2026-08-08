package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestCounterpartTypeProbeIsDeterministic is the regression for #503.
//
// counterpartType resolved a traversed entity's type with LIMIT 1 and no
// ORDER BY. For an entity mid-reanchor — its live rows transiently carry
// mixed type_definition_id — the probe's answer depended on the plan, so
// the same `type` traversal condition could match or not match across
// executions. The probe now orders by (created_at, id), the same anchor
// rule EntityAnchor uses: the earliest-created row decides.
func TestCounterpartTypeProbeIsDeterministic(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

	Convey("Given a linked counterpart whose live rows carry mixed types", t, func() {
		truncateAll(t, pool)
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		part, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "part", DisplayName: "Part"})
		So(err, ShouldBeNil)
		mkAttr := func(typeID, name string) string {
			a, aerr := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: "string"})
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		nameID := mkAttr(product.ID.String(), "name")
		codeID := mkAttr(part.ID.String(), "code")
		contains, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "contains", DisplayName: "Contains",
			ParentTypeID: product.ID.String(), ChildTypeID: part.ID.String()})
		So(err, ShouldBeNil)

		set := func(attrID, entity, v string) {
			raw, _ := json.Marshal(v)
			_, serr := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: entity, Value: json.RawMessage(raw)})
			So(serr, ShouldBeNil)
		}
		set(nameID, "p1", "P1")
		set(codeID, "c1", "C1")
		_, err = it.Relationships().Link(ctx, apprelationship.LinkInput{
			DefinitionID: contains.ID.String(), ParentEntity: "p1", ChildEntity: "c1"})
		So(err, ShouldBeNil)

		// The mid-reanchor artifact: an EARLIER-created live row of c1 under
		// a different type. The probe must anchor on it, deterministically.
		_, err = pool.Exec(`INSERT INTO flexitype_attribute_value
			(id, tenant_id, type_definition_id, attribute_definition_id, entity_id,
			 data_type, value_text, definition_version, created_at, updated_at, locale, channel)
			VALUES ($1, $2, $3, $4, 'c1', 'string', 'ghost', 1, $5, $5, '', '')`,
			ulid.New().String(), valueobjects.DefaultTenant.String(), product.ID.String(), nameID,
			time.Now().UTC().Add(-time.Hour))
		So(err, ShouldBeNil)

		Convey("When the same type-conditioned traversal runs many times", func() {
			run := func() []string {
				out, qerr := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
					Type: "product", Query: `child(contains){ type != part }`, Page: db.PageArgs{}})
				So(qerr, ShouldBeNil)
				ids := make([]string, 0, len(out.Items))
				for _, r := range out.Items {
					ids = append(ids, r.EntityID)
				}
				return ids
			}
			first := run()

			Convey("Then every execution anchors on the earliest-created row", func() {
				So(first, ShouldResemble, []string{"p1"})
				for i := 0; i < 19; i++ {
					So(run(), ShouldResemble, first)
				}
			})
		})
	})
}
