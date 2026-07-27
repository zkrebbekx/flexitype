package flexitype_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// TestErasureRedactsResiduals covers the copies an erasure left readable.
//
// PurgeEntity and PurgeTenant hard-deleted values, links, revisions, search
// documents and media blobs — and reported success while the same values
// stayed readable in the activity log, where every prior write persisted a
// full value snapshot in the entry's before/after state. The purge report
// claimed completion regardless, so a right-to-erasure request could be
// recorded as satisfied while the data was still there.
//
// Redaction rather than deletion: the activity log survives an erasure on
// purpose, so the erasure stays provable. The proof survives; the personal
// data does not.
func TestErasureRedactsResiduals(t *testing.T) {
	Convey("Given an entity whose writes are in the activity log", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "patient", DisplayName: "Patient",
		})
		So(err, ShouldBeNil)
		name, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "full_name",
			DisplayName: "Full name", DataType: "string",
		})
		So(err, ShouldBeNil)

		write := func(entity, v string) {
			raw, _ := json.Marshal(v)
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: name.ID.String(), EntityID: entity,
				TypeDefinitionID: product.ID.String(), Value: raw,
			})
			So(serr, ShouldBeNil)
		}
		write("p1", "Ada Lovelace")
		write("p1", "Ada King")
		write("p2", "Grace Hopper")

		// auditText returns every audit entry's before/after JSON, joined.
		auditText := func() string {
			limit := 500
			entries, aerr := svc.Interactors(ctx).Activity().List(ctx, application.ActivityListInput{
				Page: db.PageArgs{Limit: &limit},
			})
			So(aerr, ShouldBeNil)
			var b strings.Builder
			for _, e := range entries.Items {
				b.Write(e.Before)
				b.Write(e.After)
			}
			return b.String()
		}
		So(auditText(), ShouldContainSubstring, "Ada Lovelace")

		Convey("When the entity is purged", func() {
			report, perr := svc.Interactors(ctx).Erasure().PurgeEntity(ctx, product.ID.String(), "p1")
			So(perr, ShouldBeNil)

			Convey("Then the report says how many records were redacted", func() {
				So(report.RecordsRedacted, ShouldBeGreaterThan, 0)
			})

			Convey("Then the erased values are gone from the audit log", func() {
				text := auditText()
				So(text, ShouldNotContainSubstring, "Ada Lovelace")
				So(text, ShouldNotContainSubstring, "Ada King")
			})

			Convey("Then another entity's values are untouched", func() {
				So(auditText(), ShouldContainSubstring, "Grace Hopper")
			})

			Convey("Then the audit entries themselves survive, so erasure stays provable", func() {
				limit := 500
				entries, aerr := svc.Interactors(ctx).Activity().List(ctx, application.ActivityListInput{
					Page: db.PageArgs{Limit: &limit},
				})
				So(aerr, ShouldBeNil)
				So(len(entries.Items), ShouldBeGreaterThan, 0)

				var purged int
				for _, e := range entries.Items {
					if e.EntityID == "p1" {
						purged++
					}
				}
				So(purged, ShouldBeGreaterThan, 0)
			})
		})

		Convey("When the whole tenant is purged", func() {
			report, perr := svc.Interactors(ctx).Erasure().PurgeTenant(ctx)
			So(perr, ShouldBeNil)

			Convey("Then no entity's values remain in the audit log", func() {
				So(report.RecordsRedacted, ShouldBeGreaterThan, 0)
				text := auditText()
				So(text, ShouldNotContainSubstring, "Ada Lovelace")
				So(text, ShouldNotContainSubstring, "Grace Hopper")
			})
		})
	})
}
