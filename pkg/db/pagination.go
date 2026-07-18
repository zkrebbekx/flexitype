package db

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	defaultPageSize = 20
	maxPageSize     = 200
)

// Page is a resolved pagination request: a clamped page size, an opaque keyset
// cursor, and whether the caller wants the total count. The cursor encodes the
// ORDER BY column values of the last row of the previous page; each repository
// decodes it against its own ordering and selects the rows strictly after it.
// Keyset (rather than LIMIT/OFFSET) keeps pages stable under concurrent inserts
// and deletes — no skipped or duplicated rows.
type Page struct {
	Limit  int
	Cursor string // "" = first page
	// WantTotal asks for the full filtered count. It is computed with a
	// separate query only when requested, so unbounded lists don't pay for a
	// count on every page.
	WantTotal bool
}

// PageArgs are raw client pagination arguments.
type PageArgs struct {
	Limit     *int
	Cursor    *string
	WantTotal bool
}

// Resolve validates and clamps client args. A limit that is present but not a
// positive integer is rejected (callers surface it as a 422) rather than
// silently defaulted; a limit above the maximum is clamped. The cursor is
// passed through opaquely and validated when a repository decodes it.
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
	cursor := ""
	if a.Cursor != nil && *a.Cursor != "" {
		// Validate the cursor's shape here (a 422) so a malformed cursor is
		// rejected before it reaches a repository; the column count is checked
		// when the repository decodes it against its own ordering.
		if _, err := DecodeKeyset(*a.Cursor); err != nil {
			return Page{}, fmt.Errorf("invalid cursor")
		}
		cursor = *a.Cursor
	}
	return Page{Limit: limit, Cursor: cursor, WantTotal: a.WantTotal}, nil
}

// FetchLimit is the row count a keyset query should request: one more than the
// page size, so the caller can tell whether another page exists by whether the
// sentinel row came back.
func (p Page) FetchLimit() int { return p.Limit + 1 }

// PageInfo describes the position of a page within the full result set.
// TotalCount is present only when the caller asked for it (Page.WantTotal).
type PageInfo struct {
	HasNextPage     bool    `json:"has_next_page"`
	HasPreviousPage bool    `json:"has_previous_page"`
	NextCursor      *string `json:"next_cursor,omitempty"`
	TotalCount      *int    `json:"total_count,omitempty"`
}

// KeysetPage finalizes a keyset page: repositories over-fetch by one row
// (page.FetchLimit); this trims the sentinel, reports whether more remain and
// builds PageInfo with the cursor of the last returned row. cursorOf extracts a
// row's keyset cursor (typically db.EncodeKeyset of its ORDER BY values). total
// is nil unless the caller requested the count.
func KeysetPage[T any](page Page, items []T, total *int, cursorOf func(T) string) ([]T, PageInfo) {
	hasNext := len(items) > page.Limit
	if hasNext {
		items = items[:page.Limit]
	}
	info := PageInfo{
		HasNextPage:     hasNext,
		HasPreviousPage: page.Cursor != "",
		TotalCount:      total,
	}
	if hasNext && len(items) > 0 {
		next := cursorOf(items[len(items)-1])
		info.NextCursor = &next
	}
	return items, info
}

// keysetTimeLayout renders a timestamp with a FIXED-WIDTH nanosecond fraction.
//
// It exists because time.RFC3339Nano strips trailing zeros, so ".365800000"
// renders as ".3658" while ".365821000" renders as ".365821". Backends that
// compare cursor columns as strings (the in-memory one) then order them by the
// character after the shorter fraction — a digit versus the trailing 'Z' — and
// since 'Z' (0x5A) sorts above every digit (0x30-0x39), an OLDER timestamp can
// compare as newer. That made the comparison non-chronological and, worse,
// non-monotonic, so the binary search over a sorted page landed in the wrong
// region and silently skipped rows. A fixed-width fraction makes lexical order
// match chronological order. Postgres is unaffected either way: it casts the
// cursor value to timestamptz and compares it numerically.
const keysetTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// KeysetTime renders a timestamp for use as a keyset cursor column. Always use
// this rather than formatting inline, so every producer and comparator agrees
// on one lexically-ordered representation.
func KeysetTime(t time.Time) string { return t.UTC().Format(keysetTimeLayout) }

// EncodeKeyset builds an opaque cursor from a row's ORDER BY column values, in
// ORDER BY order (stringified: ids as-is, timestamps via KeysetTime).
func EncodeKeyset(values ...string) string {
	b, _ := json.Marshal(values)
	return base64.StdEncoding.EncodeToString(b)
}

// DecodeKeyset parses a keyset cursor back into its column values, erroring on
// a malformed cursor so callers can reject it as a bad request.
func DecodeKeyset(cursor string) ([]string, error) {
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor")
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("invalid cursor")
	}
	return values, nil
}

// KeysetColumn is one ORDER BY column for keyset pagination: a SQL expression,
// its direction, and an optional cast applied to the bound cursor value (e.g.
// "::timestamptz" so a string parameter compares against a timestamp column).
type KeysetColumn struct {
	Expr string
	Desc bool
	Cast string
}

// KeysetPredicate builds the row-tuple comparison selecting rows strictly after
// the cursor for the given ORDER BY columns, using `?` placeholders. It returns
// the empty string when the cursor is empty (first page). For ascending columns
// (c1, c2), "after (v1, v2)" expands to:
//
//	(c1 > v1) OR (c1 = v1 AND c2 > v2)
//
// A descending column flips its final comparison to `<`.
func KeysetPredicate(cols []KeysetColumn, cursor string) (string, []any, error) {
	if cursor == "" {
		return "", nil, nil
	}
	values, err := DecodeKeyset(cursor)
	if err != nil {
		return "", nil, err
	}
	if len(values) != len(cols) {
		return "", nil, fmt.Errorf("invalid cursor")
	}
	var ors []string
	var args []any
	for i := range cols {
		var ands []string
		for j := 0; j < i; j++ {
			ands = append(ands, cols[j].Expr+" = ?"+cols[j].Cast)
			args = append(args, values[j])
		}
		cmp := ">"
		if cols[i].Desc {
			cmp = "<"
		}
		ands = append(ands, cols[i].Expr+" "+cmp+" ?"+cols[i].Cast)
		args = append(args, values[i])
		ors = append(ors, "("+strings.Join(ands, " AND ")+")")
	}
	return "(" + strings.Join(ors, " OR ") + ")", args, nil
}

// KeysetTotal wraps a computed count as the optional PageInfo total, or nil
// when the caller did not request it.
func KeysetTotal(page Page, count int) *int {
	if !page.WantTotal {
		return nil
	}
	return &count
}
