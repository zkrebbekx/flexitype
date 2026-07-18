// Package postgres implements flexitype's repository ports over PostgreSQL.
// Every read path runs through request-scoped dataloaders
// (graph-gophers/dataloader): point lookups batch into ANY() queries,
// filtered List queries collapse into one UNION ALL statement across the
// batch's unique JSON filter keys, and per-parent pagination batches into a
// single windowed query. Queries are built with ? placeholders and rebound
// to $N via sqlx.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// NewRepositories builds one request-scoped repository set over the pool.
// Call once per request so dataloader caches die with the request.
func NewRepositories(pool db.QueryExecer) application.Repositories {
	return application.Repositories{
		TypeDefinitions:         NewTypeDefinitionRepository(pool),
		Attributes:              NewAttributeDefinitionRepository(pool),
		Values:                  NewAttributeValueRepository(pool),
		Dependencies:            NewDependencyRepository(pool),
		RelationshipDefinitions: NewRelationshipDefinitionRepository(pool),
		Relationships:           NewRelationshipRepository(pool),
		Query:                   NewQueryRepository(pool),
		SchemaVersions:          NewSchemaVersionReader(pool),
	}
}

// txExecer down-casts the opaque transaction handle a repository or sink is
// handed back to the SQL executor the PostgreSQL backend runs queries through.
// The handle is always the sqlx-backed transactor in this backend; db.Tx keeps
// the domain from executing SQL through it, but the backend that opened the
// transaction knows its concrete type.
func txExecer(tx db.Tx) db.QueryExecer { return tx.(db.QueryExecer) }

// idKeyset is the single-column ascending keyset used by every id-ordered list.
var idKeyset = []db.KeysetColumn{{Expr: "id"}}

// keysetWhere appends the keyset predicate for the given ordering and cursor to
// a WHERE slice and its args. The cursor is validated at the application layer
// (PageArgs.Resolve), so a decode error here is treated as "no predicate".
func keysetWhere(where []string, args []any, cols []db.KeysetColumn, cursor string) ([]string, []any) {
	pred, pargs, err := db.KeysetPredicate(cols, cursor)
	if err != nil || pred == "" {
		return where, args
	}
	return append(where, pred), append(args, pargs...)
}

// countIf runs a count query and returns its result, but only when the caller
// asked for the total; otherwise it returns 0 without touching the database.
// The count query is separate from the keyset page so an unbounded list does
// not pay for a count on every page.
func countIf(ctx context.Context, q db.QueryExecer, want bool, query func() (string, []any)) (int, error) {
	if !want {
		return 0, nil
	}
	sql, args := query()
	var n int
	if err := q.GetContext(ctx, &n, bind(sql), args...); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

// pageKey batches per-parent paginated child loads: identical pages for
// different parents collapse into one windowed query.
type pageKey struct {
	Parent    string
	Limit     int
	Cursor    string
	WantTotal bool
}

// pageKeyGroups splits page keys by (limit, cursor, wantTotal) so each group
// runs one windowed query.
func pageKeyGroups(keys []pageKey) map[[3]string][]string {
	groups := make(map[[3]string][]string)
	for _, k := range keys {
		g := [3]string{fmt.Sprintf("%d", k.Limit), k.Cursor, fmt.Sprintf("%t", k.WantTotal)}
		groups[g] = append(groups[g], k.Parent)
	}
	return groups
}

// jsonbParam renders JSON bytes as a jsonb-compatible driver argument:
// lib/pq maps []byte to bytea, which PostgreSQL rejects for jsonb columns,
// so JSON must travel as text. Empty maps to NULL.
func jsonbParam(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

// isNoRows reports whether err is sql.ErrNoRows.
func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
