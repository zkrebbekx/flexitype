package flexitype_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apprevision "github.com/zkrebbekx/flexitype/application/revision"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/blob"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// failingBlobStore stores objects normally but fails every Delete, so an
// erasure's post-commit blob GC cannot complete. It proves the Postgres-backed
// erasure reports the residual data honestly rather than a false success.
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

// countRows is a tiny helper to assert against the physical tables, so the
// test proves the rows are truly DELETEd (not merely archived or filtered).
func countRows(t *testing.T, pool *sqlx.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.Get(&n, query, args...); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestPurgeEntityPostgres re-runs the per-entity erasure over the real Postgres
// repositories and asserts every backing table row is physically gone.
func TestPurgeEntityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	store := blob.NewMemoryStore()
	svc := flexitype.New(pool, flexitype.WithBlobStore(store), flexitype.WithSearchIndex())
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given e1 with values, a media blob, an archived value, a revision and a link in Postgres", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-a"))
		it := svc.Interactors(ctx)

		typeDef, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := typeDef.ID.String()

		name, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "name", DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)
		sku, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "sku", DisplayName: "SKU", DataType: "string",
		})
		So(err, ShouldBeNil)
		image, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "image", DisplayName: "Image", DataType: "media",
		})
		So(err, ShouldBeNil)

		_, err = it.Values().Set(ctx, appvalue.SetInput{AttributeDefinitionID: name.ID.String(), EntityID: "e1", Value: json.RawMessage(`"Widget"`)})
		So(err, ShouldBeNil)
		png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), 50)...)
		mediaSnap, err := it.Values().UploadMedia(ctx, typeID, "e1", image.ID.String(), bytes.NewReader(png), "logo.png")
		So(err, ShouldBeNil)
		blobKey := mediaSnap.Value.Media().ObjectKey

		skuSnap, err := it.Values().Set(ctx, appvalue.SetInput{AttributeDefinitionID: sku.ID.String(), EntityID: "e1", Value: json.RawMessage(`"SKU-1"`)})
		So(err, ShouldBeNil)
		_, err = it.Values().Remove(ctx, skuSnap.ID.String()) // archive: an archived row stays until purge
		So(err, ShouldBeNil)

		_, err = it.Revisions().Create(ctx, typeID, "e1", "v1")
		So(err, ShouldBeNil)

		relDef, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "related", DisplayName: "Related", ParentTypeID: typeID, ChildTypeID: typeID,
		})
		So(err, ShouldBeNil)
		_, err = it.Relationships().Link(ctx, apprelationship.LinkInput{DefinitionID: relDef.ID.String(), ParentEntity: "e1", ChildEntity: "e2"})
		So(err, ShouldBeNil)

		_, err = it.Values().Set(ctx, appvalue.SetInput{AttributeDefinitionID: name.ID.String(), EntityID: "e2", Value: json.RawMessage(`"Gadget"`)})
		So(err, ShouldBeNil)

		// Sanity: rows exist before the purge (archived row included).
		So(countRows(t, pool, `SELECT count(*) FROM flexitype_attribute_value WHERE tenant_id='tenant-a' AND entity_id='e1'`), ShouldEqual, 3)

		Convey("When e1 is purged", func() {
			report, err := it.Values().PurgeEntity(ctx, typeID, "e1")
			So(err, ShouldBeNil)
			So(report.ValuesPurged, ShouldEqual, 3)
			So(report.RevisionsPurged, ShouldEqual, 1)
			So(report.RelationshipsGone, ShouldEqual, 1)
			So(report.MediaBlobsPurged, ShouldEqual, 1)

			Convey("Then every backing table row for e1 is physically gone", func() {
				So(countRows(t, pool, `SELECT count(*) FROM flexitype_attribute_value WHERE tenant_id='tenant-a' AND entity_id='e1'`), ShouldEqual, 0)
				So(countRows(t, pool, `SELECT count(*) FROM flexitype_entity_revision WHERE tenant_id='tenant-a' AND entity_id='e1'`), ShouldEqual, 0)
				So(countRows(t, pool, `SELECT count(*) FROM flexitype_relationship WHERE tenant_id='tenant-a' AND (parent_entity_id='e1' OR child_entity_id='e1')`), ShouldEqual, 0)
			})

			Convey("Then the media blob is deleted from the store", func() {
				_, _, err := store.Open(ctx, blobKey)
				So(err, ShouldNotBeNil)
			})

			Convey("Then e2's value survives", func() {
				So(countRows(t, pool, `SELECT count(*) FROM flexitype_attribute_value WHERE tenant_id='tenant-a' AND entity_id='e2'`), ShouldEqual, 1)
			})

			Convey("Then the erasure is recorded in the audit log", func() {
				So(countRows(t, pool, `SELECT count(*) FROM flexitype_activity_log WHERE tenant_id='tenant-a' AND action='purged'`), ShouldBeGreaterThanOrEqualTo, 1)
			})
		})
	})
}

// TestPurgeTenantPostgres proves the per-tenant erasure removes one tenant's
// entity data from the physical tables while another tenant's rows survive.
func TestPurgeTenantPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool, flexitype.WithSearchIndex())
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given tenant A and tenant B each with entity data in Postgres", t, func() {
		truncateAll(t, pool)
		ctxA := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-a"))
		ctxB := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-b"))
		a := svc.Interactors(ctxA)
		b := svc.Interactors(ctxB)

		typeA, err := a.TypeDefinitions().Create(ctxA, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		nameA, err := a.Attributes().Create(ctxA, appattribute.CreateInput{
			TypeDefinitionID: typeA.ID.String(), InternalName: "name", DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = a.Values().Set(ctxA, appvalue.SetInput{AttributeDefinitionID: nameA.ID.String(), EntityID: "a1", Value: json.RawMessage(`"A"`)})
		So(err, ShouldBeNil)
		_, err = a.Revisions().Create(ctxA, typeA.ID.String(), "a1", "v1")
		So(err, ShouldBeNil)

		typeB, err := b.TypeDefinitions().Create(ctxB, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		nameB, err := b.Attributes().Create(ctxB, appattribute.CreateInput{
			TypeDefinitionID: typeB.ID.String(), InternalName: "name", DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = b.Values().Set(ctxB, appvalue.SetInput{AttributeDefinitionID: nameB.ID.String(), EntityID: "b1", Value: json.RawMessage(`"B"`)})
		So(err, ShouldBeNil)

		Convey("When tenant A's entity data is purged", func() {
			_, err := a.Values().PurgeTenant(ctxA)
			So(err, ShouldBeNil)

			Convey("Then A's entity rows are gone and A's schema survives", func() {
				So(countRows(t, pool, `SELECT count(*) FROM flexitype_attribute_value WHERE tenant_id='tenant-a'`), ShouldEqual, 0)
				So(countRows(t, pool, `SELECT count(*) FROM flexitype_entity_revision WHERE tenant_id='tenant-a'`), ShouldEqual, 0)
				So(countRows(t, pool, `SELECT count(*) FROM flexitype_type_definition WHERE tenant_id='tenant-a'`), ShouldBeGreaterThanOrEqualTo, 1)
			})

			Convey("Then tenant B's rows survive", func() {
				So(countRows(t, pool, `SELECT count(*) FROM flexitype_attribute_value WHERE tenant_id='tenant-b'`), ShouldEqual, 1)
			})
		})
	})
}

// TestPurgeEntityReportsBlobFailuresPostgres is the Postgres twin of the memory
// blob-failure test: an erasure whose backing-blob deletions fail reports
// MediaBlobsFailed and the unpurged keys — not a false success — and surfaces
// the failure to the cleanup observer.
func TestPurgeEntityReportsBlobFailuresPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	blobs := failingBlobStore{inner: blob.NewMemoryStore()}
	var observed []error
	svc := flexitype.New(pool,
		flexitype.WithBlobStore(blobs),
		flexitype.WithSearchIndex(),
		flexitype.WithCleanupObserver(func(err error) { observed = append(observed, err) }),
	)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a Postgres entity with a media blob and a blob store whose deletes fail", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-a"))
		it := svc.Interactors(ctx)

		typeDef, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := typeDef.ID.String()
		image, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "image", DisplayName: "Image", DataType: "media",
		})
		So(err, ShouldBeNil)
		png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), 50)...)
		mediaSnap, err := it.Values().UploadMedia(ctx, typeID, "e1", image.ID.String(), bytes.NewReader(png), "logo.png")
		So(err, ShouldBeNil)
		blobKey := mediaSnap.Value.Media().ObjectKey

		Convey("When e1 is purged", func() {
			report, err := it.Values().PurgeEntity(ctx, typeID, "e1")
			So(err, ShouldBeNil) // erasure commits; blob GC is post-commit best effort

			Convey("Then the report tells the truth about the failed blob delete", func() {
				So(report.ValuesPurged, ShouldEqual, 1)
				So(report.MediaBlobsPurged, ShouldEqual, 0)
				So(report.MediaBlobsFailed, ShouldEqual, 1)
				So(report.UnpurgedBlobKeys, ShouldContain, blobKey)
			})

			Convey("Then the values are still physically gone (the erasure committed)", func() {
				So(countRows(t, pool, `SELECT count(*) FROM flexitype_attribute_value WHERE tenant_id='tenant-a' AND entity_id='e1'`), ShouldEqual, 0)
			})

			Convey("Then the cleanup failure is surfaced to the observer", func() {
				So(observed, ShouldNotBeEmpty)
			})
		})
	})
}

// TestRevisionPurgeJoinsTransactionPostgres is the Postgres parity twin of the
// memory revision-tx test: the revision DELETE runs inside the value write's
// transaction (via WithTx), so a rollback leaves the revisions intact and a
// commit removes them.
func TestRevisionPurgeJoinsTransactionPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given two revisions of an entity in Postgres", t, func() {
		truncateAll(t, pool)
		ctx := context.Background()
		tenant := valueobjects.TenantID("tenant-a")
		revStore := postgres.NewRevisionStore(pool)
		transactor := db.NewTransactor(pool)
		mk := func(seq int) apprevision.Revision {
			return apprevision.Revision{
				ID: ulid.New(), TenantID: tenant, TypeDefinitionID: "t1", EntityID: "e1",
				Seq: seq, CreatedAt: time.Now().UTC(), Values: []apprevision.Value{},
			}
		}
		So(revStore.Create(ctx, mk(1)), ShouldBeNil)
		So(revStore.Create(ctx, mk(2)), ShouldBeNil)
		So(countRows(t, pool, `SELECT count(*) FROM flexitype_entity_revision WHERE tenant_id='tenant-a' AND entity_id='e1'`), ShouldEqual, 2)

		Convey("When they are purged inside a transaction that rolls back", func() {
			tx, err := transactor.Begin(ctx)
			So(err, ShouldBeNil)
			n, err := revStore.WithTx(tx).PurgeEntity(ctx, tenant, "t1", "e1")
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 2)
			So(tx.Rollback(ctx), ShouldBeNil)

			Convey("Then the revision rows survive — the DELETE joined the rolled-back tx", func() {
				So(countRows(t, pool, `SELECT count(*) FROM flexitype_entity_revision WHERE tenant_id='tenant-a' AND entity_id='e1'`), ShouldEqual, 2)
			})
		})

		Convey("When they are purged inside a transaction that commits", func() {
			tx, err := transactor.Begin(ctx)
			So(err, ShouldBeNil)
			n, err := revStore.WithTx(tx).PurgeEntity(ctx, tenant, "t1", "e1")
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 2)
			So(tx.Commit(ctx), ShouldBeNil)

			Convey("Then the revision rows are gone", func() {
				So(countRows(t, pool, `SELECT count(*) FROM flexitype_entity_revision WHERE tenant_id='tenant-a' AND entity_id='e1'`), ShouldEqual, 0)
			})
		})
	})
}
