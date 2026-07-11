package db

import (
	"encoding/base64"
	"fmt"
	"strings"
)

const (
	defaultPageSize = 20
	maxPageSize     = 200
)

// Page holds resolved limit/offset values for database queries.
type Page struct {
	Limit  int
	Offset int
}

// PageArgs are raw client pagination arguments: a page size and an opaque
// cursor. Cursors are base64-encoded "offset:<n>" strings.
type PageArgs struct {
	Limit  *int
	Cursor *string
}

// Resolve converts client args into a clamped limit/offset Page.
func (a PageArgs) Resolve() (Page, error) {
	limit := defaultPageSize
	if a.Limit != nil {
		limit = *a.Limit
	}
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}

	offset := 0
	if a.Cursor != nil && *a.Cursor != "" {
		n, err := decodeCursor(*a.Cursor)
		if err != nil {
			return Page{}, fmt.Errorf("invalid cursor: %w", err)
		}
		offset = n
	}

	return Page{Limit: limit, Offset: offset}, nil
}

// PageInfo describes the position of a page within the full result set.
type PageInfo struct {
	HasNextPage     bool    `json:"has_next_page"`
	HasPreviousPage bool    `json:"has_previous_page"`
	NextCursor      *string `json:"next_cursor,omitempty"`
	TotalCount      int     `json:"total_count"`
}

// BuildPageInfo derives PageInfo from a resolved page, the number of rows
// returned and the total row count for the query.
func BuildPageInfo(page Page, resultCount, totalCount int) PageInfo {
	info := PageInfo{
		HasNextPage:     page.Offset+resultCount < totalCount,
		HasPreviousPage: page.Offset > 0,
		TotalCount:      totalCount,
	}
	if info.HasNextPage {
		next := EncodeCursor(page.Offset + resultCount)
		info.NextCursor = &next
	}
	return info
}

// EncodeCursor encodes an offset into a stable opaque cursor string.
func EncodeCursor(offset int) string {
	return base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "offset:%d", offset))
}

func decodeCursor(cursor string) (int, error) {
	b, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0, err
	}
	parts := strings.SplitN(string(b), ":", 2)
	if len(parts) != 2 || parts[0] != "offset" {
		return 0, fmt.Errorf("malformed cursor")
	}
	var n int
	if _, err := fmt.Sscanf(parts[1], "%d", &n); err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("negative cursor offset")
	}
	return n, nil
}
