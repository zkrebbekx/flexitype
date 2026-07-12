package client

import (
	"context"
	"iter"
	"net/url"
	"strconv"
)

// PageInfo describes a page's position in the full result set. TotalCount is
// present only when the request asked for it (ListOptions.Total).
type PageInfo struct {
	HasNextPage     bool    `json:"has_next_page"`
	HasPreviousPage bool    `json:"has_previous_page"`
	NextCursor      *string `json:"next_cursor,omitempty"`
	TotalCount      *int    `json:"total_count,omitempty"`
}

// Page is one page of a list response.
type Page[T any] struct {
	Items    []T      `json:"items"`
	PageInfo PageInfo `json:"page_info"`
}

// ListOptions are the pagination arguments common to every list call. The
// cursor is opaque and keyset-based, so pages stay stable under concurrent
// writes; the total is computed only when Total is set.
type ListOptions struct {
	Limit  int    // page size; 0 uses the server default
	Cursor string // opaque keyset cursor; "" starts at the first page
	Total  bool   // request PageInfo.TotalCount
}

func (o ListOptions) apply(q url.Values) {
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.Cursor != "" {
		q.Set("cursor", o.Cursor)
	}
	if o.Total {
		q.Set("total", "true")
	}
}

func firstOpts(opts []ListOptions) ListOptions {
	if len(opts) > 0 {
		return opts[0]
	}
	return ListOptions{}
}

// paginate yields every item across all pages by following the keyset cursor.
// A fetch error is yielded once (with the zero item) and ends iteration; break
// out of the range loop to stop early.
func paginate[T any](fetch func(cursor string) (*Page[T], error)) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		cursor := ""
		for {
			page, err := fetch(cursor)
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			for _, it := range page.Items {
				if !yield(it, nil) {
					return
				}
			}
			if !page.PageInfo.HasNextPage || page.PageInfo.NextCursor == nil {
				return
			}
			cursor = *page.PageInfo.NextCursor
		}
	}
}

// listPage is the shared one-page GET for a keyset list endpoint.
func listPage[T any](ctx context.Context, c *Client, path string, base url.Values, opts ListOptions) (*Page[T], error) {
	q := cloneValues(base)
	opts.apply(q)
	var out Page[T]
	if err := c.do(ctx, "GET", path, q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// items GETs a non-paginated {"items":[...]} list.
func items[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	var out struct {
		Items []T `json:"items"`
	}
	if err := c.do(ctx, "GET", path, q, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vals := range v {
		for _, val := range vals {
			out.Add(k, val)
		}
	}
	return out
}
