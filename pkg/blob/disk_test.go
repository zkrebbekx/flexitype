package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// errReader fails partway through so Put's io.Copy error path is exercised.
type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestDiskStore(t *testing.T) {
	ctx := context.Background()

	Convey("Given a disk store rooted at a fresh directory", t, func() {
		root := filepath.Join(t.TempDir(), "blobs", "nested")
		store, err := NewDiskStore(root)
		So(err, ShouldBeNil)
		So(store, ShouldNotBeNil)

		Convey("When the root does not yet exist", func() {
			Convey("Then NewDiskStore creates it", func() {
				info, statErr := os.Stat(root)
				So(statErr, ShouldBeNil)
				So(info.IsDir(), ShouldBeTrue)
			})
		})

		Convey("When an object is put under a nested key", func() {
			putErr := store.Put(ctx, "tenant-a/2026/photo.png", strings.NewReader("PNGDATA"), "image/png")
			So(putErr, ShouldBeNil)

			Convey("Then the bytes and mime land on disk under the root", func() {
				data, readErr := os.ReadFile(filepath.Join(root, "tenant-a", "2026", "photo.png"))
				So(readErr, ShouldBeNil)
				So(string(data), ShouldEqual, "PNGDATA")

				mime, mimeErr := os.ReadFile(filepath.Join(root, "tenant-a", "2026", "photo.png.mime"))
				So(mimeErr, ShouldBeNil)
				So(string(mime), ShouldEqual, "image/png")
			})

			Convey("Then Open round-trips the bytes and the mime type", func() {
				rc, mime, openErr := store.Open(ctx, "tenant-a/2026/photo.png")
				So(openErr, ShouldBeNil)
				So(mime, ShouldEqual, "image/png")

				body, copyErr := io.ReadAll(rc)
				So(copyErr, ShouldBeNil)
				So(rc.Close(), ShouldBeNil)
				So(string(body), ShouldEqual, "PNGDATA")
			})

			Convey("Then a second Put overwrites both bytes and mime", func() {
				So(store.Put(ctx, "tenant-a/2026/photo.png", strings.NewReader("JPG"), "image/jpeg"), ShouldBeNil)

				rc, mime, openErr := store.Open(ctx, "tenant-a/2026/photo.png")
				So(openErr, ShouldBeNil)
				body, _ := io.ReadAll(rc)
				So(rc.Close(), ShouldBeNil)
				So(string(body), ShouldEqual, "JPG")
				So(mime, ShouldEqual, "image/jpeg")
			})

			Convey("Then Delete removes the object and its mime sidecar", func() {
				So(store.Delete(ctx, "tenant-a/2026/photo.png"), ShouldBeNil)

				_, statErr := os.Stat(filepath.Join(root, "tenant-a", "2026", "photo.png"))
				So(os.IsNotExist(statErr), ShouldBeTrue)
				_, mimeStatErr := os.Stat(filepath.Join(root, "tenant-a", "2026", "photo.png.mime"))
				So(os.IsNotExist(mimeStatErr), ShouldBeTrue)

				_, _, reopenErr := store.Open(ctx, "tenant-a/2026/photo.png")
				So(reopenErr, ShouldNotBeNil)
				So(reopenErr.Error(), ShouldContainSubstring, "open object")
			})
		})

		Convey("When a key is opened that was never written", func() {
			rc, mime, openErr := store.Open(ctx, "missing/key")

			Convey("Then Open reports the wrapped filesystem error", func() {
				So(rc, ShouldBeNil)
				So(mime, ShouldEqual, "")
				So(openErr, ShouldNotBeNil)
				So(errors.Is(openErr, fs.ErrNotExist), ShouldBeTrue)
				So(openErr.Error(), ShouldContainSubstring, "open object")
			})
		})

		Convey("When an object was written without a mime sidecar", func() {
			So(store.Put(ctx, "bare", strings.NewReader("x"), "text/plain"), ShouldBeNil)
			So(os.Remove(filepath.Join(root, "bare.mime")), ShouldBeNil)

			Convey("Then Open still returns the bytes with an empty mime", func() {
				rc, mime, openErr := store.Open(ctx, "bare")
				So(openErr, ShouldBeNil)
				So(mime, ShouldEqual, "")
				body, _ := io.ReadAll(rc)
				So(rc.Close(), ShouldBeNil)
				So(string(body), ShouldEqual, "x")
			})
		})

		Convey("When a missing key is deleted", func() {
			Convey("Then Delete is a no-op, not an error", func() {
				So(store.Delete(ctx, "never/existed"), ShouldBeNil)
			})
		})

		Convey("When a key tries to escape the root with ..", func() {
			putErr := store.Put(ctx, "../../../etc/flexitype-pwned", strings.NewReader("owned"), "text/plain")

			Convey("Then the write is anchored inside the root, never above it", func() {
				So(putErr, ShouldBeNil)

				// The traversal is normalised away, so the object lands at
				// <root>/etc/flexitype-pwned and nothing is written outside.
				data, readErr := os.ReadFile(filepath.Join(root, "etc", "flexitype-pwned"))
				So(readErr, ShouldBeNil)
				So(string(data), ShouldEqual, "owned")

				escaped := filepath.Join(filepath.Dir(root), "etc", "flexitype-pwned")
				_, statErr := os.Stat(escaped)
				So(os.IsNotExist(statErr), ShouldBeTrue)
			})
		})

		Convey("When a key retains a literal .. component after cleaning", func() {
			Convey("Then every operation rejects it as an invalid key", func() {
				for _, key := range []string{"v1/..hidden/file", "..secret"} {
					putErr := store.Put(ctx, key, strings.NewReader("x"), "text/plain")
					So(putErr, ShouldNotBeNil)
					So(putErr.Error(), ShouldContainSubstring, "invalid object key")

					_, _, openErr := store.Open(ctx, key)
					So(openErr, ShouldNotBeNil)
					So(openErr.Error(), ShouldContainSubstring, "invalid object key")

					delErr := store.Delete(ctx, key)
					So(delErr, ShouldNotBeNil)
					So(delErr.Error(), ShouldContainSubstring, "invalid object key")
				}
			})
		})

		Convey("When the key resolves to the root directory itself", func() {
			putErr := store.Put(ctx, "", strings.NewReader("x"), "text/plain")

			Convey("Then Put fails because the target is not a regular file", func() {
				So(putErr, ShouldNotBeNil)
				So(putErr.Error(), ShouldContainSubstring, "create object")
			})
		})

		Convey("When an existing object blocks the parent directory of a new key", func() {
			So(store.Put(ctx, "collide", strings.NewReader("x"), "text/plain"), ShouldBeNil)
			putErr := store.Put(ctx, "collide/child", strings.NewReader("y"), "text/plain")

			Convey("Then Put reports the directory-creation failure", func() {
				So(putErr, ShouldNotBeNil)
				So(putErr.Error(), ShouldContainSubstring, "create object dir")
			})
		})

		Convey("When the reader fails mid-copy", func() {
			sentinel := fmt.Errorf("source exploded")
			putErr := store.Put(ctx, "partial", errReader{err: sentinel}, "text/plain")

			Convey("Then Put wraps the reader error", func() {
				So(putErr, ShouldNotBeNil)
				So(putErr.Error(), ShouldContainSubstring, "write object")
				So(putErr.Error(), ShouldContainSubstring, "source exploded")
			})
		})

		Convey("When the mime sidecar path is occupied by a directory", func() {
			// Non-empty so os.Remove cannot succeed by rmdir'ing it either.
			So(os.MkdirAll(filepath.Join(root, "blocked.mime"), 0o755), ShouldBeNil)
			So(os.WriteFile(filepath.Join(root, "blocked.mime", "occupant"), []byte("x"), 0o644), ShouldBeNil)
			putErr := store.Put(ctx, "blocked", strings.NewReader("x"), "text/plain")

			Convey("Then Put reports the mime write failure", func() {
				So(putErr, ShouldNotBeNil)
				So(putErr.Error(), ShouldContainSubstring, "write object mime")
			})

			Convey("Then Delete also reports the mime removal failure", func() {
				delErr := store.Delete(ctx, "blocked")
				So(delErr, ShouldNotBeNil)
				So(delErr.Error(), ShouldContainSubstring, "delete object mime")
			})
		})
	})

	Convey("Given a path already occupied by a regular file", t, func() {
		file := filepath.Join(t.TempDir(), "not-a-dir")
		So(os.WriteFile(file, []byte("x"), 0o644), ShouldBeNil)

		Convey("When a disk store is rooted there", func() {
			store, err := NewDiskStore(file)

			Convey("Then NewDiskStore fails with the root in the message", func() {
				So(store, ShouldBeNil)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "create blob root")
				So(err.Error(), ShouldContainSubstring, file)
			})
		})
	})
}

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()

	Convey("Given an in-memory blob store", t, func() {
		store := NewMemoryStore()

		Convey("When an object is put and read back", func() {
			So(store.Put(ctx, "k", strings.NewReader("hello"), "text/plain"), ShouldBeNil)
			rc, mime, err := store.Open(ctx, "k")

			Convey("Then the bytes and mime round-trip", func() {
				So(err, ShouldBeNil)
				So(mime, ShouldEqual, "text/plain")
				body, readErr := io.ReadAll(rc)
				So(readErr, ShouldBeNil)
				So(rc.Close(), ShouldBeNil)
				So(string(body), ShouldEqual, "hello")
			})
		})

		Convey("When an unknown key is opened", func() {
			_, _, err := store.Open(ctx, "ghost")

			Convey("Then it reports the key as not found", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, `object "ghost" not found`)
			})
		})

		Convey("When the reader fails", func() {
			err := store.Put(ctx, "k", errReader{err: fmt.Errorf("boom")}, "text/plain")

			Convey("Then Put wraps the read error", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "read object")
			})
		})

		Convey("When an object is deleted", func() {
			So(store.Put(ctx, "k", strings.NewReader("hello"), "text/plain"), ShouldBeNil)
			So(store.Delete(ctx, "k"), ShouldBeNil)

			Convey("Then it is gone and deleting again is still a no-op", func() {
				_, _, err := store.Open(ctx, "k")
				So(err, ShouldNotBeNil)
				So(store.Delete(ctx, "k"), ShouldBeNil)
			})
		})
	})
}
