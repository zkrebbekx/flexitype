import { describe, expect, it } from 'vitest'
import { TEXTUAL, buildCarriedUpdate, carriedFields, isTextual } from './attribute-edit'
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
    const carried = carriedFields({ computed: rollup })
    const body = buildCarriedUpdate('', [], carried)
    expect(body.computed).toEqual(rollup)
  })

  it('hands back a stored default the drawer never shows', () => {
    // A rename was enough to delete this.
    const carried = carriedFields({ default_value: fallback })
    const body = buildCarriedUpdate('', [], carried)
    expect(body.default_value).toEqual(fallback)
  })

  it('lets a typed formula win over what was there', () => {
    const carried = carriedFields({ computed: rollup })
    const body = buildCarriedUpdate('  price * 2 ', [], carried)
    expect(body.computed).toEqual({ kind: 'formula', formula: 'price * 2' })
  })

  it('sends no computed for an attribute that has none', () => {
    const body = buildCarriedUpdate('', [], carriedFields({}))
    expect(body.computed).toBeUndefined()
    expect(body.default_value).toBeUndefined()
  })

  it('passes the constraints it was given through untouched', () => {
    const body = buildCarriedUpdate('', [{ kind: 'max_length', n: 10 }], carriedFields({}))
    expect(body.constraints).toEqual([{ kind: 'max_length', n: 10 }])
  })
})
