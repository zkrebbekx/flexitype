/**
 * Optimistic concurrency for the console's full-replace editors.
 *
 * Four endpoints replace the whole editable record rather than merging fields:
 * the type definition, the attribute, the relationship definition and the
 * dependency. Two operators editing one record each send the whole record, so
 * without a check the later write erases the earlier one — including fields
 * that operator never looked at, and with nothing reported.
 *
 * Each editor sends the `version` it loaded, and the server refuses a write
 * whose version has moved on. The rules live here rather than in four drawers
 * so the behaviour, and the sentence the operator reads, are one thing.
 */

import { ApiError, isConflict } from './api'

/**
 * baselineVersion reads the version an edit is based on.
 *
 * Capture it when the form LOADS, never when it saves. Every one of these
 * records comes from a query cache that refetches in the background, so a
 * version read at save time describes a change the author never saw, and the
 * compare-and-swap then guards nothing.
 */
export function baselineVersion(record?: { version?: number } | null): number | undefined {
  return record?.version
}

/**
 * staleEditMessage renders a compare-and-swap failure, or returns null when
 * the error is something else.
 *
 * The advice matters as much as the report. These editors deliberately do NOT
 * re-arm the swap against the version they just fetched: saving again without
 * seeing the other operator's change is exactly the lost update this prevents.
 * Reopening the editor reloads the record, which is the recovery the sentence
 * has to name.
 */
export function staleEditMessage(e: unknown): string | null {
  if (!isConflict(e)) return null
  const reason = e instanceof ApiError ? e.message : 'The record was modified by someone else.'
  return `${reason} Close and reopen this editor to load their version, then re-apply your change.`
}
