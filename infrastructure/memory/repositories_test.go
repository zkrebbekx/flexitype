package memory_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/activity"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// The repository ports are exercised here directly (rather than through the
// interactors) so a filter field, a tenant boundary and a keyset cursor can each
// be driven in isolation and asserted on the exact rows they must return.

// ulidAt builds a deterministic 26-character ULID whose last character orders
// the id lexicographically — the ORDER BY key of every id-ordered list.
func ulidAt(last byte) string { return "01ARZ3NDEKTSV4RRFFQ69G5FA" + string(last) }

const (
	tenantA = valueobjects.TenantID("default")
	tenantB = valueobjects.TenantID("other")
)

// plainTx is a db.Tx that is NOT a memory transaction and NOT a db.Transactor:
// the shape a repository sees when a foreign backend's handle reaches it. The
// memory repositories must treat it as "no transaction" rather than mis-cast.
type plainTx struct{ db.TxMarker }

var fixedTime = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func archivedAt(offset time.Duration) *time.Time {
	t := fixedTime.Add(offset)
	return &t
}

// typeSnap builds a persisted type-definition snapshot.
func typeSnap(id string, tenant valueobjects.TenantID, name string, kind domaintypedef.Kind, archived *time.Time) domaintypedef.Snapshot {
	return domaintypedef.Snapshot{
		ID:           valueobjects.MustParseTypeDefinitionID(id),
		TenantID:     tenant,
		Kind:         kind,
		InternalName: name,
		DisplayName:  name,
		Version:      1,
		CreatedAt:    fixedTime,
		UpdatedAt:    fixedTime,
		ArchivedAt:   archived,
	}
}

// attrSnap builds a persisted attribute-definition snapshot.
func attrSnap(id string, tenant valueobjects.TenantID, typeID, name string, dt valueobjects.DataType, archived *time.Time) domainattribute.Snapshot {
	return domainattribute.Snapshot{
		ID:               valueobjects.MustParseAttributeDefinitionID(id),
		TenantID:         tenant,
		TypeDefinitionID: valueobjects.MustParseTypeDefinitionID(typeID),
		InternalName:     name,
		DisplayName:      name,
		DataType:         dt,
		Version:          1,
		CreatedAt:        fixedTime,
		UpdatedAt:        fixedTime,
		ArchivedAt:       archived,
	}
}

// valueSnap builds a persisted attribute-value snapshot.
func valueSnap(id string, tenant valueobjects.TenantID, typeID, attrID, entity string, v valueobjects.Value, updated time.Time, archived *time.Time) domainvalue.Snapshot {
	return domainvalue.Snapshot{
		ID:                    valueobjects.MustParseAttributeValueID(id),
		TenantID:              tenant,
		TypeDefinitionID:      valueobjects.MustParseTypeDefinitionID(typeID),
		AttributeDefinitionID: valueobjects.MustParseAttributeDefinitionID(attrID),
		EntityID:              valueobjects.EntityID(entity),
		Value:                 v,
		DefinitionVersion:     1,
		CreatedAt:             fixedTime,
		UpdatedAt:             updated,
		ArchivedAt:            archived,
	}
}

// depSnap builds a persisted dependency snapshot.
func depSnap(id string, tenant valueobjects.TenantID, source, target string, archived *time.Time) domaindependency.Snapshot {
	return domaindependency.Snapshot{
		ID:                valueobjects.MustParseDependencyID(id),
		TenantID:          tenant,
		SourceAttributeID: valueobjects.MustParseAttributeDefinitionID(source),
		TargetAttributeID: valueobjects.MustParseAttributeDefinitionID(target),
		Version:           1,
		CreatedAt:         fixedTime,
		UpdatedAt:         fixedTime,
		ArchivedAt:        archived,
	}
}

// relSnap builds a persisted relationship (link) snapshot.
func relSnap(id string, tenant valueobjects.TenantID, defID, parent, child string, archived *time.Time) domainrelationship.Snapshot {
	return domainrelationship.Snapshot{
		ID:             valueobjects.MustParseRelationshipID(id),
		TenantID:       tenant,
		DefinitionID:   valueobjects.MustParseRelationshipDefinitionID(defID),
		ParentEntityID: valueobjects.EntityID(parent),
		ChildEntityID:  valueobjects.EntityID(child),
		CreatedAt:      fixedTime,
		UpdatedAt:      fixedTime,
		ArchivedAt:     archived,
	}
}

// seed persists a set of snapshots through the repositories under test.
func seedTypes(ctx context.Context, repos application.Repositories, snaps ...domaintypedef.Snapshot) {
	for _, s := range snaps {
		So(repos.TypeDefinitions.Save(ctx, domaintypedef.Rehydrate(s)), ShouldBeNil)
	}
}

func seedAttrs(ctx context.Context, repos application.Repositories, snaps ...domainattribute.Snapshot) {
	for _, s := range snaps {
		So(repos.Attributes.Save(ctx, domainattribute.Rehydrate(s)), ShouldBeNil)
	}
}

func seedValues(ctx context.Context, repos application.Repositories, snaps ...domainvalue.Snapshot) {
	for _, s := range snaps {
		So(repos.Values.Save(ctx, domainvalue.Rehydrate(s)), ShouldBeNil)
	}
}

func seedDeps(ctx context.Context, repos application.Repositories, snaps ...domaindependency.Snapshot) {
	for _, s := range snaps {
		So(repos.Dependencies.Save(ctx, domaindependency.Rehydrate(s)), ShouldBeNil)
	}
}

func seedRels(ctx context.Context, repos application.Repositories, snaps ...domainrelationship.Snapshot) {
	for _, s := range snaps {
		So(repos.Relationships.Save(ctx, domainrelationship.Rehydrate(s)), ShouldBeNil)
	}
}

func typeNames(items []*domaintypedef.TypeDefinition) []string {
	out := make([]string, 0, len(items))
	for _, t := range items {
		out = append(out, t.InternalName())
	}
	return out
}

func attrNames(items []*domainattribute.Definition) []string {
	out := make([]string, 0, len(items))
	for _, a := range items {
		out = append(out, a.InternalName())
	}
	return out
}

func valueIDStrings(items []*domainvalue.AttributeValue) []string {
	out := make([]string, 0, len(items))
	for _, v := range items {
		out = append(out, v.ID().String())
	}
	return out
}

func depIDStrings(items []*domaindependency.Dependency) []string {
	out := make([]string, 0, len(items))
	for _, d := range items {
		out = append(out, d.ID().String())
	}
	return out
}

func relIDStrings(items []*domainrelationship.Relationship) []string {
	out := make([]string, 0, len(items))
	for _, r := range items {
		out = append(out, r.ID().String())
	}
	return out
}

func TestTypeDefinitionRepositoryList(t *testing.T) {
	Convey("Given a store holding live, archived, foreign-tenant and attribute-set types", t, func() {
		ctx := context.Background()
		store := memory.NewStore()
		repos := store.Repositories()

		seedTypes(ctx, repos,
			typeSnap(ulidAt('1'), tenantA, "product", domaintypedef.KindEntity, nil),
			typeSnap(ulidAt('2'), tenantA, "supplier", domaintypedef.KindEntity, nil),
			typeSnap(ulidAt('3'), tenantA, "retired", domaintypedef.KindEntity, archivedAt(time.Hour)),
			typeSnap(ulidAt('4'), tenantA, "supplied_by_attrs", domaintypedef.KindRelationshipAttributes, nil),
			typeSnap(ulidAt('5'), tenantB, "product", domaintypedef.KindEntity, nil),
		)

		Convey("When listing with only a tenant filter", func() {
			got, total, err := repos.TypeDefinitions.List(ctx,
				domaintypedef.Filter{TenantID: tenantA}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then archived, attribute-set and foreign-tenant types are all excluded", func() {
				So(typeNames(got), ShouldResemble, []string{"product", "supplier"})
				So(total, ShouldEqual, 2)
			})
		})

		Convey("When archived types are requested", func() {
			got, total, err := repos.TypeDefinitions.List(ctx,
				domaintypedef.Filter{TenantID: tenantA, IncludeArchived: true}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then the archived type joins the page, still id-ordered", func() {
				So(typeNames(got), ShouldResemble, []string{"product", "supplier", "retired"})
				So(total, ShouldEqual, 3)
			})
		})

		Convey("When the hidden attribute-set types are requested", func() {
			got, _, err := repos.TypeDefinitions.List(ctx,
				domaintypedef.Filter{TenantID: tenantA, IncludeAttributeSets: true}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then the relationship attribute-set type is included", func() {
				So(typeNames(got), ShouldContain, "supplied_by_attrs")
			})
		})

		Convey("When an internal-name filter is applied", func() {
			got, total, err := repos.TypeDefinitions.List(ctx,
				domaintypedef.Filter{TenantID: tenantA, InternalNames: []string{"supplier", "absent"}},
				db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then only the named type matches and the total counts the filtered set", func() {
				So(typeNames(got), ShouldResemble, []string{"supplier"})
				So(total, ShouldEqual, 1)
			})
		})

		Convey("When the other tenant lists its own types", func() {
			got, _, err := repos.TypeDefinitions.List(ctx,
				domaintypedef.Filter{TenantID: tenantB}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then it sees only its own row, never tenant A's same-named type", func() {
				So(got, ShouldHaveLength, 1)
				So(got[0].ID().String(), ShouldEqual, ulidAt('5'))
			})
		})

		Convey("When paging with a keyset cursor at the first id", func() {
			page, total, err := repos.TypeDefinitions.List(ctx,
				domaintypedef.Filter{TenantID: tenantA},
				db.Page{Limit: 10, Cursor: db.EncodeKeyset(ulidAt('1'))})
			So(err, ShouldBeNil)

			Convey("Then only ids strictly after the cursor are returned, total unaffected", func() {
				So(typeNames(page), ShouldResemble, []string{"supplier"})
				So(total, ShouldEqual, 2) // the total is the full filtered set, not the page
			})
		})

		Convey("When the cursor points past the last id", func() {
			page, total, err := repos.TypeDefinitions.List(ctx,
				domaintypedef.Filter{TenantID: tenantA},
				db.Page{Limit: 10, Cursor: db.EncodeKeyset(ulidAt('9'))})
			So(err, ShouldBeNil)

			Convey("Then the page is empty but the total still reports the population", func() {
				So(page, ShouldBeEmpty)
				So(total, ShouldEqual, 2)
			})
		})

		Convey("When the cursor is malformed", func() {
			page, _, err := repos.TypeDefinitions.List(ctx,
				domaintypedef.Filter{TenantID: tenantA},
				db.Page{Limit: 10, Cursor: "not-base64!!"})
			So(err, ShouldBeNil)

			Convey("Then it pages from the start rather than failing", func() {
				So(typeNames(page), ShouldResemble, []string{"product", "supplier"})
			})
		})

		Convey("When a limit smaller than the population is given", func() {
			page, total, err := repos.TypeDefinitions.List(ctx,
				domaintypedef.Filter{TenantID: tenantA}, db.Page{Limit: 1})
			So(err, ShouldBeNil)

			Convey("Then the page over-fetches by one so the caller can detect a next page", func() {
				So(typeNames(page), ShouldResemble, []string{"product", "supplier"})
				So(total, ShouldEqual, 2)
			})
		})
	})
}

func TestAttributeRepositoryList(t *testing.T) {
	Convey("Given attributes across two types, two tenants and mixed archive state", t, func() {
		ctx := context.Background()
		store := memory.NewStore()
		repos := store.Repositories()

		productType, supplierType, foreignType := ulidAt('1'), ulidAt('2'), ulidAt('3')
		seedAttrs(ctx, repos,
			attrSnap(ulidAt('A'), tenantA, productType, "name", valueobjects.DataTypeString, nil),
			attrSnap(ulidAt('B'), tenantA, productType, "price", valueobjects.DataTypeInteger, nil),
			attrSnap(ulidAt('C'), tenantA, productType, "legacy", valueobjects.DataTypeString, archivedAt(time.Hour)),
			attrSnap(ulidAt('D'), tenantA, supplierType, "region", valueobjects.DataTypeString, nil),
			attrSnap(ulidAt('E'), tenantB, foreignType, "name", valueobjects.DataTypeString, nil),
		)

		Convey("When listing every live attribute of the tenant", func() {
			got, total, err := repos.Attributes.List(ctx,
				domainattribute.Filter{TenantID: tenantA}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then archived and foreign-tenant attributes are excluded, id-ordered", func() {
				So(attrNames(got), ShouldResemble, []string{"name", "price", "region"})
				So(total, ShouldEqual, 3)
			})
		})

		Convey("When archived attributes are included", func() {
			got, _, err := repos.Attributes.List(ctx,
				domainattribute.Filter{TenantID: tenantA, IncludeArchived: true}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then the archived attribute reappears", func() {
				So(attrNames(got), ShouldResemble, []string{"name", "price", "legacy", "region"})
			})
		})

		Convey("When narrowing to one type definition", func() {
			got, total, err := repos.Attributes.List(ctx, domainattribute.Filter{
				TenantID:         tenantA,
				TypeDefinitionID: valueobjects.MustParseTypeDefinitionID(supplierType),
			}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then only that type's attributes are returned", func() {
				So(attrNames(got), ShouldResemble, []string{"region"})
				So(total, ShouldEqual, 1)
			})
		})

		Convey("When filtering by internal name", func() {
			got, _, err := repos.Attributes.List(ctx,
				domainattribute.Filter{TenantID: tenantA, InternalNames: []string{"price"}},
				db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then only the named attribute matches", func() {
				So(attrNames(got), ShouldResemble, []string{"price"})
			})
		})

		Convey("When filtering by data type", func() {
			matched, _, err := repos.Attributes.List(ctx, domainattribute.Filter{
				TenantID: tenantA, DataTypes: []valueobjects.DataType{valueobjects.DataTypeInteger},
			}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			unmatched, _, err := repos.Attributes.List(ctx, domainattribute.Filter{
				TenantID: tenantA, DataTypes: []valueobjects.DataType{valueobjects.DataTypeBool},
			}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then only attributes of a listed data type survive the filter", func() {
				So(attrNames(matched), ShouldResemble, []string{"price"})
				So(unmatched, ShouldBeEmpty)
			})
		})

		Convey("When paging live attributes from a cursor", func() {
			got, total, err := repos.Attributes.List(ctx,
				domainattribute.Filter{TenantID: tenantA},
				db.Page{Limit: 10, Cursor: db.EncodeKeyset(ulidAt('A'))})
			So(err, ShouldBeNil)

			Convey("Then the cursor row is excluded and the rest follow in id order", func() {
				So(attrNames(got), ShouldResemble, []string{"price", "region"})
				So(total, ShouldEqual, 3)
			})
		})

		Convey("When listing one type's attributes with a cursor", func() {
			got, total, err := repos.Attributes.ListByTypeDefinition(ctx,
				valueobjects.MustParseTypeDefinitionID(productType),
				db.Page{Limit: 10, Cursor: db.EncodeKeyset(ulidAt('A'))})
			So(err, ShouldBeNil)

			Convey("Then archived rows stay excluded and paging resumes after the cursor", func() {
				So(attrNames(got), ShouldResemble, []string{"price"})
				So(total, ShouldEqual, 2) // name + price; legacy is archived
			})
		})
	})
}

func TestValueRepositoryList(t *testing.T) {
	Convey("Given attribute values spanning tenants, types, attributes and entities", t, func() {
		ctx := context.Background()
		store := memory.NewStore()
		repos := store.Repositories()

		productType, supplierType := ulidAt('1'), ulidAt('2')
		nameAttr, priceAttr := ulidAt('A'), ulidAt('B')

		seedValues(ctx, repos,
			valueSnap(ulidAt('1'), tenantA, productType, nameAttr, "sku-1",
				valueobjects.NewStringValue("Trail"), fixedTime, nil),
			valueSnap(ulidAt('2'), tenantA, productType, priceAttr, "sku-1",
				valueobjects.NewIntegerValue(1499), fixedTime, nil),
			valueSnap(ulidAt('3'), tenantA, productType, nameAttr, "sku-2",
				valueobjects.NewStringValue("City"), fixedTime, nil),
			valueSnap(ulidAt('4'), tenantA, productType, nameAttr, "sku-3",
				valueobjects.NewStringValue("Removed"), fixedTime, archivedAt(time.Hour)),
			valueSnap(ulidAt('5'), tenantA, supplierType, nameAttr, "acme",
				valueobjects.NewStringValue("Acme"), fixedTime, nil),
			valueSnap(ulidAt('6'), tenantB, productType, nameAttr, "sku-1",
				valueobjects.NewStringValue("Other tenant"), fixedTime, nil),
		)

		Convey("When listing every live value of the tenant", func() {
			got, total, err := repos.ValueReader.List(ctx,
				domainvalue.Filter{TenantID: tenantA}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then the archived row and the other tenant's row are excluded", func() {
				So(valueIDStrings(got), ShouldResemble, []string{
					ulidAt('1'), ulidAt('2'), ulidAt('3'), ulidAt('5'),
				})
				So(total, ShouldEqual, 4)
			})
		})

		Convey("When archived values are included", func() {
			got, total, err := repos.ValueReader.List(ctx,
				domainvalue.Filter{TenantID: tenantA, IncludeArchived: true}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then the archived row is returned in its id position", func() {
				So(valueIDStrings(got), ShouldContain, ulidAt('4'))
				So(total, ShouldEqual, 5)
			})
		})

		Convey("When narrowing to one type definition", func() {
			got, _, err := repos.ValueReader.List(ctx, domainvalue.Filter{
				TenantID:         tenantA,
				TypeDefinitionID: valueobjects.MustParseTypeDefinitionID(supplierType),
			}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then only that type's rows are returned", func() {
				So(valueIDStrings(got), ShouldResemble, []string{ulidAt('5')})
			})
		})

		Convey("When narrowing to one attribute definition", func() {
			got, _, err := repos.ValueReader.List(ctx, domainvalue.Filter{
				TenantID:              tenantA,
				AttributeDefinitionID: valueobjects.MustParseAttributeDefinitionID(priceAttr),
			}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then only that attribute's rows are returned", func() {
				So(valueIDStrings(got), ShouldResemble, []string{ulidAt('2')})
			})
		})

		Convey("When narrowing to one entity", func() {
			got, total, err := repos.ValueReader.List(ctx, domainvalue.Filter{
				TenantID: tenantA, EntityID: valueobjects.EntityID("sku-1"),
			}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then only that entity's rows are returned — not the same entity id in tenant B", func() {
				So(valueIDStrings(got), ShouldResemble, []string{ulidAt('1'), ulidAt('2')})
				So(total, ShouldEqual, 2)
			})
		})

		Convey("When paging the tenant's values from a cursor", func() {
			got, total, err := repos.ValueReader.List(ctx,
				domainvalue.Filter{TenantID: tenantA},
				db.Page{Limit: 10, Cursor: db.EncodeKeyset(ulidAt('2'))})
			So(err, ShouldBeNil)

			Convey("Then rows at or before the cursor are dropped and the total is unchanged", func() {
				So(valueIDStrings(got), ShouldResemble, []string{ulidAt('3'), ulidAt('5')})
				So(total, ShouldEqual, 4)
			})
		})

		Convey("When listing one attribute definition's values with a cursor", func() {
			got, total, err := repos.ValueReader.ListByDefinition(ctx,
				valueobjects.MustParseAttributeDefinitionID(nameAttr),
				db.Page{Limit: 10, Cursor: db.EncodeKeyset(ulidAt('1'))})
			So(err, ShouldBeNil)

			Convey("Then live rows after the cursor are returned across both tenants and types", func() {
				So(valueIDStrings(got), ShouldResemble, []string{ulidAt('3'), ulidAt('5'), ulidAt('6')})
				So(total, ShouldEqual, 4) // ids 1,3,5,6 — id 4 is archived
			})
		})
	})
}

func TestEntitySummaryKeysetOrdering(t *testing.T) {
	Convey("Given entities last updated at distinct instants", t, func() {
		ctx := context.Background()
		store := memory.NewStore()
		repos := store.Repositories()

		productType := ulidAt('1')
		nameAttr := ulidAt('A')
		typeIDs := []valueobjects.TypeDefinitionID{valueobjects.MustParseTypeDefinitionID(productType)}

		seedValues(ctx, repos,
			valueSnap(ulidAt('1'), tenantA, productType, nameAttr, "oldest",
				valueobjects.NewStringValue("a"), fixedTime, nil),
			valueSnap(ulidAt('2'), tenantA, productType, nameAttr, "middle",
				valueobjects.NewStringValue("b"), fixedTime.Add(time.Hour), nil),
			valueSnap(ulidAt('3'), tenantA, productType, nameAttr, "newest",
				valueobjects.NewStringValue("c"), fixedTime.Add(2*time.Hour), nil),
		)

		Convey("When listing entity summaries without a cursor", func() {
			got, total, err := repos.ValueReader.ListEntities(ctx, tenantA, typeIDs, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then they come back most-recently-updated first", func() {
				So(total, ShouldEqual, 3)
				So(got[0].EntityID.String(), ShouldEqual, "newest")
				So(got[1].EntityID.String(), ShouldEqual, "middle")
				So(got[2].EntityID.String(), ShouldEqual, "oldest")
			})
		})

		Convey("When resuming from the composite (updated-at desc, entity-id asc) cursor", func() {
			cursor := db.EncodeKeyset(
				db.KeysetTime(fixedTime.Add(2*time.Hour)), "newest")
			got, total, err := repos.ValueReader.ListEntities(ctx, tenantA, typeIDs,
				db.Page{Limit: 10, Cursor: cursor})
			So(err, ShouldBeNil)

			Convey("Then only the older entities follow, still newest-first", func() {
				So(got, ShouldHaveLength, 2)
				So(got[0].EntityID.String(), ShouldEqual, "middle")
				So(got[1].EntityID.String(), ShouldEqual, "oldest")
				So(total, ShouldEqual, 3)
			})
		})

		Convey("When the cursor carries fewer columns than the ordering key", func() {
			// A truncated cursor supplies only the timestamp; the tiebreaker
			// column has no counterpart to compare against, so a row whose
			// timestamp matches must not be treated as strictly after it.
			cursor := db.EncodeKeyset(db.KeysetTime(fixedTime.Add(2 * time.Hour)))
			got, _, err := repos.ValueReader.ListEntities(ctx, tenantA, typeIDs,
				db.Page{Limit: 10, Cursor: cursor})
			So(err, ShouldBeNil)

			Convey("Then paging resumes deterministically after the equal-timestamp row", func() {
				So(got, ShouldHaveLength, 2)
				So(got[0].EntityID.String(), ShouldEqual, "middle")
				So(got[1].EntityID.String(), ShouldEqual, "oldest")
			})
		})
	})
}

func TestDependencyRepository(t *testing.T) {
	Convey("Given dependencies across tenants with one archived rule", t, func() {
		ctx := context.Background()
		store := memory.NewStore()
		repos := store.Repositories()

		src, other, target := ulidAt('A'), ulidAt('B'), ulidAt('C')
		seedDeps(ctx, repos,
			depSnap(ulidAt('1'), tenantA, src, target, nil),
			depSnap(ulidAt('2'), tenantA, other, target, nil),
			depSnap(ulidAt('3'), tenantA, src, target, archivedAt(time.Hour)),
			depSnap(ulidAt('4'), tenantB, src, target, nil),
		)

		Convey("When one dependency is fetched by id", func() {
			got, err := repos.Dependencies.Get(ctx, valueobjects.MustParseDependencyID(ulidAt('1')))
			So(err, ShouldBeNil)

			Convey("Then it rehydrates with its source and target", func() {
				So(got.ID().String(), ShouldEqual, ulidAt('1'))
				So(got.SourceAttributeID().String(), ShouldEqual, src)
				So(got.TargetAttributeID().String(), ShouldEqual, target)
			})
		})

		Convey("When an unknown dependency id is fetched", func() {
			_, err := repos.Dependencies.Get(ctx, valueobjects.MustParseDependencyID(ulidAt('9')))

			Convey("Then a typed not-found error is returned", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When a dependency is fetched for update", func() {
			got, err := repos.Dependencies.GetForUpdate(ctx, valueobjects.MustParseDependencyID(ulidAt('2')))
			So(err, ShouldBeNil)
			_, missErr := repos.Dependencies.GetForUpdate(ctx, valueobjects.MustParseDependencyID(ulidAt('9')))

			Convey("Then it reads the same row as Get and reports not-found identically", func() {
				So(got.ID().String(), ShouldEqual, ulidAt('2'))
				So(domainerrors.IsNotFound(missErr), ShouldBeTrue)
			})
		})

		Convey("When listing by source attribute", func() {
			got, err := repos.Dependencies.ListBySource(ctx, valueobjects.MustParseAttributeDefinitionID(src))
			So(err, ShouldBeNil)

			Convey("Then archived rules are skipped and the rest are id-ordered across tenants", func() {
				So(depIDStrings(got), ShouldResemble, []string{ulidAt('1'), ulidAt('4')})
			})
		})

		Convey("When listing by target attribute", func() {
			got, err := repos.Dependencies.ListByTarget(ctx, valueobjects.MustParseAttributeDefinitionID(target))
			So(err, ShouldBeNil)

			Convey("Then every live rule pointing at the target is returned", func() {
				So(depIDStrings(got), ShouldResemble, []string{ulidAt('1'), ulidAt('2'), ulidAt('4')})
			})
		})

		Convey("When listing with a tenant filter", func() {
			got, total, err := repos.Dependencies.List(ctx,
				domaindependency.Filter{TenantID: tenantA}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then the archived rule and tenant B's rule are excluded", func() {
				So(depIDStrings(got), ShouldResemble, []string{ulidAt('1'), ulidAt('2')})
				So(total, ShouldEqual, 2)
			})
		})

		Convey("When archived rules are included", func() {
			got, total, err := repos.Dependencies.List(ctx,
				domaindependency.Filter{TenantID: tenantA, IncludeArchived: true}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then the archived rule joins the page", func() {
				So(depIDStrings(got), ShouldResemble, []string{ulidAt('1'), ulidAt('2'), ulidAt('3')})
				So(total, ShouldEqual, 3)
			})
		})

		Convey("When filtering by source and by target", func() {
			bySource, _, err := repos.Dependencies.List(ctx, domaindependency.Filter{
				TenantID: tenantA, SourceAttributeID: valueobjects.MustParseAttributeDefinitionID(other),
			}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			byMissingTarget, _, err := repos.Dependencies.List(ctx, domaindependency.Filter{
				TenantID: tenantA, TargetAttributeID: valueobjects.MustParseAttributeDefinitionID(src),
			}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then each endpoint filter selects exactly its own rules", func() {
				So(depIDStrings(bySource), ShouldResemble, []string{ulidAt('2')})
				So(byMissingTarget, ShouldBeEmpty)
			})
		})

		Convey("When paging from a cursor", func() {
			got, total, err := repos.Dependencies.List(ctx,
				domaindependency.Filter{TenantID: tenantA},
				db.Page{Limit: 10, Cursor: db.EncodeKeyset(ulidAt('1'))})
			So(err, ShouldBeNil)

			Convey("Then only rules after the cursor are returned", func() {
				So(depIDStrings(got), ShouldResemble, []string{ulidAt('2')})
				So(total, ShouldEqual, 2)
			})
		})
	})
}

func TestRelationshipRepositoryList(t *testing.T) {
	Convey("Given links across two definitions, two tenants and one unlinked row", t, func() {
		ctx := context.Background()
		store := memory.NewStore()
		repos := store.Repositories()

		defOne, defTwo := ulidAt('D'), ulidAt('E')
		seedRels(ctx, repos,
			relSnap(ulidAt('1'), tenantA, defOne, "sku-1", "acme", nil),
			relSnap(ulidAt('2'), tenantA, defOne, "sku-2", "acme", nil),
			relSnap(ulidAt('3'), tenantA, defTwo, "sku-1", "globex", nil),
			relSnap(ulidAt('4'), tenantA, defOne, "sku-9", "acme", archivedAt(time.Hour)),
			relSnap(ulidAt('5'), tenantB, defOne, "sku-1", "acme", nil),
		)

		Convey("When listing with only a tenant filter", func() {
			got, total, err := repos.Relationships.List(ctx,
				domainrelationship.Filter{TenantID: tenantA}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then unlinked and foreign-tenant links are excluded, id-ordered", func() {
				So(relIDStrings(got), ShouldResemble, []string{ulidAt('1'), ulidAt('2'), ulidAt('3')})
				So(total, ShouldEqual, 3)
			})
		})

		Convey("When unlinked rows are included", func() {
			got, total, err := repos.Relationships.List(ctx,
				domainrelationship.Filter{TenantID: tenantA, IncludeArchived: true}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then the unlinked row is returned too", func() {
				So(relIDStrings(got), ShouldContain, ulidAt('4'))
				So(total, ShouldEqual, 4)
			})
		})

		Convey("When narrowing to one relationship definition", func() {
			got, _, err := repos.Relationships.List(ctx, domainrelationship.Filter{
				TenantID:     tenantA,
				DefinitionID: valueobjects.MustParseRelationshipDefinitionID(defTwo),
			}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then only that definition's links are returned", func() {
				So(relIDStrings(got), ShouldResemble, []string{ulidAt('3')})
			})
		})

		Convey("When narrowing to one parent entity", func() {
			got, _, err := repos.Relationships.List(ctx, domainrelationship.Filter{
				TenantID: tenantA, ParentEntityID: valueobjects.EntityID("sku-1"),
			}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then links whose parent side matches are returned, tenant B's excluded", func() {
				So(relIDStrings(got), ShouldResemble, []string{ulidAt('1'), ulidAt('3')})
			})
		})

		Convey("When narrowing to one child entity", func() {
			got, total, err := repos.Relationships.List(ctx, domainrelationship.Filter{
				TenantID: tenantA, ChildEntityID: valueobjects.EntityID("acme"),
			}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then only live links ending at that child are returned", func() {
				So(relIDStrings(got), ShouldResemble, []string{ulidAt('1'), ulidAt('2')})
				So(total, ShouldEqual, 2)
			})
		})

		Convey("When paging from a cursor", func() {
			got, total, err := repos.Relationships.List(ctx,
				domainrelationship.Filter{TenantID: tenantA},
				db.Page{Limit: 10, Cursor: db.EncodeKeyset(ulidAt('1'))})
			So(err, ShouldBeNil)

			Convey("Then only links after the cursor are returned", func() {
				So(relIDStrings(got), ShouldResemble, []string{ulidAt('2'), ulidAt('3')})
				So(total, ShouldEqual, 3)
			})
		})
	})
}

func TestActivityLogListFiltersAndCursor(t *testing.T) {
	Convey("Given an activity log holding entries for two tenants and two entities", t, func() {
		ctx := context.Background()
		store := memory.NewStore()
		log := store.ActivityLog()

		entry := func(id string, tenant valueobjects.TenantID, entity, entityID, actor string, at time.Time) activity.Entry {
			return activity.Entry{
				ID:         ulid.MustParse(id),
				TenantID:   tenant,
				Actor:      actor,
				Entity:     entity,
				EntityID:   entityID,
				Action:     activity.ActionCreated,
				OccurredAt: at,
			}
		}

		newest := entry(ulidAt('3'), tenantA, "attribute_value", "sku-2", "bob", fixedTime.Add(2*time.Hour))
		middle := entry(ulidAt('2'), tenantA, "type_definition", "sku-1", "alice", fixedTime.Add(time.Hour))
		oldest := entry(ulidAt('1'), tenantA, "attribute_value", "sku-1", "alice", fixedTime)
		foreign := entry(ulidAt('4'), tenantB, "attribute_value", "sku-1", "alice", fixedTime.Add(3*time.Hour))

		So(log.Write(ctx, nil, []activity.Entry{oldest, middle, newest, foreign}), ShouldBeNil)

		ids := func(entries []activity.Entry) []string {
			out := make([]string, 0, len(entries))
			for _, e := range entries {
				out = append(out, e.ID.String())
			}
			return out
		}

		Convey("When listing the tenant's whole log", func() {
			got, total, err := log.List(ctx, activity.Filter{TenantID: tenantA}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then entries come back newest-first and the other tenant's entry is invisible", func() {
				So(ids(got), ShouldResemble, []string{ulidAt('3'), ulidAt('2'), ulidAt('1')})
				So(total, ShouldEqual, 3)
			})
		})

		Convey("When filtering by entity kind", func() {
			got, total, err := log.List(ctx,
				activity.Filter{TenantID: tenantA, Entity: "type_definition"}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then only entries about that entity kind are returned", func() {
				So(ids(got), ShouldResemble, []string{ulidAt('2')})
				So(total, ShouldEqual, 1)
			})
		})

		Convey("When filtering by entity id", func() {
			got, _, err := log.List(ctx,
				activity.Filter{TenantID: tenantA, EntityID: "sku-1"}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then only that entity's entries are returned, newest-first", func() {
				So(ids(got), ShouldResemble, []string{ulidAt('2'), ulidAt('1')})
			})
		})

		Convey("When filtering by actor", func() {
			got, _, err := log.List(ctx,
				activity.Filter{TenantID: tenantA, Actor: "bob"}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then only that actor's entries are returned", func() {
				So(ids(got), ShouldResemble, []string{ulidAt('3')})
			})
		})

		Convey("When resuming from the (occurred-at, id) descending cursor", func() {
			cursor := db.EncodeKeyset(
				db.KeysetTime(fixedTime.Add(2*time.Hour)), ulidAt('3'))
			got, total, err := log.List(ctx, activity.Filter{TenantID: tenantA},
				db.Page{Limit: 10, Cursor: cursor})
			So(err, ShouldBeNil)

			Convey("Then only older entries follow the cursor row", func() {
				So(ids(got), ShouldResemble, []string{ulidAt('2'), ulidAt('1')})
				So(total, ShouldEqual, 3)
			})
		})
	})
}

func TestActivityLogJournalledInsideTransaction(t *testing.T) {
	Convey("Given an activity log with one entry already committed", t, func() {
		ctx := context.Background()
		store := memory.NewStore()
		log := store.ActivityLog()

		mk := func(id string, at time.Time) activity.Entry {
			return activity.Entry{
				ID: ulid.MustParse(id), TenantID: tenantA, Actor: "alice",
				Entity: "attribute_value", EntityID: "sku-1",
				Action: activity.ActionCreated, OccurredAt: at,
			}
		}
		So(log.Write(ctx, nil, []activity.Entry{mk(ulidAt('1'), fixedTime)}), ShouldBeNil)

		Convey("When a transaction appends two batches and then rolls back", func() {
			tx, err := store.Transactor().Begin(ctx)
			So(err, ShouldBeNil)
			So(log.Write(ctx, tx, []activity.Entry{mk(ulidAt('2'), fixedTime.Add(time.Hour))}), ShouldBeNil)
			So(log.Write(ctx, tx, []activity.Entry{mk(ulidAt('3'), fixedTime.Add(2*time.Hour))}), ShouldBeNil)

			mid, _, err := log.List(ctx, activity.Filter{TenantID: tenantA}, db.Page{Limit: 10})
			So(err, ShouldBeNil)
			So(mid, ShouldHaveLength, 3) // both batches are visible before the abort

			So(tx.Rollback(ctx), ShouldBeNil)

			Convey("Then BOTH appended batches are unwound and the pre-transaction entry survives", func() {
				got, total, err := log.List(ctx, activity.Filter{TenantID: tenantA}, db.Page{Limit: 10})
				So(err, ShouldBeNil)
				So(got, ShouldHaveLength, 1)
				So(got[0].ID.String(), ShouldEqual, ulidAt('1'))
				So(total, ShouldEqual, 1)
			})
		})

		Convey("When a transaction appends a batch and commits", func() {
			tx, err := store.Transactor().Begin(ctx)
			So(err, ShouldBeNil)
			So(log.Write(ctx, tx, []activity.Entry{mk(ulidAt('2'), fixedTime.Add(time.Hour))}), ShouldBeNil)
			So(tx.Commit(ctx), ShouldBeNil)

			Convey("Then the appended entry is durable", func() {
				got, _, err := log.List(ctx, activity.Filter{TenantID: tenantA}, db.Page{Limit: 10})
				So(err, ShouldBeNil)
				So(got, ShouldHaveLength, 2)
			})
		})
	})
}

func TestRepositoryWithForeignTransactionHandle(t *testing.T) {
	Convey("Given a repository bound to a handle that is not a memory transaction", t, func() {
		ctx := context.Background()
		store := memory.NewStore()
		repos := store.Repositories()

		Convey("When it saves a type definition and an unrelated transaction rolls back", func() {
			bound := repos.TypeDefinitions.WithTx(&plainTx{})
			snap := typeSnap(ulidAt('1'), tenantA, "product", domaintypedef.KindEntity, nil)
			So(bound.Save(ctx, domaintypedef.Rehydrate(snap)), ShouldBeNil)

			tx, err := store.Transactor().Begin(ctx)
			So(err, ShouldBeNil)
			So(tx.Rollback(ctx), ShouldBeNil)

			Convey("Then the write is not journalled and therefore survives — it was never in a transaction", func() {
				got, err := repos.TypeDefinitions.Get(ctx, snap.ID)
				So(err, ShouldBeNil)
				So(got.InternalName(), ShouldEqual, "product")
			})
		})
	})
}
