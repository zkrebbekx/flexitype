package memory_test

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

// TestMediaArchivedKeyAdoption is the in-memory regression test for issue
// #485, the twin of TestMediaArchivedKeyAdoptionPostgres.
//
// MediaValueForKey resolves through archived rows, but blob GC counts only
// live rows. So a Remove of the last live value deletes the blob, and a later
// Set that adopts the same key succeeded through the archived row — a live
// media value whose bytes were already gone. The write must fail instead.
func TestMediaArchivedKeyAdoption(t *testing.T) {
	Convey("Given an uploaded file whose only value was removed (memory)", t, func() {
		blobs := blob.NewMemoryStore()
		svc := flexitype.NewInMemory(flexitype.WithBlobStore(blobs))
		ctx := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-a"))

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
		typeID, attrID := doc.ID.String(), file.ID.String()

		snap, err := svc.Interactors(ctx).Values().UploadMedia(ctx, typeID, "e1", attrID,
			strings.NewReader("bytes"), "f.txt")
		So(err, ShouldBeNil)
		key := snap.Value.Media().ObjectKey

		_, err = svc.Interactors(ctx).Values().Remove(ctx, snap.ID.String())
		So(err, ShouldBeNil)

		// The remove archived the only live row, so GC deleted the blob.
		_, _, openErr := blobs.Open(context.Background(), key)
		So(openErr, ShouldNotBeNil)

		Convey("When another entity adopts the archived-only key", func() {
			raw, _ := json.Marshal(map[string]any{
				"object_key": key, "mime": "text/plain", "size": 1,
			})
			_, adoptErr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: "e2",
				TypeDefinitionID: typeID, Value: raw,
			})

			Convey("Then the write is rejected instead of minting a value with no bytes", func() {
				So(adoptErr, ShouldNotBeNil)
				So(domainerrors.CodeOf(adoptErr), ShouldEqual, domainerrors.CodeValidation)
				So(adoptErr.Error(), ShouldContainSubstring, "no stored bytes")
			})

			Convey("Then the entity holds no live media value", func() {
				vals, lerr := svc.Interactors(ctx).Values().ListByEntity(ctx, typeID, "e2")
				So(lerr, ShouldBeNil)
				So(vals, ShouldBeEmpty)
			})
		})
	})
}
