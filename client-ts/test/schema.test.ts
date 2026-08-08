import { describe, expect, it } from 'vitest'
import type { Attribute, EffectiveAttribute, TypeDefinition } from '../src/models.js'
import {
  fieldKind,
  flattenConstraints,
  resolveEffectiveAttributes,
  toFormDescriptor,
  unwrapTyped,
  type TypeChainEntry,
} from '../src/softtype/schema.js'

function type(id: string, internalName: string, extendsId?: string): TypeDefinition {
  return {
    id,
    internal_name: internalName,
    display_name: internalName,
    version: 1,
    ...(extendsId === undefined ? {} : { extends_id: extendsId }),
  }
}

function attribute(partial: Partial<Attribute> & Pick<Attribute, 'id' | 'internal_name'>): Attribute {
  return {
    display_name: partial.internal_name ?? '',
    data_type: 'string',
    required: false,
    multi_valued: false,
    unique: false,
    version: 1,
    ...partial,
  }
}

// product extends item extends base. The service returns a chain leaf first.
const base = type('T-base', 'base')
const item = type('T-item', 'item', 'T-base')
const product = type('T-product', 'product', 'T-item')

const chain: TypeChainEntry[] = [
  {
    type: product,
    attributes: [
      attribute({ id: 'A-sku', internal_name: 'sku', group: 'identity', sort_order: 1 }),
      attribute({ id: 'A-price', internal_name: 'price', data_type: 'decimal', group: 'commerce', sort_order: 1 }),
    ],
  },
  {
    type: item,
    attributes: [
      attribute({ id: 'A-name', internal_name: 'name', group: 'identity', sort_order: 2, required: true }),
      attribute({ id: 'A-old', internal_name: 'legacy', archived_at: '2026-01-01T00:00:00Z' }),
    ],
  },
  { type: base, attributes: [attribute({ id: 'A-id', internal_name: 'external_id' })] },
]

describe('effective attributes through inheritance', () => {
  it('collects the leaf type’s own attributes and every ancestor’s', () => {
    const resolved = resolveEffectiveAttributes(chain)
    expect(resolved.map((e) => e.attribute?.internal_name)).toEqual([
      // The ungrouped attribute sorts first, then the named groups in order.
      'external_id',
      'price',
      'sku',
      'name',
    ])
  })

  it('records the type that declares each attribute, for "inherited from X"', () => {
    const resolved = resolveEffectiveAttributes(chain)
    const byName = new Map(resolved.map((e) => [e.attribute?.internal_name, e.declared_in?.internal_name]))
    expect(byName.get('sku')).toBe('product')
    expect(byName.get('name')).toBe('item')
    expect(byName.get('external_id')).toBe('base')
  })

  it('drops an archived attribute unless the caller asks for it', () => {
    expect(resolveEffectiveAttributes(chain).map((e) => e.attribute?.internal_name)).not.toContain('legacy')
    expect(
      resolveEffectiveAttributes(chain, { includeArchived: true }).map((e) => e.attribute?.internal_name),
    ).toContain('legacy')
  })

  it('resolves a duplicate name in favour of the declaration nearest the leaf', () => {
    // The service refuses a name already declared anywhere in the hierarchy,
    // so this only arises in a chain a client assembled from stale reads.
    const shadowed: TypeChainEntry[] = [
      { type: product, attributes: [attribute({ id: 'A-new', internal_name: 'sku', display_name: 'SKU (new)' })] },
      { type: item, attributes: [attribute({ id: 'A-old', internal_name: 'sku', display_name: 'SKU (old)' })] },
    ]
    const resolved = resolveEffectiveAttributes(shadowed)
    expect(resolved).toHaveLength(1)
    expect(resolved[0]?.attribute?.display_name).toBe('SKU (new)')
  })

  it('orders by group, then by sort_order, keeping equal ones in arrival order', () => {
    const ordered = resolveEffectiveAttributes([
      {
        type: product,
        attributes: [
          attribute({ id: '1', internal_name: 'b', group: 'z', sort_order: 1 }),
          attribute({ id: '2', internal_name: 'a', group: 'a', sort_order: 5 }),
          attribute({ id: '3', internal_name: 'c', group: 'a', sort_order: 1 }),
          attribute({ id: '4', internal_name: 'd', group: 'a', sort_order: 5 }),
        ],
      },
    ])
    expect(ordered.map((e) => e.attribute?.internal_name)).toEqual(['c', 'a', 'd', 'b'])
  })
})

describe('the form descriptor', () => {
  const effective: EffectiveAttribute[] = resolveEffectiveAttributes([
    {
      type: product,
      attributes: [
        attribute({
          id: 'A-sku',
          internal_name: 'sku',
          display_name: 'SKU',
          required: true,
          unique: true,
          group: 'identity',
          help_text: 'The stock keeping unit',
          constraints: [
            { kind: 'min_length', n: 3 },
            { kind: 'max_length', n: 32 },
            { kind: 'pattern', expr: '^[A-Z]+-\\d+$' },
          ],
        }),
        attribute({
          id: 'A-status',
          internal_name: 'status',
          data_type: 'enum',
          group: 'identity',
          sort_order: 2,
          constraints: [
            {
              kind: 'one_of',
              values: [
                { type: 'enum', value: 'draft' },
                { type: 'enum', value: 'active' },
              ],
            },
          ],
        }),
        attribute({
          id: 'A-weight',
          internal_name: 'weight',
          data_type: 'quantity',
          unit_family_id: 'U-1',
          display_unit: 'kg',
          group: 'physical',
        }),
        attribute({
          id: 'A-name',
          internal_name: 'name',
          localizable: true,
          scopable: true,
          group: 'identity',
          sort_order: 3,
        }),
        attribute({
          id: 'A-total',
          internal_name: 'total',
          data_type: 'decimal',
          group: 'physical',
          sort_order: 2,
          computed: { kind: 'formula', formula: 'weight * 2' },
        }),
      ],
    },
  ])

  it('turns every attribute into a renderable field', () => {
    const form = toFormDescriptor(effective)
    expect(form.fields.map((f) => f.name)).toEqual(['sku', 'status', 'name', 'weight', 'total'])
    expect(Object.keys(form.byName).sort()).toEqual(['name', 'sku', 'status', 'total', 'weight'])
    expect(form.byId['A-sku']?.name).toBe('sku')
  })

  it('groups the fields in the order the service returns them', () => {
    const form = toFormDescriptor(effective)
    expect(form.groups.map((g) => g.name)).toEqual(['identity', 'physical'])
    expect(form.groups[0]?.fields.map((f) => f.name)).toEqual(['sku', 'status', 'name'])
  })

  it('picks a control per data type', () => {
    const form = toFormDescriptor(effective)
    expect(form.byName.sku?.kind).toBe('text')
    expect(form.byName.status?.kind).toBe('select')
    expect(form.byName.weight?.kind).toBe('quantity')
    expect(form.byName.total?.kind).toBe('decimal')
    expect(fieldKind('media')).toBe('file')
    expect(fieldKind('bool')).toBe('checkbox')
    expect(fieldKind('json')).toBe('json')
    expect(fieldKind('datetime')).toBe('datetime')
    // Any attribute a dependency has narrowed becomes a select.
    expect(fieldKind('string', true)).toBe('select')
  })

  it('draws a text area for long-form text, and a line for a string', () => {
    // The one thing a renderer cannot infer from a string with a large
    // max_length: whether the value is long. `text` declares it.
    expect(fieldKind('text')).toBe('textarea')
    expect(fieldKind('string')).toBe('text')
    // A narrowed text attribute is still a choice, not free text.
    expect(fieldKind('text', true)).toBe('select')
  })

  it('reads the choices out of a one_of constraint', () => {
    const form = toFormDescriptor(effective)
    expect(form.byName.status?.options).toEqual([
      { value: 'draft', label: 'draft' },
      { value: 'active', label: 'active' },
    ])
  })

  it('lets the caller label an option', () => {
    const form = toFormDescriptor(effective, { optionLabel: (value) => String(value).toUpperCase() })
    expect(form.byName.status?.options?.[0]).toEqual({ value: 'draft', label: 'DRAFT' })
  })

  it('flattens the constraints a control has to enforce', () => {
    const form = toFormDescriptor(effective)
    expect(form.byName.sku?.constraints).toEqual({ minLength: 3, maxLength: 32, pattern: '^[A-Z]+-\\d+$' })
  })

  it('carries the scope flags, so a form knows it needs a locale selector', () => {
    const form = toFormDescriptor(effective)
    expect(form.byName.name?.localizable).toBe(true)
    expect(form.byName.name?.scopable).toBe(true)
    expect(form.byName.sku?.localizable).toBe(false)
  })

  it('marks a computed attribute read-only, so a form shows it and refuses to edit it', () => {
    const form = toFormDescriptor(effective)
    expect(form.byName.total?.readOnly).toBe(true)
    expect(form.byName.sku?.readOnly).toBe(false)
  })

  it('carries the unit family a quantity field draws its units from', () => {
    const form = toFormDescriptor(effective)
    expect(form.byName.weight?.unitFamilyId).toBe('U-1')
    expect(form.byName.weight?.displayUnit).toBe('kg')
  })

  it('lets a dependency make a field required and narrow its choices', () => {
    // This is what GET .../effective-schema answers for one entity, and it is
    // the difference between the static schema and this entity's real rules.
    const form = toFormDescriptor(effective, {
      overrides: {
        'A-sku': { attribute_definition_id: 'A-sku', entity_id: 'e1', required: true, restricted: false },
        'A-name': {
          attribute_definition_id: 'A-name',
          entity_id: 'e1',
          required: true,
          restricted: true,
          allowed_values: [{ type: 'string', value: 'Widget' }],
        },
      },
    })
    expect(form.byName.name?.required).toBe(true)
    expect(form.byName.name?.restricted).toBe(true)
    expect(form.byName.name?.options).toEqual([{ value: 'Widget', label: 'Widget' }])
    expect(form.byName.name?.kind).toBe('select')
    // An attribute with no override keeps the definition's own rules.
    expect(form.byName.status?.required).toBe(false)
  })
})

describe('typed values and constraints', () => {
  it('unwraps a self-describing value and passes a bare one through', () => {
    expect(unwrapTyped({ type: 'integer', value: 42 })).toBe(42)
    expect(unwrapTyped(42)).toBe(42)
    expect(unwrapTyped(undefined)).toBeUndefined()
  })

  it('flattens every constraint kind a control cares about', () => {
    expect(
      flattenConstraints([
        { kind: 'min_value', value: { type: 'integer', value: 1 } },
        { kind: 'max_value', value: { type: 'integer', value: 9 } },
        { kind: 'pattern', expr: 'abc', substring: true },
        { kind: 'media', mime: ['image/png'], max_size: 1024 },
      ]),
    ).toEqual({ min: 1, max: 9, pattern: 'abc', patternSubstring: true, mime: ['image/png'], maxSize: 1024 })
  })
})
