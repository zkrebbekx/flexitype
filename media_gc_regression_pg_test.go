package flexitype_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/blob"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// This file holds the media-blob GC regression tests for issues #483, #484 and
// #485. Each test reproduces one defect through the public service surface (or
// raw SQL), so the whole file compiles and FAILS on the pre-fix tree.

// mediaGCSetup creates one media attribute and returns its type and attribute
// ids.
func mediaGCSetup(ctx context.Context, svc *flexitype.Service) (typeID, attrID string) {
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
	return doc.ID.String(), file.ID.String()
}

// adoptMediaKey writes a media value by object key, the way an API client
// adopts an already-uploaded file.
func adoptMediaKey(ctx context.Context, svc *flexitype.Service, typeID, attrID, entity, key string) error {
	raw, _ := json.Marshal(map[string]any{
		"object_key": key, "mime": "text/plain", "size": 1,
	})
	_, err := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
		AttributeDefinitionID: attrID, EntityID: entity,
		TypeDefinitionID: typeID, Value: raw,
	})
	return err
}

// TestMediaArchivedKeyAdoptionPostgres is the regression test for issue #485.
//
// MediaValueForKey resolves through archived rows (ownership survives
// archival), but blob GC counts only live rows. So a Remove of the last live
// value deletes the blob, and a later Set that adopts the same key succeeded
// through the archived row — it minted a live media value whose bytes were
// already gone, and every download of it 404ed. The write must fail instead.
func TestMediaArchivedKeyAdoptionPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()
	blobs := blob.NewMemoryStore()
	svc := flexitype.New(pool, flexitype.WithBlobStore(blobs))
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given an uploaded file whose only value was removed (Postgres)", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-a"))
		typeID, attrID := mediaGCSetup(ctx, svc)

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
			adoptErr := adoptMediaKey(ctx, svc, typeID, attrID, "e2", key)

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

// TestMediaAdoptionGCRacePostgres is the regression test for issue #484.
//
// An adoption in flight inserts its value row and commits later. Blob GC ran
// its reference count post-commit under READ COMMITTED with no lock, so the
// uncommitted adoption was invisible: GC counted zero, deleted the bytes, and
// the adoption then committed a live value whose download 404s.
//
// The test makes the race deterministic. It plays the in-flight adoption by
// hand in an open transaction: it inserts the adopted row and takes the same
// per-key advisory lock the adoption path takes, and holds both across the
// concurrent Remove. The Remove's post-commit GC must wait on that lock,
// recount after the adoption commits, and retain the blob.
func TestMediaAdoptionGCRacePostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()
	blobs := blob.NewMemoryStore()
	svc := flexitype.New(pool, flexitype.WithBlobStore(blobs))
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given one live value for a key and an adoption in flight (Postgres)", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-a"))
		typeID, attrID := mediaGCSetup(ctx, svc)

		snap, err := svc.Interactors(ctx).Values().UploadMedia(ctx, typeID, "e1", attrID,
			strings.NewReader("bytes"), "f.txt")
		So(err, ShouldBeNil)
		key := snap.Value.Media().ObjectKey

		// The in-flight adoption: an open transaction that inserted its row
		// and holds the per-key lock until commit, exactly as adoptMediaValue
		// does.
		transactor := db.NewTransactor(pool)
		adoptTx, err := transactor.Begin(ctx)
		So(err, ShouldBeNil)
		exec, ok := adoptTx.(db.QueryExecer)
		So(ok, ShouldBeTrue)
		meta, _ := json.Marshal(map[string]any{"object_key": key, "mime": "text/plain", "size": 5})
		_, err = exec.ExecContext(ctx,
			`INSERT INTO flexitype_attribute_value
			   (id, tenant_id, type_definition_id, attribute_definition_id, entity_id,
			    data_type, value_json, definition_version, created_at, updated_at)
			 VALUES ($1, 'tenant-a', $2, $3, 'e2', 'media', $4, 1, now(), now())`,
			ulid.New().String(), typeID, attrID, meta)
		So(err, ShouldBeNil)
		_, err = exec.ExecContext(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended('flexitype:media-key:' || $1, 0))`, key)
		So(err, ShouldBeNil)

		Convey("When the original value is removed while the adoption holds the lock", func() {
			removed := make(chan error, 1)
			go func() {
				_, rerr := svc.Interactors(ctx).Values().Remove(ctx, snap.ID.String())
				removed <- rerr
			}()

			// Post-fix, the Remove's blob GC blocks on the advisory lock, so
			// the channel stays empty; pre-fix the Remove returns at once,
			// having deleted the bytes under the adoption.
			var removeErr error
			removeDone := false
			select {
			case removeErr = <-removed:
				removeDone = true
			case <-time.After(500 * time.Millisecond):
			}

			So(adoptTx.Commit(ctx), ShouldBeNil)
			if !removeDone {
				removeErr = <-removed
			}
			So(removeErr, ShouldBeNil)

			Convey("Then the bytes survive for the committed adoption", func() {
				rc, _, oerr := blobs.Open(context.Background(), key)
				So(oerr, ShouldBeNil)
				So(rc.Close(), ShouldBeNil)
			})
		})
	})
}

// TestMediaGCIndexPostgres is the regression test for issue #483.
//
// The blob-GC reference count is cross-tenant (blob keys live in one shared
// namespace), but the only object-key index led with tenant_id, so the count
// could not seek: every media overwrite or remove paid a full scan of the
// media rows, and a large erasure spent its whole post-commit budget on
// serial scans and reported the remaining bytes unpurged. Migration 000033
// adds the cross-tenant partial expression index the count seeks on.
func TestMediaGCIndexPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()
	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a migrated database (Postgres)", t, func() {
		Convey("Then the cross-tenant media-key index exists and is valid", func() {
			var valid bool
			err := pool.QueryRowContext(context.Background(),
				`SELECT i.indisvalid FROM pg_class c JOIN pg_index i ON i.indexrelid = c.oid
				  WHERE c.relname = 'idx_flexitype_attribute_value_media_key_live'`).Scan(&valid)
			So(err, ShouldBeNil)
			So(valid, ShouldBeTrue)
		})

		Convey("Then the index is partial on live media rows", func() {
			var def string
			err := pool.QueryRowContext(context.Background(),
				`SELECT indexdef FROM pg_indexes
				  WHERE indexname = 'idx_flexitype_attribute_value_media_key_live'`).Scan(&def)
			So(err, ShouldBeNil)
			So(def, ShouldContainSubstring, "object_key")
			So(def, ShouldContainSubstring, "archived_at IS NULL")
		})
	})
}
