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
// cursor. Cursors are base64-encoded "offset:<n>" strings; pages use
// LIMIT/OFFSET, so a page can shift by rows inserted or deleted before it
// between requests. Callers that need stability across concurrent writes
// should snapshot or accept that (see issue #42 for the keyset follow-up).
type PageArgs struct {
	Limit  *int
	Cursor *string
}

// Resolve validates client args into a clamped limit/offset Page. A limit that
// is present but not a positive integer is rejected (callers surface it as a
// 422) rather than silently defaulted, so every endpoint validates pagination
// params the same way; a limit above the maximum is clamped. A malformed
// cursor is likewise rejected.
func (a PageArgs) Resolve() (Page, error) {
	limit := defaultPageSize
	if a.Limit != nil {
		if *a.Limit < 1 {
			return Page{}, fmt.Errorf("limit must be a positive integer")
		}
		limit = *a.Limit
		if limit > maxPageSize {
			limit = maxPageSize
		}
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

// DecodeCursor is the inverse of EncodeCursor: it reads the offset back out of
// an opaque cursor, so callers that synthesize per-row cursors (e.g. Relay
// edges) can locate a page within the full result set.
func DecodeCursor(cursor string) (int, error) {
	return decodeCursor(cursor)
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
