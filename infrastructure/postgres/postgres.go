// Package postgres implements flexitype's repository ports over PostgreSQL.
// Every read path runs through request-scoped dataloaders
// (graph-gophers/dataloader): point lookups batch into ANY() queries,
// filtered List queries collapse into one UNION ALL statement across the
// batch's unique JSON filter keys, and per-parent pagination batches into a
// single windowed query. Queries are built with ? placeholders and rebound
// to $N via sqlx.
package postgres

import (
	"database/sql"
	"errors"

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
	}
}

// pageKey batches per-parent paginated child loads: identical pages for
// different parents collapse into one windowed query.
type pageKey struct {
	Parent string
	Limit  int
	Offset int
}

// pageKeyGroups splits page keys by (limit, offset) so each group runs one
// windowed query.
func pageKeyGroups(keys []pageKey) map[[2]int][]string {
	groups := make(map[[2]int][]string)
	for _, k := range keys {
		g := [2]int{k.Limit, k.Offset}
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
