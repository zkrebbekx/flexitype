// Package blob is the object-storage port for media attribute values. The
// bytes of a file/image live behind this interface; the attribute value
// itself only carries metadata (object key, mime, size). Implementations
// include local disk (below) and, for the hosted tier, an S3-compatible
// store — either satisfies the same contract.
package blob

import (
	"context"
	"io"
)

// Store persists opaque objects keyed by a string. Keys are chosen by the
// caller and must be treated as untrusted paths by implementations.
type Store interface {
	// Put stores an object's bytes and its MIME type under key, overwriting
	// any existing object.
	Put(ctx context.Context, key string, r io.Reader, mime string) error
	// Open returns a reader over the object and its stored MIME type.
	Open(ctx context.Context, key string) (io.ReadCloser, string, error)
	// Delete removes the object; deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
}
