import { describe, expect, it } from 'vitest'
import type { AttributeValue } from '../src/models.js'
import {
  BASE_SCOPE,
  isBaseScope,
  sameScope,
  ScopedValues,
  scopedValueInput,
  scopeKey,
  scopeOf,
} from '../src/softtype/scoped.js'

function value(partial: Partial<AttributeValue>): AttributeValue {
  return { attribute_definition_id: 'A-name', entity_id: 'e1', value: 'x', ...partial }
}

// One entity's rows: a localizable name in three scopes, and a plain sku.
const rows: AttributeValue[] = [
  value({ id: 'v1', value: 'Widget' }),
  value({ id: 'v2', locale: 'fr', value: 'Bidule' }),
  value({ id: 'v3', locale: 'fr', channel: 'web', value: 'Bidule (web)' }),
  value({ id: 'v4', attribute_definition_id: 'A-sku', value: 'ABC-1' }),
  value({ id: 'v5', attribute_definition_id: 'A-tags', value: 'red' }),
  value({ id: 'v6', attribute_definition_id: 'A-tags', value: 'blue' }),
]

describe('value scopes', () => {
  it('keys the base scope as the empty string', () => {
    expect(scopeKey(BASE_SCOPE)).toBe('')
    expect(scopeKey()).toBe('')
    expect(isBaseScope({})).toBe(true)
    expect(isBaseScope({ locale: 'fr' })).toBe(false)
  })

  it('gives a distinct key to every combination', () => {
    const keys = new Set([
      scopeKey({}),
      scopeKey({ locale: 'fr' }),
      scopeKey({ channel: 'web' }),
      scopeKey({ locale: 'fr', channel: 'web' }),
    ])
    expect(keys.size).toBe(4)
  })

  it('reads the scope of a stored row, treating an empty string as absent', () => {
    expect(scopeOf(value({ locale: 'fr', channel: 'web' }))).toEqual({ locale: 'fr', channel: 'web' })
    expect(scopeOf(value({ locale: '', channel: '' }))).toEqual({})
    expect(sameScope({ locale: 'fr' }, { locale: 'fr' })).toBe(true)
    expect(sameScope({ locale: 'fr' }, { locale: 'de' })).toBe(false)
  })
})

describe('ScopedValues', () => {
  const values = new ScopedValues(rows)

  it('addresses a value by attribute AND scope', () => {
    // Keying by attribute id alone is how a screen shows one locale's value
    // under another's label.
    expect(values.get('A-name')?.value).toBe('Widget')
    expect(values.get('A-name', { locale: 'fr' })?.value).toBe('Bidule')
    expect(values.get('A-name', { locale: 'fr', channel: 'web' })?.value).toBe('Bidule (web)')
  })

  it('reports nothing for a scope that holds no value', () => {
    expect(values.get('A-name', { locale: 'de' })).toBeUndefined()
    expect(values.get('A-missing')).toBeUndefined()
  })

  it('falls back to the base scope only when the caller asks', () => {
    expect(values.get('A-name', { locale: 'de' })).toBeUndefined()
    expect(values.get('A-name', { locale: 'de' }, { fallbackToBase: true })?.value).toBe('Widget')
    // A scope that holds its own value is never replaced by the base one.
    expect(values.get('A-name', { locale: 'fr' }, { fallbackToBase: true })?.value).toBe('Bidule')
  })

  it('returns every value of a multi-valued attribute', () => {
    expect(values.all('A-tags').map((v) => v.value)).toEqual(['red', 'blue'])
  })

  it('lists the scopes an attribute holds, and the locales and channels in use', () => {
    expect(values.scopesOf('A-name')).toEqual([{}, { locale: 'fr' }, { locale: 'fr', channel: 'web' }])
    expect(values.locales()).toEqual(['fr'])
    expect(values.channels()).toEqual(['web'])
    expect(values.attributeIds().sort()).toEqual(['A-name', 'A-sku', 'A-tags'])
  })

  it('reads a payload directly for a renderer that only needs the value', () => {
    expect(values.valueOf('A-sku')).toBe('ABC-1')
    expect(values.valueOf('A-name', { locale: 'fr' })).toBe('Bidule')
  })
})

describe('scopedValueInput', () => {
  it('builds a write body carrying the scope', () => {
    expect(
      scopedValueInput({
        attributeDefinitionId: 'A-name',
        entityId: 'e1',
        typeDefinitionId: 'T-1',
        value: 'Bidule',
        scope: { locale: 'fr' },
      }),
    ).toEqual({
      attribute_definition_id: 'A-name',
      entity_id: 'e1',
      type_definition_id: 'T-1',
      value: 'Bidule',
      locale: 'fr',
    })
  })

  it('drops an empty locale or channel rather than sending it', () => {
    // The service refuses a locale on a non-localizable attribute, and an
    // unset form field is the usual source of an empty string.
    expect(
      scopedValueInput({
        attributeDefinitionId: 'A-sku',
        entityId: 'e1',
        value: 'ABC-1',
        scope: { locale: '', channel: '' },
      }),
    ).toEqual({ attribute_definition_id: 'A-sku', entity_id: 'e1', value: 'ABC-1' })
  })
})
