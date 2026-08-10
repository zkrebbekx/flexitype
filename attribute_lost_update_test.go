package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// runAttributeLostUpdate is the regression for #597.
//
// An attribute update is a FULL REPLACE with no field-presence tracking, and it
// had no concurrency control at all. Two operators editing one attribute each
// sent the whole editable record, so the later write erased the earlier one —
// including fields that operator never looked at, and with nothing reported.
//
// Supplying the version read before the edit turns the silent loss into a 409.
// Omitting it keeps last-write-wins, which is the contract a saved-view patch
// already offers and what an existing caller depends on.
func runAttributeLostUpdate(t *testing.T, label string, newService func() *flexitype.Service) {
	t.Helper()

	Convey("Given an attribute two operators are about to edit ("+label+")", t, func() {
		svc := newService()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		sku, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "sku",
			DisplayName: "SKU", DataType: "string", HelpText: "The vendor code.",
		})
		So(err, ShouldBeNil)

		// Both operators load the SAME record, which is the whole point: the
		// second one's read happens before the first one's write.
		baseline := sku.Version

		update := func(displayName, helpText string, version *int) (*domainattribute.Snapshot, error) {
			return svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
				ID: sku.ID.String(), DisplayName: displayName, HelpText: helpText, Version: version,
			})
		}

		Convey("When the first saves and the second saves the record they read", func() {
			first, ferr := update("Stock code", "The vendor code.", &baseline)
			So(ferr, ShouldBeNil)
			So(first.Version, ShouldEqual, baseline+1)

			_, serr := update("SKU", "Printed on the label.", &baseline)

			Convey("Then the second is refused rather than erasing the first", func() {
				So(serr, ShouldNotBeNil)
				So(domainerrors.CodeOf(serr), ShouldEqual, domainerrors.CodeConflict)
			})

			Convey("Then the first operator's edit is still there", func() {
				current, gerr := svc.Interactors(ctx).Attributes().Get(ctx, sku.ID.String())
				So(gerr, ShouldBeNil)
				So(current.DisplayName, ShouldEqual, "Stock code")
			})

			Convey("Then the refused write changed nothing at all", func() {
				// A conflict must not be a partial apply: the version must not
				// move and the record must not carry half the second edit.
				current, gerr := svc.Interactors(ctx).Attributes().Get(ctx, sku.ID.String())
				So(gerr, ShouldBeNil)
				So(current.Version, ShouldEqual, baseline+1)
				So(current.HelpText, ShouldEqual, "The vendor code.")
			})

			Convey("And re-reading lets the second operator re-apply on top", func() {
				current, gerr := svc.Interactors(ctx).Attributes().Get(ctx, sku.ID.String())
				So(gerr, ShouldBeNil)
				after, aerr := update("Stock code", "Printed on the label.", &current.Version)
				So(aerr, ShouldBeNil)
				So(after.DisplayName, ShouldEqual, "Stock code")
				So(after.HelpText, ShouldEqual, "Printed on the label.")
			})
		})

		Convey("When a caller sends NO version", func() {
			_, ferr := update("Stock code", "The vendor code.", nil)
			So(ferr, ShouldBeNil)
			second, serr := update("SKU", "Printed on the label.", nil)

			Convey("Then it is still last-write-wins, as it always was", func() {
				// Adding the check must not break a caller that never sent
				// one. The compare-and-swap is opt-in.
				So(serr, ShouldBeNil)
				So(second.DisplayName, ShouldEqual, "SKU")
			})
		})

		Convey("When a caller sends the version it just read", func() {
			current, gerr := svc.Interactors(ctx).Attributes().Get(ctx, sku.ID.String())
			So(gerr, ShouldBeNil)
			_, uerr := update("Stock code", "The vendor code.", &current.Version)

			Convey("Then the write succeeds — nothing moved under it", func() {
				So(uerr, ShouldBeNil)
			})
		})

		Convey("When a stale version names a change that would ALSO be invalid", func() {
			// The swap is checked before every other rule, so the operator is
			// told the record moved rather than being told about a validation
			// failure judged against a baseline they never saw.
			_, ferr := update("Stock code", "", &baseline)
			So(ferr, ShouldBeNil)

			_, serr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
				ID: sku.ID.String(), DisplayName: "SKU", Version: &baseline,
				Computed: json.RawMessage(`{"kind":"rollup","relationship":"nope","aggregate":"count"}`),
			})

			Convey("Then the conflict is what it reports", func() {
				So(serr, ShouldNotBeNil)
				So(domainerrors.CodeOf(serr), ShouldEqual, domainerrors.CodeConflict)
			})
		})
	})
}

// TestAttributeLostUpdate runs the scenarios against the in-memory backend.
func TestAttributeLostUpdate(t *testing.T) {
	runAttributeLostUpdate(t, "memory", func() *flexitype.Service { return flexitype.NewInMemory() })
}

// TestAttributeLostUpdatePostgres re-runs them against Postgres, where the
// read is a real SELECT ... FOR UPDATE.
func TestAttributeLostUpdatePostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	runAttributeLostUpdate(t, "postgres", func() *flexitype.Service {
		svc := flexitype.New(pool)
		if err := svc.Migrate(context.Background()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		truncateAll(t, pool)
		return svc
	})
}
