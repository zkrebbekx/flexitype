// Package dataloader provides a generic request-scoped batching and caching
// loader. Repositories use it to collapse N point lookups into one
// `WHERE key = ANY($1)` query, to deduplicate identical filter queries and
// to batch per-parent pagination — significantly reducing database load and
// repository code duplication.
//
// Loaders are intended to live for a single request/unit-of-work: create
// them in the application factory's New(ctx) and let them die with the
// request. Never share a loader across tenants or transactions.
package dataloader

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Config tunes loader batching behaviour.
type Config struct {
	// Wait is how long the loader collects keys before firing a batch.
	Wait time.Duration
	// MaxBatch caps how many keys are sent in a single batch query.
	MaxBatch int
}

// DefaultConfig batches aggressively enough to collapse a request's lookups
// without adding perceptible latency.
func DefaultConfig() Config {
	return Config{
		Wait:     2 * time.Millisecond,
		MaxBatch: 500,
	}
}

// BatchFunc performs the batched load. Missing keys are simply absent from
// the returned map; the loader resolves them via the loader's miss policy
// (see NewLoader and NewSliceLoader).
type BatchFunc[K comparable, V any] func(ctx context.Context, keys []K) (map[K]V, error)

type result[V any] struct {
	value V
	err   error
}

type batch[K comparable, V any] struct {
	keys    []K
	seen    map[K]struct{}
	done    chan struct{}
	results map[K]result[V]
	err     error
}

// Loader batches and caches loads by key. Safe for concurrent use.
type Loader[K comparable, V any] struct {
	fetch    BatchFunc[K, V]
	wait     time.Duration
	maxBatch int
	missFn   func(K) result[V]

	mu    sync.Mutex
	cache map[K]result[V]
	cur   *batch[K, V]
}

// NewLoader creates a loader whose misses resolve to an error. Use for
// by-ID lookups where absence is exceptional.
func NewLoader[K comparable, V any](fetch BatchFunc[K, V], cfg Config) *Loader[K, V] {
	return newLoader(fetch, cfg, func(k K) result[V] {
		var zero V
		return result[V]{value: zero, err: fmt.Errorf("dataloader: key not found: %v", k)}
	})
}

// NewZeroLoader creates a loader whose misses resolve to the zero value
// with no error. Use for by-ID lookups where the repository maps the zero
// value to its own not-found error, keeping error types out of the loader.
func NewZeroLoader[K comparable, V any](fetch BatchFunc[K, V], cfg Config) *Loader[K, V] {
	return newLoader(fetch, cfg, func(K) result[V] {
		var zero V
		return result[V]{value: zero}
	})
}

// NewSliceLoader creates a loader whose misses resolve to the zero value
// with no error. Use for child-collection and filter loads where an empty
// result is a normal outcome.
func NewSliceLoader[K comparable, V any](fetch BatchFunc[K, V], cfg Config) *Loader[K, V] {
	return newLoader(fetch, cfg, func(K) result[V] {
		var zero V
		return result[V]{value: zero}
	})
}

func newLoader[K comparable, V any](fetch BatchFunc[K, V], cfg Config, miss func(K) result[V]) *Loader[K, V] {
	if cfg.Wait <= 0 {
		cfg.Wait = DefaultConfig().Wait
	}
	if cfg.MaxBatch <= 0 {
		cfg.MaxBatch = DefaultConfig().MaxBatch
	}
	return &Loader[K, V]{
		fetch:    fetch,
		wait:     cfg.Wait,
		maxBatch: cfg.MaxBatch,
		missFn:   miss,
		cache:    make(map[K]result[V]),
	}
}

// Load returns the value for key, batching concurrent callers and caching
// results for the loader's lifetime.
func (l *Loader[K, V]) Load(ctx context.Context, key K) (V, error) {
	l.mu.Lock()
	if r, ok := l.cache[key]; ok {
		l.mu.Unlock()
		return r.value, r.err
	}

	b := l.cur
	if b == nil {
		b = &batch[K, V]{
			seen: make(map[K]struct{}),
			done: make(chan struct{}),
		}
		l.cur = b
		go l.scheduleFlush(ctx, b)
	}
	if _, ok := b.seen[key]; !ok {
		b.seen[key] = struct{}{}
		b.keys = append(b.keys, key)
		if len(b.keys) >= l.maxBatch {
			l.cur = nil
			go l.flush(ctx, b)
		}
	}
	l.mu.Unlock()

	select {
	case <-b.done:
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	}

	if b.err != nil {
		var zero V
		return zero, b.err
	}
	r := b.results[key]
	return r.value, r.err
}

// LoadMany loads all keys, preserving order. The first error encountered is
// returned alongside the partial results.
func (l *Loader[K, V]) LoadMany(ctx context.Context, keys []K) ([]V, error) {
	values := make([]V, len(keys))
	var firstErr error
	for i, k := range keys {
		v, err := l.Load(ctx, k)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		values[i] = v
	}
	return values, firstErr
}

// Prime seeds the cache, e.g. after a list query already fetched the rows.
func (l *Loader[K, V]) Prime(key K, value V) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cache[key] = result[V]{value: value}
}

// Clear evicts a key, forcing the next Load to re-fetch. Call after writes.
func (l *Loader[K, V]) Clear(key K) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.cache, key)
}

// ClearAll drops the entire cache.
func (l *Loader[K, V]) ClearAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cache = make(map[K]result[V])
}

func (l *Loader[K, V]) scheduleFlush(ctx context.Context, b *batch[K, V]) {
	timer := time.NewTimer(l.wait)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-b.done:
		return
	}

	l.mu.Lock()
	if l.cur == b {
		l.cur = nil
	} else {
		// Already flushed by the maxBatch path.
		l.mu.Unlock()
		return
	}
	l.mu.Unlock()

	l.flush(ctx, b)
}

func (l *Loader[K, V]) flush(ctx context.Context, b *batch[K, V]) {
	defer close(b.done)

	fetched, err := l.fetch(ctx, b.keys)
	if err != nil {
		b.err = fmt.Errorf("dataloader batch: %w", err)
		return
	}

	b.results = make(map[K]result[V], len(b.keys))
	l.mu.Lock()
	for _, k := range b.keys {
		var r result[V]
		if v, ok := fetched[k]; ok {
			r = result[V]{value: v}
		} else {
			r = l.missFn(k)
		}
		b.results[k] = r
		l.cache[k] = r
	}
	l.mu.Unlock()
}
