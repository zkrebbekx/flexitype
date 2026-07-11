package dataloader

// PageKey batches per-parent paginated child loads. Because the key is
// comparable, identical pages requested concurrently collapse into a single
// windowed query (row_number() OVER (PARTITION BY parent)) and repeat
// requests are served from the loader cache.
type PageKey[P comparable] struct {
	Parent P
	Limit  int
	Offset int
}

// FilterKey batches filtered list queries. Filter must be a comparable
// struct capturing every predicate that affects the result set, so equal
// filters deduplicate and unequal filters never collide.
type FilterKey[F comparable] struct {
	Filter F
	Limit  int
	Offset int
}

// PagedResult carries one page of rows plus the total row count for the
// filter, so callers can build pagination metadata without a second count
// query.
type PagedResult[T any] struct {
	Items []T
	Total int
}
