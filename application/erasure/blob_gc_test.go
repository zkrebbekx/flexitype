package erasure

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// countingValues answers the reference count, or fails.
type countingValues struct {
	domainvalue.Repository
	refs int
	err  error
}

func (c countingValues) MediaKeyRefCount(context.Context, string, valueobjects.AttributeValueID) (int, error) {
	return c.refs, c.err
}

// recordingBlobs records the keys it was asked to delete.
type recordingBlobs struct {
	deleted []string
	err     error
}

func (b *recordingBlobs) Delete(_ context.Context, key string) error {
	b.deleted = append(b.deleted, key)
	return b.err
}

// hookTx captures the post-commit hook so a test can run it directly.
type hookTx struct {
	db.Transactor
	hooks []db.Hook
}

func (t *hookTx) OnPostCommit(h db.Hook) { t.hooks = append(t.hooks, h) }

func (t *hookTx) run(ctx context.Context) error {
	for _, h := range t.hooks {
		if err := h(ctx); err != nil {
			return err
		}
	}
	return nil
}

// TestBlobGCCountsReferences covers the reference count the erasure GC takes
// before it deletes a blob.
//
// The write path counted references; this path did not. Adoption is the
// sanctioned way to reuse an object key, so purging one entity deleted a
// different entity's live document and left it a media value whose bytes
// 404. A key the count cannot be taken for is reported as unpurged rather
// than deleted on a guess.
func TestBlobGCCountsReferences(t *testing.T) {
	ctx := context.Background()

	Convey("Given purged media keys and a blob store", t, func() {
		blobs := &recordingBlobs{}

		Convey("When no other value references the keys", func() {
			i := &Interactor{blobs: blobs, values: countingValues{refs: 0}}
			tx := &hookTx{}
			report := &PurgeReport{}
			i.gcErasedBlobs(tx, []string{"k1", "", "k2"}, report)
			So(tx.run(ctx), ShouldBeNil)

			Convey("Then both blobs are deleted and counted", func() {
				So(blobs.deleted, ShouldResemble, []string{"k1", "k2"})
				So(report.MediaBlobsPurged, ShouldEqual, 2)
				So(report.RetainedBlobKeys, ShouldBeEmpty)
			})
		})

		Convey("When another value still references the key", func() {
			i := &Interactor{blobs: blobs, values: countingValues{refs: 1}}
			tx := &hookTx{}
			report := &PurgeReport{}
			i.gcErasedBlobs(tx, []string{"k1"}, report)
			So(tx.run(ctx), ShouldBeNil)

			Convey("Then the blob survives and is reported as retained", func() {
				So(blobs.deleted, ShouldBeEmpty)
				So(report.MediaBlobsPurged, ShouldEqual, 0)
				So(report.RetainedBlobKeys, ShouldResemble, []string{"k1"})
			})
		})

		Convey("When the reference count cannot be taken", func() {
			boom := errors.New("connection reset")
			var seen []error
			i := &Interactor{
				blobs: blobs, values: countingValues{err: boom},
				onCleanupError: func(err error) { seen = append(seen, err) },
			}
			tx := &hookTx{}
			report := &PurgeReport{}
			i.gcErasedBlobs(tx, []string{"k1"}, report)
			So(tx.run(ctx), ShouldBeNil)

			Convey("Then the blob is left alone and the failure is reported", func() {
				So(blobs.deleted, ShouldBeEmpty)
				So(report.MediaBlobsFailed, ShouldEqual, 1)
				So(report.UnpurgedBlobKeys, ShouldResemble, []string{"k1"})
				So(len(seen), ShouldEqual, 1)
				So(errors.Is(seen[0], boom), ShouldBeTrue)
			})
		})

		Convey("When there is no blob store or no keys", func() {
			i := &Interactor{values: countingValues{}}
			tx := &hookTx{}
			i.gcErasedBlobs(tx, []string{"k1"}, &PurgeReport{})
			withStore := &Interactor{blobs: blobs, values: countingValues{}}
			withStore.gcErasedBlobs(tx, nil, &PurgeReport{})

			Convey("Then nothing is registered", func() {
				So(tx.hooks, ShouldBeEmpty)
			})
		})
	})
}
