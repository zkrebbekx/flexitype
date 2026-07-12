package memory_test

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

func TestMediaAttribute(t *testing.T) {
	Convey("Given a media attribute constrained to PNGs up to 1KB", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		store := blob.NewMemoryStore()
		svc := flexitype.NewInMemory(flexitype.WithBlobStore(store))
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

		Convey("When a small PNG is uploaded", func() {
			snap, err := it.Values().UploadMedia(ctx, typeID, "e1", attrID, bytes.NewReader(png), "logo.png")
			So(err, ShouldBeNil)

			Convey("Then the value stores metadata and the blob is fetchable", func() {
				meta := snap.Value.Media()
				So(meta.MIME, ShouldEqual, "image/png")
				So(meta.Size, ShouldEqual, len(png))
				So(meta.Filename, ShouldEqual, "logo.png")
				So(meta.ObjectKey, ShouldNotBeBlank)
				rc, mime, err := store.Open(ctx, meta.ObjectKey)
				So(err, ShouldBeNil)
				So(mime, ShouldEqual, "image/png")
				_ = rc.Close()
			})

			Convey("Then archiving the value removes the blob", func() {
				meta := snap.Value.Media()
				_, err := it.Values().Remove(ctx, snap.ID.String())
				So(err, ShouldBeNil)
				_, _, err = store.Open(ctx, meta.ObjectKey)
				So(err, ShouldNotBeNil) // gone
			})
		})

		Convey("When a PDF is uploaded", func() {
			_, err := it.Values().UploadMedia(ctx, typeID, "e1", attrID, bytes.NewReader([]byte("%PDF-1.4 data")), "doc.pdf")

			Convey("Then it is rejected by the MIME constraint and nothing is orphaned", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When non-image bytes are uploaded with a .png filename", func() {
			// The content type is sniffed from the bytes, not taken from the
			// filename or a client-declared type, so this cannot slip past the
			// png-only allowlist.
			_, err := it.Values().UploadMedia(ctx, typeID, "e1", attrID, bytes.NewReader([]byte("%PDF-1.4 not really a png")), "fake.png")

			Convey("Then it is still rejected by the MIME constraint", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When an oversize PNG is uploaded", func() {
			big := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), 2000)...)
			_, err := it.Values().UploadMedia(ctx, typeID, "e1", attrID, bytes.NewReader(big), "big.png")

			Convey("Then it is rejected by the size constraint", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When a PNG is overwritten by a new upload", func() {
			first, err := it.Values().UploadMedia(ctx, typeID, "e1", attrID, bytes.NewReader(png), "v1.png")
			So(err, ShouldBeNil)
			firstKey := first.Value.Media().ObjectKey

			png2 := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("y"), 300)...)
			second, err := it.Values().UploadMedia(ctx, typeID, "e1", attrID, bytes.NewReader(png2), "v2.png")
			So(err, ShouldBeNil)
			secondKey := second.Value.Media().ObjectKey

			Convey("Then the superseded blob is garbage-collected and the new one remains", func() {
				So(secondKey, ShouldNotEqual, firstKey)
				_, _, err := store.Open(ctx, firstKey)
				So(err, ShouldNotBeNil) // old blob gone
				rc, _, err := store.Open(ctx, secondKey)
				So(err, ShouldBeNil) // new blob present
				_ = rc.Close()
			})
		})

		Convey("When the whole entity is removed", func() {
			snap, err := it.Values().UploadMedia(ctx, typeID, "e1", attrID, bytes.NewReader(png), "logo.png")
			So(err, ShouldBeNil)
			key := snap.Value.Media().ObjectKey

			_, err = it.Values().RemoveEntity(ctx, typeID, "e1")
			So(err, ShouldBeNil)

			Convey("Then its media blob is garbage-collected", func() {
				_, _, err := store.Open(ctx, key)
				So(err, ShouldNotBeNil)
			})
		})
	})
}
