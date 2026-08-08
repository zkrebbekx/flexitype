package erasure

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// countingValues answers the reference count, or fails. It records the batch
// queries and the per-key locks it saw, so a test can assert the GC takes ONE
// batched count per chunk and holds the per-key lock around a delete.
type countingValues struct {
	domainvalue.Repository
	refs       int
	err        error
	batchCalls [][]string
	locked     []string
}

func (c *countingValues) WithTx(db.Tx) domainvalue.Repository { return c }

func (c *countingValues) MediaKeyRefCount(context.Context, string, valueobjects.AttributeValueID) (int, error) {
	return c.refs, c.err
}

func (c *countingValues) MediaKeyRefCounts(_ context.Context, keys []string) (map[string]int, error) {
	c.batchCalls = append(c.batchCalls, append([]string(nil), keys...))
	if c.err != nil {
		return nil, c.err
	}
	out := make(map[string]int, len(keys))
	for _, k := range keys {
		if c.refs > 0 {
			out[k] = c.refs
		}
	}
	return out, nil
}

func (c *countingValues) LockMediaKey(_ context.Context, key string) error {
	c.locked = append(c.locked, key)
	return nil
}

// immediateTransactor runs the transaction body directly; it stands in for the
// short locked GC transaction.
type immediateTransactor struct{ db.Transactor }

func (t immediateTransactor) InTransaction(_ context.Context, fn func(db.Transactor) error) error {
	return fn(t)
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
			values := &countingValues{refs: 0}
			i := &Interactor{blobs: blobs, transactor: immediateTransactor{}, values: values}
			tx := &hookTx{}
			report := &PurgeReport{}
			i.gcErasedBlobs(tx, []string{"k1", "", "k2"}, report)
			So(tx.run(ctx), ShouldBeNil)

			Convey("Then both blobs are deleted and counted", func() {
				So(blobs.deleted, ShouldResemble, []string{"k1", "k2"})
				So(report.MediaBlobsPurged, ShouldEqual, 2)
				So(report.RetainedBlobKeys, ShouldBeEmpty)
			})

			Convey("Then the chunk took one batched count, not one per key", func() {
				So(len(values.batchCalls), ShouldEqual, 1)
				So(values.batchCalls[0], ShouldResemble, []string{"k1", "k2"})
			})

			Convey("Then each delete ran under its per-key lock", func() {
				So(values.locked, ShouldResemble, []string{"k1", "k2"})
			})
		})

		Convey("When another value still references the key", func() {
			i := &Interactor{blobs: blobs, transactor: immediateTransactor{}, values: &countingValues{refs: 1}}
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

		Convey("When an adoption commits between the batch count and the locked recount", func() {
			// The batch says zero references, but the locked recount inside
			// gcBlobKey sees one: an adoption committed in between. The blob
			// must be retained on the LOCKED count, not deleted on the stale
			// batch count.
			values := &raceValues{countingValues: countingValues{refs: 0}, lockedRefs: 1}
			i := &Interactor{blobs: blobs, transactor: immediateTransactor{}, values: values}
			tx := &hookTx{}
			report := &PurgeReport{}
			i.gcErasedBlobs(tx, []string{"k1"}, report)
			So(tx.run(ctx), ShouldBeNil)

			Convey("Then the blob survives on the authoritative locked recount", func() {
				So(blobs.deleted, ShouldBeEmpty)
				So(report.MediaBlobsPurged, ShouldEqual, 0)
				So(report.RetainedBlobKeys, ShouldResemble, []string{"k1"})
			})
		})

		Convey("When the reference count cannot be taken", func() {
			boom := errors.New("connection reset")
			var seen []error
			i := &Interactor{
				blobs: blobs, transactor: immediateTransactor{}, values: &countingValues{err: boom},
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

		Convey("When there is no blob store, no transactor, or no keys", func() {
			i := &Interactor{values: &countingValues{}, transactor: immediateTransactor{}}
			tx := &hookTx{}
			i.gcErasedBlobs(tx, []string{"k1"}, &PurgeReport{})
			noTransactor := &Interactor{blobs: blobs, values: &countingValues{}}
			noTransactor.gcErasedBlobs(tx, []string{"k1"}, &PurgeReport{})
			withStore := &Interactor{blobs: blobs, transactor: immediateTransactor{}, values: &countingValues{}}
			withStore.gcErasedBlobs(tx, nil, &PurgeReport{})

			Convey("Then nothing is registered", func() {
				So(tx.hooks, ShouldBeEmpty)
			})
		})

		Convey("When the purge holds more keys than one chunk", func() {
			values := &countingValues{refs: 0}
			i := &Interactor{blobs: blobs, transactor: immediateTransactor{}, values: values}
			tx := &hookTx{}
			report := &PurgeReport{}
			keys := make([]string, gcChunkSize+1)
			for n := range keys {
				keys[n] = fmt.Sprintf("k%03d", n)
			}
			i.gcErasedBlobs(tx, keys, report)
			So(tx.run(ctx), ShouldBeNil)

			Convey("Then the keys split into chunked batch counts", func() {
				So(len(values.batchCalls), ShouldEqual, 2)
				So(len(values.batchCalls[0]), ShouldEqual, gcChunkSize)
				So(len(values.batchCalls[1]), ShouldEqual, 1)
				So(report.MediaBlobsPurged, ShouldEqual, gcChunkSize+1)
			})
		})
	})
}

// raceValues reports zero references from the batch count but a positive count
// from the locked per-key recount, simulating an adoption that commits between
// the two.
type raceValues struct {
	countingValues
	lockedRefs int
}

func (r *raceValues) WithTx(db.Tx) domainvalue.Repository { return r }

func (r *raceValues) MediaKeyRefCount(context.Context, string, valueobjects.AttributeValueID) (int, error) {
	return r.lockedRefs, nil
}
