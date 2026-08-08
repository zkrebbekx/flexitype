package db

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
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
//
// That guarantee holds against inserts and deletes, and it does NOT hold when
// a concurrent write changes a row's sort key. The entity listing orders
// newest-first on last_updated_at, which a trigger rewrites on every value
// write: an entity the sweep has not reached yet, written mid-sweep, jumps
// ahead of the cursor and can never satisfy the "strictly older" predicate
// again. It is skipped, silently. Set Stable for a full sweep.
type Page struct {
	Limit  int
	Cursor string // "" = first page
	// WantTotal asks for the full filtered count. It is computed with a
	// separate query only when requested, so unbounded lists don't pay for a
	// count on every page.
	WantTotal bool
	// Stable asks for an ordering on an IMMUTABLE key, for a consumer that
	// must see every row exactly once — a reindex, a CSV export, a
	// completeness sweep, a recompute.
	//
	// It trades presentation order for coverage: rows come back in id order
	// rather than newest-first. A row inserted ahead of the cursor mid-sweep
	// is still missed (it did not exist when the sweep started, so no
	// snapshot could include it), but no EXISTING row is ever skipped because
	// something wrote to it.
	Stable bool
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
// checked for shape only here, because the ordering columns are not known at
// this layer; a repository checks its arity and its value types against its
// own ordering (see ValidateKeyset).
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

// ErrInvalidCursor is the error every cursor rejection carries. It is a
// domain validation error, so an interface layer maps it onto a 4xx (the HTTP
// layer answers 422) instead of an internal error. The message never repeats
// the cursor's contents, because the cursor is attacker-controlled input.
func ErrInvalidCursor() error { return domainerrors.NewValidation("invalid cursor") }

// DecodeKeyset parses a keyset cursor back into its column values, erroring on
// a malformed cursor so callers can reject it as a bad request.
func DecodeKeyset(cursor string) ([]string, error) {
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return nil, ErrInvalidCursor()
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, ErrInvalidCursor()
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

// castKind is the value type a column's cast implies, for cursor validation.
type castKind int

const (
	castText castKind = iota // no cast, or a cast this package does not check
	castTime
	castNumber
)

// kind classifies the column's cast. An empty or unknown cast is text, and a
// text column accepts any string, so this package does not check it.
func (c KeysetColumn) kind() castKind {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(c.Cast), "::")) {
	case "timestamptz", "timestamp", "timestamp with time zone", "timestamp without time zone", "date":
		return castTime
	case "int", "int2", "int4", "int8", "smallint", "integer", "bigint",
		"numeric", "decimal", "real", "float4", "float8", "double precision":
		return castNumber
	default:
		return castText
	}
}

// keysetTimeFormats are the timestamp spellings a cursor value may use.
// KeysetTime emits the first one. The others accept a cursor that a client
// built by hand from an RFC 3339 timestamp, and a date-only value for a
// "::date" column.
var keysetTimeFormats = []string{keysetTimeLayout, time.RFC3339Nano, time.RFC3339, "2006-01-02"}

// ValidateKeyset decodes a cursor and checks it against the ordering columns
// it will be compared with, then returns the decoded column values.
//
// It rejects two cursors that the shape check in PageArgs.Resolve accepts:
//
//   - A cursor whose value count differs from the column count. Such a cursor
//     cannot address a row, so a caller must not page from the top with it.
//   - A cursor whose value does not parse as the type its column's cast
//     implies. Without this check, PostgreSQL evaluates the cast and fails
//     with SQLSTATE 22007 ("invalid datetime format"), which the service
//     reports as an internal error.
//
// A column with no cast (or a cast this package does not classify) holds
// text, which accepts any string, so ValidateKeyset does not check it.
//
// The cost is one parse for each cast column, once for each query, and only
// when the request carries a cursor.
func ValidateKeyset(cols []KeysetColumn, cursor string) ([]string, error) {
	values, err := DecodeKeyset(cursor)
	if err != nil {
		return nil, err
	}
	if len(values) != len(cols) {
		return nil, ErrInvalidCursor()
	}
	for i, col := range cols {
		switch col.kind() {
		case castTime:
			if !parsesAsTime(values[i]) {
				return nil, ErrInvalidCursor()
			}
		case castNumber:
			if _, err := strconv.ParseFloat(strings.TrimSpace(values[i]), 64); err != nil {
				return nil, ErrInvalidCursor()
			}
		case castText:
		}
	}
	return values, nil
}

// parsesAsTime reports whether the value is a timestamp a keyset cursor may
// carry.
func parsesAsTime(v string) bool {
	for _, layout := range keysetTimeFormats {
		if _, err := time.Parse(layout, v); err == nil {
			return true
		}
	}
	return false
}

// KeysetPredicate builds the row-tuple comparison selecting rows strictly after
// the cursor for the given ORDER BY columns, using `?` placeholders. It returns
// the empty string when the cursor is empty (first page). For ascending columns
// (c1, c2), "after (v1, v2)" expands to:
//
//	(c1 > v1) OR (c1 = v1 AND c2 > v2)
//
// A descending column flips its final comparison to `<`.
//
// It validates the cursor with ValidateKeyset first, so a cursor that carries
// the wrong number of values, or a value the column's cast cannot parse, is a
// validation error rather than a database error.
func KeysetPredicate(cols []KeysetColumn, cursor string) (string, []any, error) {
	if cursor == "" {
		return "", nil, nil
	}
	values, err := ValidateKeyset(cols, cursor)
	if err != nil {
		return "", nil, err
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
