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
			owned, err := svc.Interactors(ctxA).Values().MediaKeyReadable(ctxA, key)
			So(err, ShouldBeNil)
			So(owned, ShouldBeTrue)
		})

		Convey("And tenant B does not own it — the cross-tenant download (IDOR) is blocked", func() {
			owned, err := svc.Interactors(ctxB).Values().MediaKeyReadable(ctxB, key)
			So(err, ShouldBeNil)
			So(owned, ShouldBeFalse)
		})

		Convey("And an unknown key is owned by no tenant", func() {
			owned, err := svc.Interactors(ctxA).Values().MediaKeyReadable(ctxA, "does-not-exist.txt")
			So(err, ShouldBeNil)
			So(owned, ShouldBeFalse)
		})
	})
}

// TestMediaKeyLaunderingBlocked covers the field-ACL bypass that adoption
// opened.
//
// Adoption authorized a media write on tenant ownership of the object key
// alone, and the download check granted the bytes if ANY attribute
// referencing the key was readable. A principal restricted from
// `passport_scan` therefore needed only write access to some other media
// attribute: POST the restricted key into `avatar`, then GET the media
// endpoint. Object keys are not secret — they leak into value payloads,
// exports and revision snapshots — so nothing had to be guessed.
func TestMediaKeyLaunderingBlocked(t *testing.T) {
	Convey("Given a restricted media attribute and a writable one", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		person, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "person", DisplayName: "Person"})
		So(err, ShouldBeNil)
		mk := func(name string) string {
			a, e := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: person.ID.String(), InternalName: name,
				DisplayName: name, DataType: "media",
			})
			So(e, ShouldBeNil)
			return a.ID.String()
		}
		passport := mk("passport_scan")
		avatar := mk("avatar")

		snap, err := it.Values().UploadMedia(ctx, person.ID.String(), "p1", passport,
			strings.NewReader("passport bytes"), "passport.txt")
		So(err, ShouldBeNil)
		key := snap.Value.Media().ObjectKey

		// The attacker: no read on passport_scan, full access to avatar.
		restricted := uow.WithAccess(ctx, uow.Access{
			Attr: map[string]uow.Perm{"passport_scan": uow.PermNone},
		})
		rit := svc.Interactors(restricted)

		Convey("When the restricted principal tries to download the key directly", func() {
			ok, err := rit.Values().MediaKeyReadable(restricted, key)

			Convey("Then it is refused", func() {
				So(err, ShouldBeNil)
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When it adopts the restricted key into the attribute it may write", func() {
			_, err := rit.Values().Set(restricted, appvalue.SetInput{
				AttributeDefinitionID: avatar, EntityID: "p1",
				TypeDefinitionID: person.ID.String(),
				Value:            json.RawMessage(`{"object_key":"` + key + `","mime":"text/plain","size":1}`),
			})

			Convey("Then the adoption is refused, with the same error as an unknown key", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "unknown media object key")
			})

			Convey("Then the bytes are still not downloadable", func() {
				ok, rerr := rit.Values().MediaKeyReadable(restricted, key)
				So(rerr, ShouldBeNil)
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When a principal that MAY read the owning attribute adopts the key", func() {
			_, err := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: avatar, EntityID: "p1",
				TypeDefinitionID: person.ID.String(),
				Value:            json.RawMessage(`{"object_key":"` + key + `","mime":"text/plain","size":1}`),
			})

			Convey("Then adoption still works: the legitimate case is unaffected", func() {
				So(err, ShouldBeNil)
			})

			Convey("Then the restricted principal still cannot download it", func() {
				// The OWNING attribute governs, so a copy under a readable
				// attribute does not launder the bytes into readability.
				ok, rerr := rit.Values().MediaKeyReadable(restricted, key)
				So(rerr, ShouldBeNil)
				So(ok, ShouldBeFalse)
			})
		})
	})
}
