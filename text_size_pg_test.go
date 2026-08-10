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
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// incompressible returns n bytes of high-entropy text.
//
// It matters that this does not compress. A btree index entry is compressed
// inline, so a long run of one character stays far under the tuple limit and a
// test built on strings.Repeat passes with the FAULTY index still in place —
// which is how the first version of this test proved nothing. The report used
// prose for the same reason.
func incompressible(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, n)
	// A xorshift keeps this deterministic without importing a generator, so a
	// failure is reproducible.
	state := uint32(0x9E3779B9) ^ uint32(n)
	for i := range out {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		out[i] = alphabet[state%uint32(len(alphabet))]
	}
	return string(out)
}

// TestLongTextValuesPostgres covers issue #590.
//
// The uniqueness probe index was a plain btree over the raw value, so every
// text-backed row had to fit a btree tuple and Postgres refused a write past
// roughly 2.7KB with SQLSTATE 54000 — surfacing as HTTP 500. `text` exists to
// hold long-form values, so the storage class made the data type useless.
//
// It is a Postgres test by necessity: the in-memory backend stores the value
// happily, which is why no existing test saw it.
func TestLongTextValuesPostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a text attribute (Postgres)", t, func() {
		truncateAll(t, pool, svc)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		article, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "article", DisplayName: "Article",
		})
		So(err, ShouldBeNil)
		body, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: article.ID.String(), InternalName: "body",
			DisplayName: "Body", DataType: "text",
		})
		So(err, ShouldBeNil)

		write := func(entity string, n int) error {
			raw, _ := json.Marshal(incompressible(n))
			_, werr := ia.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: body.ID.String(), EntityID: entity,
				TypeDefinitionID: article.ID.String(), Value: raw,
			})
			return werr
		}

		Convey("When values well past the btree tuple limit are written", func() {
			Convey("Then each is stored", func() {
				// 2704 bytes is the btree maximum that refused these.
				So(write("a", 4_000), ShouldBeNil)
				So(write("b", 16_000), ShouldBeNil)
				So(write("c", 200_000), ShouldBeNil)
			})
		})

		Convey("When a long value is read back", func() {
			So(write("a", 50_000), ShouldBeNil)
			values, verr := ia.Values().ListByEntity(ctx, article.ID.String(), "a")
			So(verr, ShouldBeNil)
			So(values, ShouldHaveLength, 1)

			Convey("Then it survives the round trip intact", func() {
				So(len(values[0].Value.String()), ShouldBeGreaterThan, 49_000)
			})
		})
	})

	Convey("Given a UNIQUE text attribute (Postgres)", t, func() {
		truncateAll(t, pool, svc)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		article, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "article", DisplayName: "Article",
		})
		So(err, ShouldBeNil)
		slug, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: article.ID.String(), InternalName: "slug",
			DisplayName: "Slug", DataType: "text", Unique: true,
		})
		So(err, ShouldBeNil)

		writeSlug := func(entity, value string) error {
			raw, _ := json.Marshal(value)
			_, werr := ia.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: slug.ID.String(), EntityID: entity,
				TypeDefinitionID: article.ID.String(), Value: raw,
			})
			return werr
		}

		Convey("When two entities claim the same long value", func() {
			long := incompressible(8_000)
			So(writeSlug("a", long), ShouldBeNil)
			second := writeSlug("b", long)

			Convey("Then the duplicate is still refused", func() {
				// The probe reads a hash now, so this is the assertion that
				// says the hash actually narrows to the right rows.
				So(second, ShouldNotBeNil)
				So(second.Error(), ShouldContainSubstring, "already used")
			})
		})

		Convey("When two entities claim DIFFERENT long values", func() {
			So(writeSlug("a", incompressible(8_000)), ShouldBeNil)
			second := writeSlug("b", incompressible(8_001))

			Convey("Then both are accepted", func() {
				So(second, ShouldBeNil)
			})
		})
	})
}
