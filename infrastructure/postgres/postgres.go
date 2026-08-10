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

	"github.com/lib/pq"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// NewRepositories builds one request-scoped repository set over the pool.
// Call once per request so dataloader caches die with the request.
func NewRepositories(pool db.QueryExecer) application.Repositories {
	// One value repository serves both the aggregate write port (Values) and
	// the read-model port (ValueReader): the same struct implements both.
	values := NewAttributeValueRepository(pool)
	return application.Repositories{
		TypeDefinitions:         NewTypeDefinitionRepository(pool),
		Attributes:              NewAttributeDefinitionRepository(pool),
		Values:                  values,
		ValueReader:             values.(application.ValueReader),
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

// entitySummaryKeyset is the newest-first ordering every entity-summary list
// pages on: last_updated_at descending, with entity_id ascending as the unique
// tiebreaker. The "::timestamptz" cast is what db.ValidateKeyset checks the
// cursor's first value against, so a value that is not a timestamp is a
// validation error and never reaches the cast in the query.
//
// The entity browser and the FQL entity query share this spec so both reject
// the same cursors, even though the FQL query builds its own SQL with
// table-qualified expressions.
var entitySummaryKeyset = []db.KeysetColumn{
	{Expr: "last_updated_at", Desc: true, Cast: "::timestamptz"},
	{Expr: "entity_id"},
}

// keysetWhere appends the keyset predicate for the given ordering and cursor to
// a WHERE slice and its args.
//
// It returns the error from db.KeysetPredicate unchanged, and the caller must
// return it. The error is a domain validation error, so the request fails with
// a 422. An earlier version discarded it and returned the WHERE slice
// unchanged, which served page 1 again for a cursor of the wrong arity and let
// an unparseable timestamp reach the "::timestamptz" cast, where PostgreSQL
// failed with SQLSTATE 22007 and the service reported an internal error.
func keysetWhere(where []string, args []any, cols []db.KeysetColumn, cursor string) ([]string, []any, error) {
	pred, pargs, err := db.KeysetPredicate(cols, cursor)
	if err != nil {
		return nil, nil, err
	}
	if pred == "" {
		return where, args, nil
	}
	return append(where, pred), append(args, pargs...), nil
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
// isUniqueViolation reports whether err is SQLSTATE 23505.
//
// A store that inserts against a UNIQUE index must translate it, or the caller
// gets a generic 500 for something the in-memory twin answers as a 409. The
// application layer normally checks first and returns a conflict itself, so
// this is the RACE: two callers past the same check, one of which loses at the
// index.
//
// Matched on the code, never on the constraint name or the driver's message —
// the message is the server's localized text, and the constraint name is
// schema detail that must not reach a client.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
