package memory_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apprevision "github.com/zkrebbekx/flexitype/application/revision"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/blob"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// failingBlobStore stores objects normally but fails every Delete, so an
// erasure's post-commit blob GC cannot complete. It proves the erasure reports
// the residual data honestly rather than claiming a false success.
type failingBlobStore struct{ inner blob.Store }

func (f failingBlobStore) Put(ctx context.Context, key string, r io.Reader, mime string) error {
	return f.inner.Put(ctx, key, r, mime)
}

func (f failingBlobStore) Open(ctx context.Context, key string) (io.ReadCloser, string, error) {
	return f.inner.Open(ctx, key)
}

func (f failingBlobStore) Delete(context.Context, string) error {
	return errors.New("blob storage unavailable")
}

// TestPurgeEntityReportsBlobFailures proves a right-to-erasure purge whose
// backing-blob deletions all fail reports MediaBlobsFailed and the unpurged
// keys — never a silent MediaBlobsPurged=len(keys) false success — and surfaces
// the failure to the cleanup observer.
func TestPurgeEntityReportsBlobFailures(t *testing.T) {
	Convey("Given an entity with a media blob and a blob store whose deletes fail", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		blobs := failingBlobStore{inner: blob.NewMemoryStore()}
		var observed []error
		svc := flexitype.NewInMemory(
			flexitype.WithBlobStore(blobs),
			flexitype.WithSearchIndex(),
			flexitype.WithCleanupObserver(func(err error) { observed = append(observed, err) }),
		)
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		image, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "image", DisplayName: "Image", DataType: "media",
		})
		So(err, ShouldBeNil)

		png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), 50)...)
		mediaSnap, err := it.Values().UploadMedia(ctx, typeID, "e1", image.ID.String(), bytes.NewReader(png), "logo.png")
		So(err, ShouldBeNil)
		blobKey := mediaSnap.Value.Media().ObjectKey

		Convey("When e1 is purged", func() {
			report, err := it.Erasure().PurgeEntity(ctx, typeID, "e1")
			// The erasure still commits: blob GC is post-commit best effort and
			// must not undo a durable erasure.
			So(err, ShouldBeNil)

			Convey("Then the report tells the truth about the failed blob delete", func() {
				So(report.ValuesPurged, ShouldEqual, 1)
				So(report.MediaBlobsPurged, ShouldEqual, 0) // nothing was actually deleted
				So(report.MediaBlobsFailed, ShouldEqual, 1) // the failure is counted
				So(report.UnpurgedBlobKeys, ShouldContain, blobKey)
			})

			Convey("Then the cleanup failure is surfaced to the observer, not swallowed", func() {
				So(observed, ShouldNotBeEmpty)
			})
		})
	})
}

// TestRevisionPurgeJoinsValueTransaction proves the revision store joins the
// value write's transaction via WithTx: a rollback un-does the revision purge
// (no hard-deleted audit trail behind a failed erasure) while a commit makes it
// durable. It exercises the memory backend directly; the Postgres parity twin
// lives in erasure_pg_test.go.
func TestRevisionPurgeJoinsValueTransaction(t *testing.T) {
	Convey("Given two revisions of an entity in the memory revision store", t, func() {
		ctx := context.Background()
		tenant := valueobjects.DefaultTenant
		revStore := memory.NewRevisionStore()
		mk := func(seq int) apprevision.Revision {
			return apprevision.Revision{
				ID: ulid.New(), TenantID: tenant, TypeDefinitionID: "t1", EntityID: "e1",
				Seq: seq, CreatedAt: time.Now(),
			}
		}
		So(revStore.Create(ctx, mk(1)), ShouldBeNil)
		So(revStore.Create(ctx, mk(2)), ShouldBeNil)

		Convey("When they are purged inside a transaction that rolls back", func() {
			tx, err := memory.NewStore().Transactor().Begin(ctx)
			So(err, ShouldBeNil)
			n, err := revStore.WithTx(tx).PurgeEntity(ctx, tenant, "t1", "e1")
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 2)
			So(tx.Rollback(ctx), ShouldBeNil)

			Convey("Then the revisions survive — the purge joined the rolled-back tx", func() {
				revs, err := revStore.List(ctx, tenant, "t1", "e1")
				So(err, ShouldBeNil)
				So(revs, ShouldHaveLength, 2)
			})
		})

		Convey("When they are purged inside a transaction that commits", func() {
			tx, err := memory.NewStore().Transactor().Begin(ctx)
			So(err, ShouldBeNil)
			n, err := revStore.WithTx(tx).PurgeEntity(ctx, tenant, "t1", "e1")
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 2)
			So(tx.Commit(ctx), ShouldBeNil)

			Convey("Then the revisions are gone", func() {
				revs, err := revStore.List(ctx, tenant, "t1", "e1")
				So(err, ShouldBeNil)
				So(revs, ShouldBeEmpty)
			})
		})
	})
}
