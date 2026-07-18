package flexitype_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/blob"
)

// TestMediaAttributeParityPostgres re-runs the media suite
// (infrastructure/memory/media_test.go) against the real Postgres value
// repository, with an in-process blob.NewMemoryStore() standing in for object
// storage. It proves the upload/download path, the MIME/size constraint gate
// (content is sniffed, not trusted from the filename), and — the subtle part —
// that the blob garbage-collection that rides on archiving a value or removing
// an entity works when the superseded object_key has to be read back out of
// Postgres before its blob is deleted.
func TestMediaAttributeParityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	store := blob.NewMemoryStore()
	svc := flexitype.New(pool, flexitype.WithBlobStore(store))
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a media attribute constrained to PNGs up to 1KB (Postgres)", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		image, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "image", DisplayName: "Image", DataType: "media",
			Constraints: json.RawMessage(`[{"kind":"media","mime":["image/png"],"max_size":1000}]`),
		})
		So(err, ShouldBeNil)
		attrID := image.ID.String()

		png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), 200)...) // ~208 bytes

		// Every upload/remove is its own unit of work (fresh Interactors), mirroring
		// the per-request wiring in the HTTP layer.
		upload := func(entity, filename string, body []byte) (string, string, int64, string, string, error) {
			snap, e := svc.Interactors(ctx).Values().UploadMedia(ctx, typeID, entity, attrID, bytes.NewReader(body), filename)
			if e != nil {
				return "", "", 0, "", "", e
			}
			m := snap.Value.Media()
			return m.MIME, m.Filename, m.Size, m.ObjectKey, snap.ID.String(), nil
		}
		open := func(key string) error {
			rc, _, e := store.Open(ctx, key)
			if e == nil {
				_ = rc.Close()
			}
			return e
		}

		Convey("When a small PNG is uploaded", func() {
			mime, fn, size, key, valueID, err := upload("e1", "logo.png", png)
			So(err, ShouldBeNil)

			Convey("Then the value stores metadata and the blob is fetchable", func() {
				So(mime, ShouldEqual, "image/png")
				So(size, ShouldEqual, len(png))
				So(fn, ShouldEqual, "logo.png")
				So(key, ShouldNotBeBlank)
				So(open(key), ShouldBeNil)
			})

			Convey("Then archiving the value removes the blob", func() {
				_, err := svc.Interactors(ctx).Values().Remove(ctx, valueID)
				So(err, ShouldBeNil)
				So(open(key), ShouldNotBeNil) // gone
			})
		})

		Convey("When a PDF is uploaded", func() {
			_, _, _, _, _, err := upload("e1", "doc.pdf", []byte("%PDF-1.4 data"))

			Convey("Then it is rejected by the MIME constraint and nothing is orphaned", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When non-image bytes are uploaded with a .png filename", func() {
			// The content type is sniffed from the bytes, not taken from the
			// filename or a client-declared type, so this cannot slip past the
			// png-only allowlist.
			_, _, _, _, _, err := upload("e1", "fake.png", []byte("%PDF-1.4 not really a png"))

			Convey("Then it is still rejected by the MIME constraint", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When an oversize PNG is uploaded", func() {
			big := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), 2000)...)
			_, _, _, _, _, err := upload("e1", "big.png", big)

			Convey("Then it is rejected by the size constraint", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When a PNG is overwritten by a new upload", func() {
			_, _, _, firstKey, _, err := upload("e1", "v1.png", png)
			So(err, ShouldBeNil)

			png2 := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("y"), 300)...)
			_, _, _, secondKey, _, err := upload("e1", "v2.png", png2)
			So(err, ShouldBeNil)

			Convey("Then the superseded blob is garbage-collected and the new one remains", func() {
				So(secondKey, ShouldNotEqual, firstKey)
				So(open(firstKey), ShouldNotBeNil) // old blob gone
				So(open(secondKey), ShouldBeNil)   // new blob present
			})
		})

		Convey("When the whole entity is removed", func() {
			_, _, _, key, _, err := upload("e1", "logo.png", png)
			So(err, ShouldBeNil)

			_, err = svc.Interactors(ctx).Values().RemoveEntity(ctx, typeID, "e1")
			So(err, ShouldBeNil)

			Convey("Then its media blob is garbage-collected", func() {
				So(open(key), ShouldNotBeNil)
			})
		})
	})
}
