package value

import "sort"

// Canonical write ordering for multi-entity transactions.
//
// Every value write refreshes a shared summary row keyed on
// (tenant, anchor type, entity). Two transactions writing DISJOINT value rows
// of the same two entities, in opposite order, therefore take those summary
// rows in opposite order and deadlock — even though their own rows never
// conflict. Nothing about a caller's input orders entities, so opposite order
// is the normal case rather than the exception, and a naive retry re-runs the
// same unordered writes into the same lock ordering.
//
// The statement trigger sorts keys WITHIN one statement, which covers a
// single multi-row statement but not a loop that issues one INSERT per value —
// which is what every path here does. Measured against Postgres 16, two
// change-set publishes over the same two entities in opposite order deadlocked
// 40 times out of 40 rounds.
//
// THE SORT KEY IS THE ENTITY ID ALONE. An entity has exactly one anchor type
// (the anchor invariant), so it has exactly one summary row, and ordering by
// entity id is therefore a total order over the rows a transaction touches.
// Sorting by the caller-supplied type first was wrong for the same reason it
// looked right: that field is optional, so a batch that omits it and one that
// supplies it sorted the same mixed-type entities differently, and two writers
// could still invert their lock order.
//
// Sorting is applied wherever a transaction writes values for more than one
// entity: SetBatch, ApplyMutations (change-set publish), and the CSV import
// paths. ApplySnapshot needs none — a snapshot restores one entity, so it
// touches one summary row.

// canonicalOrder returns the indices of items sorted by entity id, with ties
// broken by the original position so the caller's order is preserved within
// one entity.
//
// It returns indices rather than reordering the slice, so the caller's order
// survives in the output and in the item index an error reports: the lock
// ordering is invisible from outside.
func canonicalOrder[T any](items []T, entityOf func(T) string) []int {
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return entityOf(items[order[a]]) < entityOf(items[order[b]])
	})
	return order
}
