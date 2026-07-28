// Role helpers the Roles page uses, kept out of the component so they can be
// tested the way the rest of lib/ is.

import type { FieldPermission, Role, Scope, ServiceAccount } from './api'

/** A row in the field-permission editor. */
export interface PermissionRow {
  attribute: string
  level: FieldPermission
}

/**
 * accountsHolding returns the accounts naming a role.
 *
 * The delete guard answers 409 while any account holds it — a role that
 * resolves to nothing grants no field permissions, and an empty permission map
 * reads as "unrestricted", so deleting a held role would hand out every
 * attribute it was hiding. Showing the holders puts that in front of the
 * operator before they try.
 */
export function accountsHolding(accounts: ServiceAccount[], role: string): ServiceAccount[] {
  return accounts.filter((a) => (a.roles ?? []).includes(role))
}

/** toPermissionRows turns a stored map into editor rows, in name order. */
export function toPermissionRows(perms: Record<string, FieldPermission> | undefined): PermissionRow[] {
  return Object.entries(perms ?? {})
    .map(([attribute, level]) => ({ attribute, level }))
    .sort((a, b) => a.attribute.localeCompare(b.attribute))
}

/**
 * fromPermissionRows turns editor rows back into the stored map.
 *
 * A blank attribute is dropped rather than sent: the API refuses an empty
 * name, and a half-typed row is not a restriction the operator meant to make.
 * A duplicate name keeps the LAST row, matching what the form shows.
 */
export function fromPermissionRows(rows: PermissionRow[]): Record<string, FieldPermission> {
  const out: Record<string, FieldPermission> = {}
  for (const row of rows) {
    const name = row.attribute.trim()
    if (name) out[name] = row.level
  }
  return out
}

/** scopesFrom builds a role's scope list from the editor's checkboxes. */
export function scopesFrom(flags: { read: boolean; write: boolean }): Scope[] {
  const out: Scope[] = []
  if (flags.read) out.push('read')
  if (flags.write) out.push('write')
  return out
}

/**
 * describeRole renders a role's grants in one line, for the list.
 *
 * An empty scope set is not an error: a role may carry only field
 * permissions — "this person sees every attribute except salary" is a real
 * grant — so it reads as "no scopes" rather than as missing data.
 */
export function describeRole(r: Role): string {
  const scopes = r.scopes.length ? r.scopes.join(', ') : 'no scopes'
  const perms = Object.entries(r.field_permissions ?? {})
  if (!perms.length) return scopes
  const rendered = perms
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([attr, level]) => `${attr}:${level}`)
    .join(' ')
  return `${scopes} — ${rendered}`
}
