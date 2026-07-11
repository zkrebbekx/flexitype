// Client-side hierarchy assembly: the API returns a flat type list with
// extends_id pointers; the console renders it as an indented tree.

import type { TypeDefinition } from './api'

export interface TypeNode {
  type: TypeDefinition
  depth: number
  /** Whether this node is the last child of its parent (tree line shape). */
  isLast: boolean
  childCount: number
}

// buildTypeTree flattens the hierarchy depth-first for table rendering:
// roots in input order, children (sorted by display name) directly under
// their parent. Orphans whose parent is missing from the list (filtered
// out or archived) render as roots.
export function buildTypeTree(types: TypeDefinition[]): TypeNode[] {
  const present = new Set(types.map((t) => t.id))
  const children = new Map<string, TypeDefinition[]>()
  const roots: TypeDefinition[] = []

  for (const t of types) {
    if (t.extends_id && present.has(t.extends_id)) {
      const siblings = children.get(t.extends_id) ?? []
      siblings.push(t)
      children.set(t.extends_id, siblings)
    } else {
      roots.push(t)
    }
  }
  for (const siblings of children.values()) {
    siblings.sort((a, b) => a.display_name.localeCompare(b.display_name))
  }

  const out: TypeNode[] = []
  const visit = (t: TypeDefinition, depth: number, isLast: boolean, seen: Set<string>) => {
    if (seen.has(t.id)) return // cycle guard; the backend forbids cycles
    seen.add(t.id)
    const kids = children.get(t.id) ?? []
    out.push({ type: t, depth, isLast, childCount: kids.length })
    kids.forEach((k, i) => visit(k, depth + 1, i === kids.length - 1, seen))
  }
  const seen = new Set<string>()
  roots.forEach((r, i) => visit(r, 0, i === roots.length - 1, seen))
  return out
}

// ancestorsOf walks extends pointers upward within a flat list.
export function ancestorsOf(types: TypeDefinition[], id: string): TypeDefinition[] {
  const byId = new Map(types.map((t) => [t.id, t]))
  const out: TypeDefinition[] = []
  let current = byId.get(id)
  const seen = new Set<string>([id])
  while (current?.extends_id && !seen.has(current.extends_id)) {
    const parent = byId.get(current.extends_id)
    if (!parent) break
    out.push(parent)
    seen.add(parent.id)
    current = parent
  }
  return out
}
