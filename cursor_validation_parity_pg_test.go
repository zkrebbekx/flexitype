package flexitype_test

import (
	"context"
	"encoding/json"
	"net/url"
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

// seedCursorCatalog creates one "widget" type with a "name" attribute and
// five entities, so an entity listing has enough rows for two pages of two.
// It returns the type definition's id.
func seedCursorCatalog(ctx context.Context, t *testing.T, svc *flexitype.Service) string {
	t.Helper()
	it := svc.Interactors(ctx)

	widget, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "widget", DisplayName: "Widget",
	})
	if err != nil {
		t.Fatalf("create type: %v", err)
	}
	name, err := it.Attributes().Create(ctx, appattribute.CreateInput{
		TypeDefinitionID: widget.ID.String(), InternalName: "name", DisplayName: "Name",
		DataType: "string",
	})
	if err != nil {
		t.Fatalf("create attribute: %v", err)
	}
	for _, entity := range []string{"e1", "e2", "e3", "e4", "e5"} {
		raw, _ := json.Marshal(entity)
		if _, err := it.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: name.ID.String(), EntityID: entity,
			TypeDefinitionID: widget.ID.String(), Value: raw,
		}); err != nil {
			t.Fatalf("set %s: %v", entity, err)
		}
	}
	return widget.ID.String()
}

// entityIDs flattens an entity page into its entity ids, in page order.
func entityIDs(out *appvalue.EntityListOutput) []string {
	ids := make([]string, 0, len(out.Items))
	for _, i := range out.Items {
		ids = append(ids, i.EntityID)
	}
	return ids
}

// TestCursorValidationParity pins the fix for issue #502 on BOTH backends.
//
// A pagination cursor is opaque client input. Before the fix, two well-formed
// cursors carrying unusable values behaved badly, and the two backends did not
// even agree:
//
//   - EncodeKeyset("not-a-time", "e1") passed the shape check in
//     PageArgs.Resolve and reached the "::timestamptz" cast in the compiled
//     query. PostgreSQL failed with SQLSTATE 22007 and the caller received an
//     internal error (HTTP 500). The in-memory backend compared the value as a
//     string and answered a page.
//   - A cursor with the wrong number of values was discarded by both backends,
//     so the query ran with no keyset predicate and re-served page 1 to a
//     caller who believed it was advancing.
//
// Both are now domain validation errors, on both backends, and the HTTP layer
// maps them to 422.
func TestCursorValidationParity(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	pg := flexitype.New(pool)
	if err := pg.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given the same widget catalog in the memory and Postgres backends", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

		mem := flexitype.NewInMemory()
		backends := []struct {
			name string
			svc  *flexitype.Service
		}{
			{"memory", mem},
			{"postgres", pg},
		}

		for _, backend := range backends {
			backend := backend
			typeID := seedCursorCatalog(ctx, t, backend.svc)
			it := backend.svc.Interactors(ctx)
			two := 2

			Convey("Given the "+backend.name+" backend", func() {
				Convey("When an entity listing is asked for with a cursor whose timestamp is garbage", func() {
					cursor := db.EncodeKeyset("not-a-time", "e1")
					out, err := it.Values().ListEntities(ctx, typeID, false,
						db.PageArgs{Limit: &two, Cursor: &cursor})

					Convey("Then it is a validation error, not an internal error", func() {
						So(err, ShouldNotBeNil)
						So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
						So(out, ShouldBeNil)
					})

					Convey("And the message does not repeat the cursor's contents", func() {
						So(err.Error(), ShouldNotContainSubstring, "not-a-time")
					})
				})

				Convey("When an entity listing is asked for with a cursor of the wrong arity", func() {
					// The entity ordering has two columns; this cursor has one.
					cursor := db.EncodeKeyset(db.KeysetTime(uow.UTCNow()))
					out, err := it.Values().ListEntities(ctx, typeID, false,
						db.PageArgs{Limit: &two, Cursor: &cursor})

					Convey("Then it is rejected rather than re-serving page 1", func() {
						So(err, ShouldNotBeNil)
						So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
						So(out, ShouldBeNil)
					})
				})

				Convey("When a type-definition listing is asked for with a cursor of the wrong arity", func() {
					// The type-definition ordering is the id alone.
					cursor := db.EncodeKeyset("id-one", "id-two")
					out, err := it.TypeDefinitions().List(ctx, apptypedef.ListInput{
						Page: db.PageArgs{Limit: &two, Cursor: &cursor},
					})

					Convey("Then it is rejected rather than re-serving page 1", func() {
						So(err, ShouldNotBeNil)
						So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
						So(out, ShouldBeNil)
					})
				})

				Convey("When an FQL query is run with a cursor whose timestamp is garbage", func() {
					cursor := db.EncodeKeyset("not-a-time", "e1")
					out, err := it.Query().Execute(ctx, appquery.ExecuteInput{
						Type: "widget", Query: "name is not empty",
						Page: db.PageArgs{Limit: &two, Cursor: &cursor},
					})

					Convey("Then it is a validation error, not an internal error", func() {
						So(err, ShouldNotBeNil)
						So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
						So(out, ShouldBeNil)
					})
				})

				Convey("When two pages are walked with the cursors the listing itself returns", func() {
					first, err := it.Values().ListEntities(ctx, typeID, false,
						db.PageArgs{Limit: &two})
					So(err, ShouldBeNil)
					So(first.PageInfo.HasNextPage, ShouldBeTrue)
					So(first.PageInfo.NextCursor, ShouldNotBeNil)

					second, err := it.Values().ListEntities(ctx, typeID, false,
						db.PageArgs{Limit: &two, Cursor: first.PageInfo.NextCursor})
					So(err, ShouldBeNil)

					Convey("Then the two pages carry four different entities", func() {
						page1 := entityIDs(first)
						page2 := entityIDs(second)
						So(page1, ShouldHaveLength, 2)
						So(page2, ShouldHaveLength, 2)

						seen := map[string]int{}
						for _, id := range append(append([]string{}, page1...), page2...) {
							seen[id]++
						}
						So(seen, ShouldHaveLength, 4) // no entity repeated
					})

					Convey("And they are exactly the first four of the unpaged listing — none skipped", func() {
						all := 10
						full, err := it.Values().ListEntities(ctx, typeID, false,
							db.PageArgs{Limit: &all})
						So(err, ShouldBeNil)
						So(entityIDs(full), ShouldHaveLength, 5)
						So(append(entityIDs(first), entityIDs(second)...),
							ShouldResemble, entityIDs(full)[:4])
					})
				})
			})
		}
	})
}

// TestCursorValidationHTTPStatus proves the rejection reaches the client as a
// 422 with the stable VALIDATION code, which is the whole point of issue #502:
// the same request used to return 500.
func TestCursorValidationHTTPStatus(t *testing.T) {
	Convey("Given a widget type with entities behind the REST API", t, func() {
		a := newAPI(t, flexitype.APIConfig{})

		created := a.post("/api/v1/type-definitions", map[string]any{
			"internal_name": "widget", "display_name": "Widget",
		})
		So(created.Status, ShouldEqual, 201)
		typeID := created.str(t, "id")

		attr := a.post("/api/v1/attributes", map[string]any{
			"type_definition_id": typeID, "internal_name": "name",
			"display_name": "Name", "data_type": "string",
		})
		So(attr.Status, ShouldEqual, 201)

		Convey("When an entity listing is requested with a cursor whose timestamp is garbage", func() {
			cursor := url.QueryEscape(db.EncodeKeyset("not-a-time", "e1"))
			resp := a.get("/api/v1/entities/" + typeID + "?limit=2&cursor=" + cursor)

			Convey("Then the API answers 422 VALIDATION rather than 500 INTERNAL", func() {
				So(resp.Status, ShouldEqual, 422)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})

			Convey("And the message does not repeat the cursor's contents", func() {
				So(resp.errorMessage(), ShouldNotContainSubstring, "not-a-time")
			})
		})

		Convey("When an entity listing is requested with a cursor of the wrong arity", func() {
			cursor := url.QueryEscape(db.EncodeKeyset(db.KeysetTime(uow.UTCNow())))
			resp := a.get("/api/v1/entities/" + typeID + "?limit=2&cursor=" + cursor)

			Convey("Then the API answers 422 rather than serving page 1 again", func() {
				So(resp.Status, ShouldEqual, 422)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})
	})
}
