package memory_test

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
)

func TestMediaKeyTenantOwnership(t *testing.T) {
	Convey("Given tenant A has uploaded a media file", t, func() {
		ctxA := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ctxB := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-b"))
		svc := flexitype.NewInMemory()

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

		Convey("Then tenant A owns the key", func() {
			owned, err := svc.Interactors(ctxA).Values().MediaKeyOwned(ctxA, key)
			So(err, ShouldBeNil)
			So(owned, ShouldBeTrue)
		})

		Convey("And tenant B does not own it — the cross-tenant download (IDOR) is blocked", func() {
			owned, err := svc.Interactors(ctxB).Values().MediaKeyOwned(ctxB, key)
			So(err, ShouldBeNil)
			So(owned, ShouldBeFalse)
		})

		Convey("And an unknown key is owned by no tenant", func() {
			owned, err := svc.Interactors(ctxA).Values().MediaKeyOwned(ctxA, "does-not-exist.txt")
			So(err, ShouldBeNil)
			So(owned, ShouldBeFalse)
		})
	})
}
