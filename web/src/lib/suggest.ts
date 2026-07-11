// Context-aware FQL suggestions. Pure and deterministic: given the query
// text, the cursor and the schema, produce the completions that are valid
// at that position.

import type { AttributeDefinition, EffectiveAttribute, RelationshipDefinition, TypeDefinition } from './api'

export interface SuggestSchema {
  attributes: EffectiveAttribute[]
  relationships: RelationshipDefinition[]
  /** relationship internal name → its (inherited) link attributes */
  linkAttributes: Record<string, AttributeDefinition[]>
  types: TypeDefinition[]
}

export interface Suggestion {
  /** Shown in the dropdown. */
  label: string
  /** Inserted at the word being completed. */
  insert: string
  detail?: string
  kind: 'attribute' | 'function' | 'keyword' | 'operator' | 'value' | 'relationship' | 'type'
}

export interface SuggestResult {
  items: Suggestion[]
  /** Byte offset the current word starts at; insertion replaces [replaceFrom, cursor). */
  replaceFrom: number
}

const FUNCTIONS: Suggestion[] = [
  { label: 'min(…)', insert: 'min(', detail: 'smallest value of a multi-valued attribute', kind: 'function' },
  { label: 'max(…)', insert: 'max(', detail: 'largest value of a multi-valued attribute', kind: 'function' },
  { label: 'count(…)', insert: 'count(', detail: 'number of values the entity holds', kind: 'function' },
  { label: 'length(…)', insert: 'length(', detail: 'character count of a textual value', kind: 'function' },
  { label: 'range(…, lo, hi)', insert: 'range(', detail: 'inclusive between', kind: 'function' },
  { label: 'has(…)', insert: 'has(', detail: 'the entity holds a value', kind: 'function' },
  { label: 'contains(…, "x")', insert: 'contains(', detail: 'substring match', kind: 'function' },
  { label: 'icontains(…, "x")', insert: 'icontains(', detail: 'case-insensitive substring', kind: 'function' },
  { label: 'iequals(…, "x")', insert: 'iequals(', detail: 'case-insensitive equality', kind: 'function' },
]

const ORDERED_OPS = ['>', '>=', '<', '<=']
const WORD_RE = /[A-Za-z0-9_.]/

interface analysis {
  word: string
  replaceFrom: number
  prev: string // previous significant token, lowercased
  prev2: string
  braceDepth: number
  enclosingRel?: string // nearest unclosed child(x){ / parent(x){
  inRelParens?: boolean // cursor inside child( … ) before the brace
}

function analyse(text: string, cursor: number): analysis {
  const upto = text.slice(0, cursor)

  // Current word being typed.
  let start = cursor
  while (start > 0 && WORD_RE.test(upto[start - 1]!)) start--
  const word = upto.slice(start, cursor)

  // Tokenise everything before the current word, tracking braces and the
  // enclosing traversal.
  const before = upto.slice(0, start)
  const tokens: string[] = []
  const relStack: string[] = []
  let braceDepth = 0
  let inRelParens = false
  const re = /"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|[A-Za-z_][A-Za-z0-9_.]*|-?\d+(?:\.\d+)?|>=|<=|!=|[=<>(){},.]/g
  let m: RegExpExecArray | null
  let pendingRel: string | null = null
  let lastKw = ''
  while ((m = re.exec(before))) {
    const t = m[0]
    tokens.push(t)
    const lower = t.toLowerCase()
    if (lower === 'child' || lower === 'parent') {
      lastKw = lower
    } else if (t === '(' && lastKw) {
      pendingRel = '' // next ident is the relationship name
      inRelParens = true
    } else if (pendingRel === '' && /^[a-z_]/i.test(t)) {
      pendingRel = t
    } else if (t === ')' && pendingRel !== null) {
      inRelParens = false
    } else if (t === '{') {
      braceDepth++
      if (pendingRel) relStack.push(pendingRel)
      pendingRel = null
      lastKw = ''
    } else if (t === '}') {
      braceDepth--
      relStack.pop()
    } else if (lower !== '(' && lower !== ')') {
      lastKw = ''
    }
  }

  const significant = tokens.filter((t) => t !== ',')
  return {
    word,
    replaceFrom: start,
    prev: (significant[significant.length - 1] ?? '').toLowerCase(),
    prev2: (significant[significant.length - 2] ?? '').toLowerCase(),
    braceDepth,
    enclosingRel: relStack[relStack.length - 1],
    inRelParens: inRelParens && pendingRel === '',
  }
}

export function suggest(text: string, cursor: number, schema: SuggestSchema): SuggestResult {
  const a = analyse(text, cursor)
  const items = candidates(a, schema).filter(
    (s) => !a.word || s.insert.toLowerCase().startsWith(a.word.toLowerCase()),
  )
  return { items: items.slice(0, 20), replaceFrom: a.replaceFrom }
}

function candidates(a: analysis, schema: SuggestSchema): Suggestion[] {
  const attrByName = new Map(schema.attributes.map((e) => [e.attribute.internal_name, e]))

  // Inside child( / parent( — complete relationship names.
  if (a.inRelParens || a.prev === 'child' || a.prev === 'parent') {
    return schema.relationships.map((r) => ({
      label: r.internal_name,
      insert: r.internal_name,
      detail: r.display_name,
      kind: 'relationship' as const,
    }))
  }

  // link.<attr> inside a traversal body.
  if (a.word.startsWith('link.') && a.enclosingRel) {
    const linkAttrs = schema.linkAttributes[a.enclosingRel] ?? []
    return linkAttrs.map((la) => ({
      label: `link.${la.internal_name}`,
      insert: `link.${la.internal_name}`,
      detail: `${la.display_name} (${la.data_type})`,
      kind: 'attribute' as const,
    }))
  }

  // After the virtual type field → its operators.
  if (a.prev === 'type') {
    return [
      { label: '=', insert: '=', detail: 'exact type', kind: 'operator' },
      { label: '!=', insert: '!=', kind: 'operator' },
      { label: 'isa', insert: 'isa', detail: 'the type or any descendant', kind: 'operator' },
      { label: 'in (…)', insert: 'in (', kind: 'operator' },
    ]
  }

  // After a type operator → type names.
  if ((a.prev === '=' || a.prev === '!=' || a.prev === 'isa') && a.prev2 === 'type') {
    return schema.types.map((t) => ({
      label: t.internal_name,
      insert: t.internal_name,
      detail: t.display_name,
      kind: 'type' as const,
    }))
  }

  // After an attribute name → its valid operators.
  const prevAttr = attrByName.get(a.prev) ?? linkAttr(a, schema)
  if (prevAttr) {
    const dt = 'attribute' in prevAttr ? prevAttr.attribute.data_type : prevAttr.data_type
    const ops: Suggestion[] = [
      { label: '=', insert: '=', kind: 'operator' },
      { label: '!=', insert: '!=', kind: 'operator' },
      { label: 'in (…)', insert: 'in (', kind: 'operator' },
    ]
    const ordered = ['integer', 'float', 'decimal', 'date', 'time', 'datetime'].includes(dt)
    if (ordered) {
      ops.push(...ORDERED_OPS.map((op) => ({ label: op, insert: op, kind: 'operator' as const })))
    }
    return ops
  }

  // After a comparison operator → value suggestions for the attribute.
  if (['=', '!=', '>', '>=', '<', '<=', 'in', '('].includes(a.prev)) {
    const attrName = a.prev === '(' || a.prev === 'in' ? a.prev2 : a.prev2
    const entry = attrByName.get(attrName)
    if (entry) {
      const dt = entry.attribute.data_type
      if (dt === 'bool') {
        return [
          { label: 'true', insert: 'true', kind: 'value' },
          { label: 'false', insert: 'false', kind: 'value' },
        ]
      }
      const oneOf = entry.attribute.constraints.find((c) => c.kind === 'one_of')
      if (oneOf?.values) {
        return oneOf.values.map((v) => ({
          label: `"${String(v.value)}"`,
          insert: `"${String(v.value)}"`,
          kind: 'value' as const,
        }))
      }
    }
    return []
  }

  // Expression start: attributes, functions, traversals, keywords.
  const startOfExpr = ['', 'and', 'or', 'not', '(', '{'].includes(a.prev)
  if (startOfExpr) {
    const out: Suggestion[] = schema.attributes.map((e) => ({
      label: e.attribute.internal_name,
      insert: e.attribute.internal_name,
      detail: `${e.attribute.display_name} (${e.attribute.data_type})${e.declared_in ? ` — ${e.declared_in.display_name}` : ''}`,
      kind: 'attribute' as const,
    }))
    out.push({ label: 'type', insert: 'type', detail: 'the entity’s declared type', kind: 'attribute' })
    if (a.braceDepth > 0 && a.enclosingRel) {
      for (const la of schema.linkAttributes[a.enclosingRel] ?? []) {
        out.push({
          label: `link.${la.internal_name}`,
          insert: `link.${la.internal_name}`,
          detail: `${la.display_name} (link attribute)`,
          kind: 'attribute',
        })
      }
    }
    out.push(...FUNCTIONS)
    out.push(
      { label: 'child(rel) { … }', insert: 'child(', detail: 'traverse to child-side entities', kind: 'function' },
      { label: 'parent(rel) { … }', insert: 'parent(', detail: 'traverse to parent-side entities', kind: 'function' },
      { label: 'not', insert: 'not', kind: 'keyword' },
    )
    return out
  }

  // After a complete condition → joiners.
  return [
    { label: 'and', insert: 'and', kind: 'keyword' },
    { label: 'or', insert: 'or', kind: 'keyword' },
  ]
}

function linkAttr(a: analysis, schema: SuggestSchema): AttributeDefinition | undefined {
  if (!a.prev.startsWith('link.') || !a.enclosingRel) return undefined
  const name = a.prev.slice('link.'.length)
  return (schema.linkAttributes[a.enclosingRel] ?? []).find((la) => la.internal_name === name)
}
