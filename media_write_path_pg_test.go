package flexitype_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/blob"
)

// TestMediaWritePathPostgres covers the four consequences of accepting raw
// media metadata through the generic value write.
//
// Media download authorization asks "does any value row in my tenant reference
// this key". Minting that row was therefore enough to read another tenant's
// file, and removing it was enough to delete their bytes. Object keys are not
// secret — they leak into revision payloads, CSV exports and URLs — so the
// attack needed no unusual access.
func TestMediaWritePathPostgres(t *testing.T) {
	pool := openTestDB(t)
	blobs := blob.NewMemoryStore()

	svc := flexitype.New(pool, flexitype.WithBlobStore(blobs))
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given two tenants, each with a media attribute (Postgres)", t, func() {
		truncateAll(t, pool)
		ctxA := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-a"))
		ctxB := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-b"))

		setup := func(ctx context.Context) (typeID, attrID string) {
			ia := svc.Interactors(ctx)
			doc, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
				InternalName: "doc", DisplayName: "Doc",
			})
			So(err, ShouldBeNil)
			file, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: doc.ID.String(), InternalName: "file", DisplayName: "File",
				DataType: "media",
			})
			So(err, ShouldBeNil)
			return doc.ID.String(), file.ID.String()
		}
		typeA, attrA := setup(ctxA)
		typeB, attrB := setup(ctxB)

		// Tenant A uploads a file through the only path that may declare
		// media metadata.
		snapA, err := svc.Interactors(ctxA).Values().UploadMedia(ctxA, typeA, "a1", attrA,
			strings.NewReader("tenant A secret"), "secret.txt")
		So(err, ShouldBeNil)
		keyA := snapA.Value.Media().ObjectKey
		So(keyA, ShouldNotBeBlank)

		// Writing a media value by hand, the way an API client would.
		writeMedia := func(ctx context.Context, typeID, attrID, entity, key string, mime string, size int64) error {
			raw, _ := json.Marshal(map[string]any{
				"object_key": key, "mime": mime, "size": size,
			})
			_, err := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: entity,
				TypeDefinitionID: typeID, Value: raw,
			})
			return err
		}

		Convey("Tenant B cannot mint a value referencing tenant A's object key", func() {
			err := writeMedia(ctxB, typeB, attrB, "b1", keyA, "image/png", 1)

			Convey("Then the write is rejected", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "unknown media object key")
			})

			Convey("Then the cross-tenant download stays closed", func() {
				readable, err := svc.Interactors(ctxB).Values().MediaKeyReadable(ctxB, keyA)
				So(err, ShouldBeNil)
				So(readable, ShouldBeFalse)
			})

			Convey("Then tenant A's blob is untouched", func() {
				rc, _, err := blobs.Open(context.Background(), keyA)
				So(err, ShouldBeNil)
				So(rc.Close(), ShouldBeNil)
			})
		})

		Convey("An unknown object key is rejected for its own tenant too", func() {
			err := writeMedia(ctxA, typeA, attrA, "a2", "01NOTREAL.png", "image/png", 1)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "unknown media object key")

			Convey("And another tenant's real key is refused the same way", func() {
				// Same message shape either way, so a caller cannot use the
				// error to learn whether a key exists in another tenant.
				other := writeMedia(ctxB, typeB, attrB, "b2", keyA, "image/png", 1)
				So(other, ShouldNotBeNil)
				So(domainerrors.CodeOf(other), ShouldEqual, domainerrors.CodeOf(err))
				So(other.Error(), ShouldContainSubstring, "unknown media object key")
			})
		})

		Convey("A same-tenant write inherits the stored metadata rather than declaring it", func() {
			// The caller lies about the type and the size; both are discarded.
			So(writeMedia(ctxA, typeA, attrA, "a3", keyA, "application/x-lies", 999999), ShouldBeNil)

			vals, err := svc.Interactors(ctxA).Values().ListByEntity(ctxA, typeA, "a3")
			So(err, ShouldBeNil)
			So(vals, ShouldHaveLength, 1)
			meta := vals[0].Value.Media()

			Convey("Then the MIME type is the one sniffed from the bytes at upload", func() {
				So(meta.MIME, ShouldNotEqual, "application/x-lies")
				So(meta.MIME, ShouldContainSubstring, "text/plain")
			})

			Convey("Then the size is the real byte count", func() {
				So(meta.Size, ShouldEqual, int64(len("tenant A secret")))
			})

			Convey("Then the checksum survives, so the reference is verifiable", func() {
				So(meta.Checksum, ShouldStartWith, "sha256:")
			})
		})

		Convey("Blob GC is reference-counted", func() {
			// Two entities of the same tenant referencing one key.
			So(writeMedia(ctxA, typeA, attrA, "a4", keyA, "text/plain", 1), ShouldBeNil)

			vals, err := svc.Interactors(ctxA).Values().ListByEntity(ctxA, typeA, "a4")
			So(err, ShouldBeNil)
			So(vals, ShouldHaveLength, 1)

			Convey("When one of them is removed", func() {
				_, err := svc.Interactors(ctxA).Values().Remove(ctxA, vals[0].ID.String())
				So(err, ShouldBeNil)

				Convey("Then the bytes survive for the value that still references them", func() {
					rc, _, err := blobs.Open(context.Background(), keyA)
					So(err, ShouldBeNil)
					So(rc.Close(), ShouldBeNil)

					readable, err := svc.Interactors(ctxA).Values().MediaKeyReadable(ctxA, keyA)
					So(err, ShouldBeNil)
					So(readable, ShouldBeTrue)
				})
			})
		})
	})
}
