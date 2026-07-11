import { describe, expect, it } from 'vitest'
import { suggest } from './suggest'
import type { SuggestSchema } from './suggest'
import type { AttributeDefinition, EffectiveAttribute, RelationshipDefinition, TypeDefinition } from './api'

function attr(name: string, dt: AttributeDefinition['data_type'], oneOf?: string[]): EffectiveAttribute {
  return {
    attribute: {
      id: name,
      tenant_id: 'default',
      type_definition_id: 't',
      internal_name: name,
      display_name: name,
      data_type: dt,
      required: false,
      multi_valued: false,
      unique: false,
      constraints: oneOf ? [{ kind: 'one_of', values: oneOf.map((v) => ({ type: dt, value: v })) }] : [],
      version: 1,
      created_at: '',
      updated_at: '',
    },
    declared_in: { id: 't', tenant_id: 'default', internal_name: 'product', display_name: 'Product', version: 1, created_at: '', updated_at: '' } as TypeDefinition,
  }
}

const schema: SuggestSchema = {
  attributes: [attr('price', 'decimal'), attr('category', 'enum', ['bike', 'car']), attr('in_stock', 'bool')],
  relationships: [
    { internal_name: 'supplied_by', display_name: 'Supplied by' } as RelationshipDefinition,
  ],
  linkAttributes: {
    supplied_by: [
      { internal_name: 'lead_time_days', display_name: 'Lead time', data_type: 'integer' } as AttributeDefinition,
    ],
  },
  types: [
    { internal_name: 'product', display_name: 'Product' } as TypeDefinition,
    { internal_name: 'ebike', display_name: 'E-Bike' } as TypeDefinition,
  ],
}

function at(text: string): { text: string; cursor: number } {
  const cursor = text.indexOf('¦')
  return { text: text.replace('¦', ''), cursor }
}

function labels(text: string): string[] {
  const { text: t, cursor } = at(text)
  return suggest(t, cursor, schema).items.map((s) => s.insert)
}

describe('suggest', () => {
  it('offers attributes, functions and traversals at expression start', () => {
    const items = labels('¦')
    expect(items).toContain('price')
    expect(items).toContain('category')
    expect(items).toContain('min(')
    expect(items).toContain('child(')
  })

  it('filters by the typed prefix', () => {
    expect(labels('pri¦')).toEqual(['price'])
    expect(labels('cat¦')).toEqual(['category'])
  })

  it('offers ordered operators only for ordered attributes', () => {
    expect(labels('price ¦')).toContain('>=')
    expect(labels('category ¦')).not.toContain('>=')
    expect(labels('category ¦')).toContain('in (')
  })

  it('offers enum members and booleans as values', () => {
    expect(labels('category = ¦')).toEqual(['"bike"', '"car"'])
    expect(labels('in_stock = ¦')).toEqual(['true', 'false'])
  })

  it('offers type operators and type names', () => {
    expect(labels('type ¦')).toContain('isa')
    expect(labels('type isa ¦')).toContain('ebike')
  })

  it('offers relationship names inside traversal parens', () => {
    expect(labels('child(¦')).toEqual(['supplied_by'])
  })

  it('offers link attributes inside a traversal body', () => {
    const items = labels('child(supplied_by) { link.¦')
    expect(items).toContain('link.lead_time_days')
  })

  it('offers joiners after a complete condition', () => {
    const items = labels('price >= 10 ¦')
    expect(items).toEqual(['and', 'or'])
  })
})
