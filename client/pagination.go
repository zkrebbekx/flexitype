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
	// HasNextPage reports that NextCursor will fetch more.
	HasNextPage bool `json:"has_next_page"`

	// HasPreviousPage reports only that this is not the first page. It is
	// not a backward-paging capability: the API has no "before" cursor and
	// no direction, so there is nothing to call to go back.
	//
	// To offer a Back button, keep the cursors you have already used and
	// re-request one — CursorStack does exactly that.
	HasPreviousPage bool `json:"has_previous_page"`

	NextCursor *string `json:"next_cursor,omitempty"`
	TotalCount *int    `json:"total_count,omitempty"`
}

// CursorStack remembers the cursor of each page a caller has visited, so a
// paged UI can step backwards over a forward-only keyset API.
//
// PageInfo.HasPreviousPage says a previous page exists but the API offers no
// way to ask for it, so every paged UI has to keep this state itself. The
// admin console does, in its own composable; this is the same thing for Go
// callers.
//
// A CursorStack is not safe for concurrent use: it belongs to one view.
//
//	var st client.CursorStack
//	page, err := c.Types().List(ctx, client.ListOptions{Cursor: st.Current()})
//	// forward:
//	st.Push(page.PageInfo)
//	// backward:
//	st.Pop()
type CursorStack struct {
	// visited holds the cursor used for each page in order. The first entry
	// is "" — the first page takes no cursor.
	visited []string
}

// Current returns the cursor for the page the stack is on. It is "" on the
// first page, which is what a list call wants there.
func (s *CursorStack) Current() string {
	if len(s.visited) == 0 {
		return ""
	}
	return s.visited[len(s.visited)-1]
}

// Push records that the caller moved forward, and returns the cursor for the
// next page. It reports false when info has no next page, leaving the stack
// untouched — so a Next button at the end of a list is a no-op rather than an
// empty page.
func (s *CursorStack) Push(info PageInfo) (string, bool) {
	if !info.HasNextPage || info.NextCursor == nil || *info.NextCursor == "" {
		return s.Current(), false
	}
	if len(s.visited) == 0 {
		s.visited = append(s.visited, "")
	}
	s.visited = append(s.visited, *info.NextCursor)
	return s.Current(), true
}

// Pop steps back one page and returns that page's cursor. It reports false on
// the first page, where there is nothing to go back to.
func (s *CursorStack) Pop() (string, bool) {
	if len(s.visited) < 2 {
		return "", false
	}
	s.visited = s.visited[:len(s.visited)-1]
	return s.Current(), true
}

// Depth is the zero-based index of the page the stack is on.
func (s *CursorStack) Depth() int {
	if len(s.visited) == 0 {
		return 0
	}
	return len(s.visited) - 1
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
