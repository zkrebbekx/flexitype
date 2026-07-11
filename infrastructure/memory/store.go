// Package memory implements every flexitype repository port in process
// memory: no database, no migrations. It powers the browser playground and
// gives embedding consumers a zero-dependency test double with the same
// semantics as the PostgreSQL implementation (soft archiving, hierarchies,
// pagination, FQL). Writes are NOT transactional — the memory transactor
// honours the commit-hook contract (activity, events) but data mutations
// apply immediately.
package memory

import (
	"sort"
	"strings"
	"sync"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/activity"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// Store is the shared in-memory state every repository views.
type Store struct {
	mu sync.RWMutex

	typeDefs   map[string]domaintypedef.Snapshot
	attrs      map[string]domainattribute.Snapshot
	values     map[string]domainvalue.Snapshot
	deps       map[string]domaindependency.Snapshot
	relDefs    map[string]domainrelationship.DefinitionSnapshot
	rels       map[string]domainrelationship.Snapshot
	activities []activity.Entry
	searchDocs map[string]searchDoc // key: tenant + "\x00" + entity
}

type searchDoc struct {
	tenant string
	typeID string
	entity string
	values map[string][]string
	text   string
}

// NewStore creates an empty in-memory store.
func NewStore() *Store {
	return &Store{
		typeDefs:   map[string]domaintypedef.Snapshot{},
		attrs:      map[string]domainattribute.Snapshot{},
		values:     map[string]domainvalue.Snapshot{},
		deps:       map[string]domaindependency.Snapshot{},
		relDefs:    map[string]domainrelationship.DefinitionSnapshot{},
		rels:       map[string]domainrelationship.Snapshot{},
		searchDocs: map[string]searchDoc{},
	}
}

// Repositories returns the full repository set over this store, including
// the in-process FQL executor.
func (s *Store) Repositories() application.Repositories {
	return application.Repositories{
		TypeDefinitions:         &typeDefRepo{s},
		Attributes:              &attrRepo{s},
		Values:                  &valueRepo{s},
		Dependencies:            &depRepo{s},
		RelationshipDefinitions: &relDefRepo{s},
		Relationships:           &relRepo{s},
		Query:                   &queryRepo{s},
	}
}

// ActivityLog returns the in-memory audit log.
func (s *Store) ActivityLog() activity.Log { return &activityLog{s} }

// Transactor returns a transactor honouring the pre/post/rollback hook
// contract without real transactions.
func (s *Store) Transactor() db.Transactor { return &transactor{} }

// paginate slices a sorted result set and reports the total.
func paginate[T any](items []T, page db.Page) ([]T, int) {
	total := len(items)
	if page.Offset >= total {
		return nil, total
	}
	end := page.Offset + page.Limit
	if page.Limit <= 0 || end > total {
		end = total
	}
	return items[page.Offset:end], total
}

// sortByID orders snapshots by an id-extracting function for stable pages.
func sortByID[T any](items []T, id func(T) string) {
	sort.Slice(items, func(i, j int) bool { return id(items[i]) < id(items[j]) })
}

// matchNames reports whether name is in the filter list (empty = all).
func matchNames(names []string, name string) bool {
	if len(names) == 0 {
		return true
	}
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// containsFold is a case-insensitive substring test.
func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
