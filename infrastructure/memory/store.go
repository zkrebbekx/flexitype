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
	"time"

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
// contract. On rollback it restores the store to its pre-transaction
// state, so a unit of work that writes then fails leaves no partial data
// — matching PostgreSQL's atomicity.
func (s *Store) Transactor() db.Transactor { return &transactor{store: s} }

// storeSnapshot is a shallow copy of every collection. Entries are
// immutable value snapshots, so copying the maps is enough to undo any
// writes made during a transaction.
type storeSnapshot struct {
	typeDefs   map[string]domaintypedef.Snapshot
	attrs      map[string]domainattribute.Snapshot
	values     map[string]domainvalue.Snapshot
	deps       map[string]domaindependency.Snapshot
	relDefs    map[string]domainrelationship.DefinitionSnapshot
	rels       map[string]domainrelationship.Snapshot
	activities []activity.Entry
	searchDocs map[string]searchDoc
}

// snapshot captures the current state for a potential rollback.
func (s *Store) snapshot() storeSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return storeSnapshot{
		typeDefs:   cloneMap(s.typeDefs),
		attrs:      cloneMap(s.attrs),
		values:     cloneMap(s.values),
		deps:       cloneMap(s.deps),
		relDefs:    cloneMap(s.relDefs),
		rels:       cloneMap(s.rels),
		activities: append([]activity.Entry(nil), s.activities...),
		searchDocs: cloneMap(s.searchDocs),
	}
}

// restore reverts the store to a captured snapshot.
func (s *Store) restore(snap storeSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.typeDefs = snap.typeDefs
	s.attrs = snap.attrs
	s.values = snap.values
	s.deps = snap.deps
	s.relDefs = snap.relDefs
	s.rels = snap.rels
	s.activities = snap.activities
	s.searchDocs = snap.searchDocs
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// paginate returns the keyset page of a result set already sorted in the
// list's order: the items strictly after the cursor (matched by cursorOf,
// exclusive), over-fetched by one so the caller can detect a next page. The
// total is always returned; the application layer drops it unless requested.
func paginate[T any](items []T, page db.Page, cursorOf func(T) string) ([]T, int) {
	total := len(items)
	start := 0
	if page.Cursor != "" {
		for i := range items {
			if cursorOf(items[i]) == page.Cursor {
				start = i + 1
				break
			}
		}
	}
	if start >= total {
		return nil, total
	}
	end := start + page.Limit + 1 // over-fetch by one (keyset sentinel)
	if page.Limit <= 0 || end > total {
		end = total
	}
	return items[start:end], total
}

// idCursor is the keyset cursor for an id-ordered list.
func idCursor(id string) string { return db.EncodeKeyset(id) }

// entityCursor is the composite keyset cursor for entity lists ordered by
// last-updated (newest first) with the entity id as the unique tiebreaker.
func entityCursor(lastUpdated time.Time, entityID string) string {
	return db.EncodeKeyset(lastUpdated.UTC().Format(time.RFC3339Nano), entityID)
}

// entryCursor is the composite keyset cursor for the activity log (newest
// first with the id as the tiebreaker).
func entryCursor(e activity.Entry) string {
	return db.EncodeKeyset(e.OccurredAt.UTC().Format(time.RFC3339Nano), e.ID.String())
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
