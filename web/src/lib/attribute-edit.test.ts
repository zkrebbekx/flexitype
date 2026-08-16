import { describe, expect, it } from 'vitest'
import { ORDERED, TEXTUAL, buildCarriedUpdate, isTextual, loadPassthrough } from './attribute-edit'
import type { ComputedSpec, DefaultValue } from './api'

describe('a text attribute keeps its constraints (#591)', () => {
  it('treats text as textual, like the server does', () => {
    // domain/valueobjects/datatype.go IsTextual includes DataTypeText, so the
    // server accepts length and pattern constraints on it. The drawer said
    // otherwise, hid the inputs, and sent an empty list into a full replace.
    expect(isTextual('text')).toBe(true)
    expect(TEXTUAL).toContain('text')
  })

  it('still excludes a type that takes no length or pattern', () => {
    expect(isTextual('integer')).toBe(false)
    expect(isTextual('media')).toBe(false)
  })
})

describe('an edit does not delete what the drawer does not model (#592)', () => {
  const rollup: ComputedSpec = {
    kind: 'rollup',
    rollup: { relationship: 'has_line', direction: 'child', aggregate: 'sum', target: 'cost' },
  } as ComputedSpec
  const fallback: DefaultValue = { static: { type: 'text', value: 'hello' } } as DefaultValue

  it('hands a rollup back unchanged when the formula box is empty', () => {
    // The drawer models only a formula, so a rollup read as "no computed" and
    // the full replace turned a derived attribute into a plain writable one.
    const carried = loadPassthrough({ computed: rollup }, 'string')
    const body = buildCarriedUpdate('', [], carried)
    expect(body.computed).toEqual(rollup)
  })

  it('hands back a stored default the drawer never shows', () => {
    // A rename was enough to delete this.
    const carried = loadPassthrough({ default_value: fallback }, 'string')
    const body = buildCarriedUpdate('', [], carried)
    expect(body.default_value).toEqual(fallback)
  })

  it('lets a typed formula win over what was there', () => {
    const carried = loadPassthrough({ computed: rollup }, 'string')
    const body = buildCarriedUpdate('  price * 2 ', [], carried)
    expect(body.computed).toEqual({ kind: 'formula', formula: 'price * 2' })
  })

  it('sends no computed for an attribute that has none', () => {
    const body = buildCarriedUpdate('', [], loadPassthrough({}, 'string'))
    expect(body.computed).toBeUndefined()
    expect(body.default_value).toBeUndefined()
  })

  it('passes the constraints it was given through untouched', () => {
    const body = buildCarriedUpdate('', [{ kind: 'max_length', n: 10 }], loadPassthrough({}, 'string'))
    expect(body.constraints).toEqual([{ kind: 'max_length', n: 10 }])
  })
})

// Issue #597: an attribute update replaces the whole record, so a lost update
// loses fields the later writer never looked at.
describe('compare-and-swap baseline', () => {
  it('carries the version of the record the edit was based on', () => {
    const carried = loadPassthrough({ version: 4 }, 'string')
    expect(carried.version).toBe(4)
    expect(buildCarriedUpdate('', [], carried).version).toBe(4)
  })

  it('sends no version for a record that has none, keeping last-write-wins', () => {
    // A caller that never sent one must keep working; the swap is opt-in.
    expect(buildCarriedUpdate('', [], loadPassthrough(undefined, 'string')).version).toBeUndefined()
  })
})

