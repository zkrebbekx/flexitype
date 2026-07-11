package value

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// maxMediaBytes caps how much of an upload the interactor buffers to compute
// its size and checksum. The HTTP layer enforces its own request limit.
const maxMediaBytes = 64 << 20 // 64 MiB

// UploadMedia stores an uploaded file for a media attribute: it puts the
// bytes in object storage, then writes a media value holding only the
// metadata (object key, mime, size, checksum, filename). The value write
// runs the attribute's media constraint (allowed types, max size); if it is
// rejected the freshly stored object is deleted so nothing is orphaned.
func (i *Interactor) UploadMedia(ctx context.Context, rawTypeID, entityID, rawAttrID string, r io.Reader, filename, declaredMIME string) (*domainvalue.Snapshot, error) {
	if i.blobs == nil {
		return nil, domainerrors.NewValidation("media storage is not configured in this deployment")
	}
	attrID, err := valueobjects.ParseAttributeDefinitionID(rawAttrID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	def, err := i.attrs.Get(ctx, attrID)
	if err != nil {
		return nil, err
	}
	if err := uow.EnsureTenant(ctx, def.TenantID(), "attribute_definition", rawAttrID); err != nil {
		return nil, err
	}
	if def.DataType() != valueobjects.DataTypeMedia {
		return nil, domainerrors.NewValidation("attribute is not a media attribute", "attribute", def.InternalName())
	}

	buf, err := io.ReadAll(io.LimitReader(r, maxMediaBytes+1))
	if err != nil {
		return nil, domainerrors.NewValidation("read upload: " + err.Error())
	}
	if len(buf) > maxMediaBytes {
		return nil, domainerrors.NewValidation("upload exceeds the maximum size", "max_bytes", maxMediaBytes)
	}

	mime := declaredMIME
	if mime == "" || mime == "application/octet-stream" {
		mime = http.DetectContentType(buf)
	}
	if idx := strings.IndexByte(mime, ';'); idx >= 0 { // strip "; charset=..."
		mime = strings.TrimSpace(mime[:idx])
	}
	sum := sha256.Sum256(buf)
	key := ulid.New().String() + strings.ToLower(filepath.Ext(filename))

	if err := i.blobs.Put(ctx, key, bytes.NewReader(buf), mime); err != nil {
		return nil, err
	}

	meta := valueobjects.MediaMeta{
		ObjectKey: key,
		MIME:      mime,
		Size:      int64(len(buf)),
		Checksum:  "sha256:" + hex.EncodeToString(sum[:]),
		Filename:  filename,
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		_ = i.blobs.Delete(ctx, key)
		return nil, err
	}
	snap, err := i.Set(ctx, SetInput{
		AttributeDefinitionID: rawAttrID,
		EntityID:              entityID,
		TypeDefinitionID:      rawTypeID,
		Value:                 raw,
	})
	if err != nil {
		_ = i.blobs.Delete(ctx, key) // constraint rejected the value; don't orphan the blob
		return nil, err
	}
	return snap, nil
}
