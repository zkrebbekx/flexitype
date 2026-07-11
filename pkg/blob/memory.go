package blob

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
)

// memStore is an in-memory Store for the playground and tests. Objects live
// only for the process lifetime.
type memStore struct {
	mu      sync.RWMutex
	objects map[string]memObject
}

type memObject struct {
	data []byte
	mime string
}

// NewMemoryStore builds an in-memory blob store.
func NewMemoryStore() Store {
	return &memStore{objects: map[string]memObject{}}
}

func (s *memStore) Put(_ context.Context, key string, r io.Reader, mime string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read object: %w", err)
	}
	s.mu.Lock()
	s.objects[key] = memObject{data: data, mime: mime}
	s.mu.Unlock()
	return nil
}

func (s *memStore) Open(_ context.Context, key string) (io.ReadCloser, string, error) {
	s.mu.RLock()
	obj, ok := s.objects[key]
	s.mu.RUnlock()
	if !ok {
		return nil, "", fmt.Errorf("object %q not found", key)
	}
	return io.NopCloser(bytes.NewReader(obj.data)), obj.mime, nil
}

func (s *memStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.objects, key)
	s.mu.Unlock()
	return nil
}
