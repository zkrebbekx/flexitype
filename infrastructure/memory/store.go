// Package memory implements every flexitype repository port in process
// memory: no database, no migrations. It powers the browser playground and
// gives embedding consumers a zero-dependency test double with the same
// semantics as the PostgreSQL implementation (soft archiving, hierarchies,
// pagination, FQL). Writes apply to the shared maps immediately, but the
// memory transactor honours the commit-hook contract (activity, events) AND
// keeps a per-transaction undo journal, so a rollback reverses exactly the keys
// that transaction wrote — leaving a concurrently interleaved transaction's
// committed writes intact, matching PostgreSQL atomicity.
package memory

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/uow"
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
	// schemaVersions is the per-tenant schema version, bumped on any definition
	// (type/attribute/relationship) write, mirroring the Postgres trigger of
	// migration 000020 (issue #192). It backs the GraphQL engine's schema-cache
	// staleness check. Memory mode is single-process, so it only has to move on
	// a definition change. It is deliberately NOT journalled: a rolled-back
	// definition write leaving the counter advanced only causes a harmless
	// schema rebuild, never a stale schema.
	schemaVersions map[string]uint64
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
		typeDefs:       map[string]domaintypedef.Snapshot{},
		attrs:          map[string]domainattribute.Snapshot{},
		values:         map[string]domainvalue.Snapshot{},
		deps:           map[string]domaindependency.Snapshot{},
		relDefs:        map[string]domainrelationship.DefinitionSnapshot{},
		rels:           map[string]domainrelationship.Snapshot{},
		searchDocs:     map[string]searchDoc{},
		schemaVersions: map[string]uint64{},
	}
}

// bumpSchemaVersion advances a tenant's schema version. The definition-repo
// Save methods that call it already hold s.mu, so it takes no lock of its own.
func (s *Store) bumpSchemaVersion(tenant string) {
	s.schemaVersions[tenant]++
}

// schemaVersionReader serves the persisted per-tenant schema version so the
// GraphQL engine's cross-replica cache check works identically over the memory
// backend (issue #192).
type schemaVersionReader struct{ s *Store }

func (r *schemaVersionReader) SchemaVersion(ctx context.Context) (uint64, error) {
	tenant := uow.TenantFromContext(ctx)
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	return r.s.schemaVersions[tenant.String()], nil
}

// Repositories returns the full repository set over this store, including
// the in-process FQL executor.
func (s *Store) Repositories() application.Repositories {
	return application.Repositories{
		TypeDefinitions:         &typeDefRepo{s: s},
		Attributes:              &attrRepo{s: s},
		Values:                  &valueRepo{s: s},
		Dependencies:            &depRepo{s: s},
		RelationshipDefinitions: &relDefRepo{s: s},
		Relationships:           &relRepo{s: s},
		Query:                   &queryRepo{s},
		SchemaVersions:          &schemaVersionReader{s},
	}
}

// ActivityLog returns the in-memory audit log.
func (s *Store) ActivityLog() activity.Log { return &activityLog{s} }

// Transactor returns a transactor honouring the pre/post/rollback hook
// contract. On rollback it replays the transaction's undo journal — restoring
// exactly the keys that transaction wrote — so a unit of work that writes then
// fails leaves no partial data (matching PostgreSQL's atomicity) WITHOUT
// disturbing the writes any other in-flight transaction has committed.
func (s *Store) Transactor() db.Transactor { return &transactor{store: s} }

// The collection tags below name the map a journalled key belongs to, so the
// same id in two different collections gets two independent undo entries.
const (
	collTypeDefs   = "type_definitions"
	collAttrs      = "attributes"
	collValues     = "values"
	collDeps       = "dependencies"
	collRelDefs    = "relationship_definitions"
	collRels       = "relationships"
	collActivities = "activities"
)

// journalKey identifies one journalled store entry: its collection and its id.
type journalKey struct {
	coll string
	id   string
}

// undoJournal records how to reverse a single transaction's writes. On the
// FIRST mutation of a given key within the transaction it captures that key's
// prior value (or its absence); Rollback replays the entries to restore exactly
// those keys and Commit simply discards the journal.
//
// It replaces the old whole-store snapshot, which broke isolation — a rollback
// restored the ENTIRE store, silently erasing writes another interleaved
// transaction had already committed — and cost O(dataset) per write
// transaction. The journal touches only the keys a transaction actually wrote,
// so interleaved transactions no longer clobber one another and a write
// transaction is O(touched keys).
type undoJournal struct {
	seen  map[journalKey]struct{} // keys already captured (first-write-wins)
	undos []func()                // restore closures, replayed in reverse
}

func newUndoJournal() *undoJournal {
	return &undoJournal{seen: map[journalKey]struct{}{}}
}

// captureMap records, once per transaction, the prior state of key id in map m
// before the caller overwrites or deletes it, so a rollback can put it back
// (its prior value, or a delete when it did not exist). The caller holds s.mu.
// A nil journal — a write made outside any transaction — is a no-op, since
// there is nothing to roll back to. The map header is captured, never
// reassigned, so restoring mutates the live store map in place.
func captureMap[V any](j *undoJournal, coll string, m map[string]V, id string) {
	if j == nil {
		return
	}
	k := journalKey{coll: coll, id: id}
	if _, done := j.seen[k]; done {
		return
	}
	j.seen[k] = struct{}{}
	prior, existed := m[id]
	j.undos = append(j.undos, func() {
		if existed {
			m[id] = prior
		} else {
			delete(m, id)
		}
	})
}

// captureActivities records the activity log's slice header at the first append
// in the transaction so a rollback truncates the appended entries back off. The
// log is append-only, so restoring the pre-append header is enough.
func captureActivities(j *undoJournal, s *Store) {
	if j == nil {
		return
	}
	k := journalKey{coll: collActivities}
	if _, done := j.seen[k]; done {
		return
	}
	j.seen[k] = struct{}{}
	prior := s.activities
	j.undos = append(j.undos, func() { s.activities = prior })
}

// rollback replays the journal under the store lock, restoring every captured
// key to its pre-transaction state (newest entry first).
func (s *Store) rollback(j *undoJournal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(j.undos) - 1; i >= 0; i-- {
		j.undos[i]()
	}
}

// paginate returns the keyset page of a result set already sorted in the
// list's order: the items strictly after the cursor, over-fetched by one so
// the caller can detect a next page. keyOf extracts an item's ORDER BY column
// values and desc flags the descending columns. The cursor is compared by
// decoded key VALUE (not exact row identity), so a page stays correct even when
// the cursor row was updated or deleted between requests — matching the
// Postgres row-tuple predicate. The total is always returned; the application
// layer drops it unless requested.
func paginate[T any](items []T, page db.Page, keyOf func(T) []string, desc ...bool) ([]T, int) {
	total := len(items)
	start := 0
	if page.Cursor != "" {
		if cur, err := db.DecodeKeyset(page.Cursor); err == nil {
			// items are sorted in list order, so keyAfter is monotonic: find
			// the first item strictly after the cursor key.
			start = sort.Search(total, func(i int) bool {
				return keyAfter(keyOf(items[i]), cur, desc)
			})
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

// keyAfter reports whether row key a sorts strictly after cursor key b in the
// list's order (desc[i] flags a descending column), mirroring the SQL
// row-tuple comparison the Postgres backend uses. Timestamps are compared as
// their RFC3339Nano (UTC) strings, which order chronologically.
func keyAfter(a, b []string, desc []bool) bool {
	for i := range a {
		if i >= len(b) || a[i] == b[i] {
			continue
		}
		if i < len(desc) && desc[i] {
			return a[i] < b[i]
		}
		return a[i] > b[i]
	}
	return false // equal (or a is a prefix of b) → not strictly after
}

// idKey is the keyset key for an id-ordered (ascending) list.
func idKey(id string) []string { return []string{id} }

// entityKey is the composite keyset key for entity lists ordered by
// last-updated (descending) with the entity id as the ascending tiebreaker.
func entityKey(lastUpdated time.Time, entityID string) []string {
	return []string{lastUpdated.UTC().Format(time.RFC3339Nano), entityID}
}

// entryKey is the composite keyset key for the activity log, ordered by
// occurred-at then id (both descending — newest first).
func entryKey(e activity.Entry) []string {
	return []string{e.OccurredAt.UTC().Format(time.RFC3339Nano), e.ID.String()}
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
