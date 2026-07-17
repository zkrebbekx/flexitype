package flexitype_test

import (
	"context"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/blob"
)

// TestMediaTenantOwnershipPostgres exercises the real Postgres media-ownership
// query (value_json->>'object_key' scoped by tenant) that guards the media
// download handler against a cross-tenant file read (IDOR).
func TestMediaTenantOwnershipPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool, flexitype.WithBlobStore(blob.NewMemoryStore()))
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given tenant A uploads a media file into Postgres", t, func() {
		truncateAll(t, pool)
		ctxA := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-a"))
		ctxB := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-b"))

		ia := svc.Interactors(ctxA)
		doc, err := ia.TypeDefinitions().Create(ctxA, apptypedef.CreateInput{InternalName: "doc", DisplayName: "Doc"})
		So(err, ShouldBeNil)
		file, err := ia.Attributes().Create(ctxA, appattribute.CreateInput{
			TypeDefinitionID: doc.ID.String(), InternalName: "file", DisplayName: "File", DataType: "media",
		})
		So(err, ShouldBeNil)
		snap, err := ia.Values().UploadMedia(ctxA, doc.ID.String(), "e1", file.ID.String(),
			strings.NewReader("hello world"), "note.txt")
		So(err, ShouldBeNil)
		key := snap.Value.Media().ObjectKey
		So(key, ShouldNotEqual, "")

		Convey("Then only tenant A owns the key; tenant B and unknown keys are denied", func() {
			ownedA, err := svc.Interactors(ctxA).Values().MediaKeyOwned(ctxA, key)
			So(err, ShouldBeNil)
			So(ownedA, ShouldBeTrue)

			ownedB, err := svc.Interactors(ctxB).Values().MediaKeyOwned(ctxB, key)
			So(err, ShouldBeNil)
			So(ownedB, ShouldBeFalse)

			unknown, err := svc.Interactors(ctxA).Values().MediaKeyOwned(ctxA, "nope.txt")
			So(err, ShouldBeNil)
			So(unknown, ShouldBeFalse)
		})
	})
}
