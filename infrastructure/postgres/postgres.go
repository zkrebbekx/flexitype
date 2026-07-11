// Package postgres implements flexitype's repository ports over PostgreSQL.
// Every read path runs through request-scoped dataloaders: point lookups
// batch into ANY() queries, identical filter/page queries deduplicate, and
// per-parent pagination batches into a single windowed query — cutting
// database round trips without per-repository caching code.
package postgres

import (
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/pkg/dataloader"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// NewRepositories builds one request-scoped repository set over the pool.
// Call once per request so dataloader caches die with the request.
func NewRepositories(pool db.QueryExecer) application.Repositories {
	return application.Repositories{
		TypeDefinitions: NewTypeDefinitionRepository(pool),
		Attributes:      NewAttributeDefinitionRepository(pool),
		Values:          NewAttributeValueRepository(pool),
		Dependencies:    NewDependencyRepository(pool),
	}
}

// loaderConfig is shared by every repository loader.
func loaderConfig() dataloader.Config { return dataloader.DefaultConfig() }

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

// joinSorted renders a slice as a canonical NUL-joined string so it can be
// part of a comparable dataloader key.
func joinSorted(items []string) string {
	if len(items) == 0 {
		return ""
	}
	sorted := make([]string, len(items))
	copy(sorted, items)
	sort.Strings(sorted)
	return strings.Join(sorted, "\x00")
}

// pageKeyGroups splits page keys by (limit, offset) so each group runs one
// windowed query.
func pageKeyGroups[P comparable](keys []dataloader.PageKey[P]) map[[2]int][]P {
	groups := make(map[[2]int][]P)
	for _, k := range keys {
		g := [2]int{k.Limit, k.Offset}
		groups[g] = append(groups[g], k.Parent)
	}
	return groups
}
