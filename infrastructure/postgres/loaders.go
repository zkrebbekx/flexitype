package postgres

import (
	"context"
	"time"

	"github.com/graph-gophers/dataloader/v7"
	"github.com/jmoiron/sqlx"
)

// pagedResult carries one page of rows plus the total row count for the
// filter, so callers build pagination metadata without a second query.
type pagedResult[T any] struct {
	Items []T
	Total int
}

// mapBatchFunc is the fetch shape the repositories implement: batched keys
// in, key→value map out. Missing keys resolve through miss.
type mapBatchFunc[K comparable, V any] func(ctx context.Context, keys []K) (map[K]V, error)

// newLoader adapts a map-based batch fetch onto graph-gophers/dataloader,
// aligning results with keys as the library requires. Loaders are
// request-scoped: the built-in cache dies with the repository set.
func newLoader[K comparable, V any](fetch mapBatchFunc[K, V]) *dataloader.Loader[K, V] {
	batch := func(ctx context.Context, keys []K) []*dataloader.Result[V] {
		out := make([]*dataloader.Result[V], len(keys))
		fetched, err := fetch(ctx, keys)
		for i, k := range keys {
			if err != nil {
				out[i] = &dataloader.Result[V]{Error: err}
				continue
			}
			// Missing keys resolve to the zero value; repositories map that
			// onto their own not-found semantics.
			out[i] = &dataloader.Result[V]{Data: fetched[k]}
		}
		return out
	}
	return dataloader.NewBatchedLoader(batch,
		dataloader.WithWait[K, V](2*time.Millisecond),
		dataloader.WithBatchCapacity[K, V](500),
	)
}

// load resolves one key through a loader (Load returns a thunk).
func load[K comparable, V any](ctx context.Context, l *dataloader.Loader[K, V], key K) (V, error) {
	return l.Load(ctx, key)()
}

// bind rewrites ? placeholders into PostgreSQL $N form. Queries are built
// with ? and sqlx args; no hand-counted placeholder arithmetic.
func bind(query string) string {
	return sqlx.Rebind(sqlx.DOLLAR, query)
}
