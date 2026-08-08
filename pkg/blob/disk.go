package blob

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// diskStore is a local-filesystem Store. Each object is a file under root;
// its MIME type is written to a sibling ".mime" file. It is intended for
// single-node deployments and development; the hosted tier uses object
// storage behind the same interface.
type diskStore struct {
	root string
}

// NewDiskStore builds a disk-backed blob store rooted at dir, creating it if
// necessary.
//
// It also PROVES the root is writable, by creating a temporary file under it
// and removing it again. MkdirAll succeeds for a directory that already
// exists whatever its ownership, so a root the process cannot write to used to
// build a store that reported itself ready and failed on the first upload —
// which is a container-orchestrated deployment's normal shape: a named volume
// mounts owned by root, and the image runs as a non-root user. The service
// then came up healthy and lost the first image somebody uploaded, hours
// later. A configuration error must stop the service at start-up instead.
func NewDiskStore(dir string) (Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create blob root %q: %w", dir, err)
	}
	probe, err := os.CreateTemp(dir, ".writable-*")
	if err != nil {
		return nil, fmt.Errorf(
			"blob root %q is not writable by this process (uid %d): %w",
			dir, os.Getuid(), err)
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return nil, fmt.Errorf("blob root %q is not writable: %w", dir, err)
	}
	if err := os.Remove(name); err != nil {
		return nil, fmt.Errorf("blob root %q does not allow a file to be removed: %w", dir, err)
	}
	return &diskStore{root: dir}, nil
}

// path resolves a key to a file path under root, rejecting traversal.
func (s *diskStore) path(key string) (string, error) {
	clean := filepath.Clean("/" + key) // anchor so ".." cannot escape
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	p := filepath.Join(s.root, clean)
	if rel, err := filepath.Rel(s.root, p); err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	return p, nil
}

func (s *diskStore) Put(_ context.Context, key string, r io.Reader, mime string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("create object dir: %w", err)
	}
	f, err := os.Create(p)
	if err != nil {
		return fmt.Errorf("create object: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return fmt.Errorf("write object: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close object: %w", err)
	}
	if err := os.WriteFile(p+".mime", []byte(mime), 0o644); err != nil {
		return fmt.Errorf("write object mime: %w", err)
	}
	return nil
}

func (s *diskStore) Open(_ context.Context, key string) (io.ReadCloser, string, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, "", fmt.Errorf("open object: %w", err)
	}
	mime, _ := os.ReadFile(p + ".mime")
	return f, string(mime), nil
}

func (s *diskStore) Delete(_ context.Context, key string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete object: %w", err)
	}
	if err := os.Remove(p + ".mime"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete object mime: %w", err)
	}
	return nil
}
