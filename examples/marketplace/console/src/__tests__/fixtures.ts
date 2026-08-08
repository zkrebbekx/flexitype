import type { EffectiveAttribute } from '@flexitype/client'

/**
 * A product schema shaped like the one the `ecommerce` template applies, plus
 * one field a merchant added to its own subtype.
 *
 * The point of the tests that use it: the console knows none of these names.
 */
export const productAttributes: EffectiveAttribute[] = [
  {
    attribute: {
      id: 'attr-name',
      internal_name: 'name',
      display_name: 'Name',
      data_type: 'string',
      required: true,
      localizable: true,
      sort_order: 10,
      constraints: [{ kind: 'max_length', n: 200 }],
    },
    declared_in: { id: 'type-product', internal_name: 'product', display_name: 'Product' },
  },
  {
    attribute: {
      id: 'attr-status',
      internal_name: 'status',
      display_name: 'Status',
      data_type: 'enum',
      sort_order: 40,
      constraints: [
        {
          kind: 'one_of',
          values: [
            { type: 'enum', value: 'draft' },
            { type: 'enum', value: 'active' },
          ],
        },
      ],
    },
    declared_in: { id: 'type-product', internal_name: 'product', display_name: 'Product' },
  },
  {
    attribute: {
      id: 'attr-price',
      internal_name: 'price',
      display_name: 'Price',
      data_type: 'decimal',
      sort_order: 50,
    },
    declared_in: { id: 'type-product', internal_name: 'product', display_name: 'Product' },
  },
  {
    attribute: {
      id: 'attr-in-stock',
      internal_name: 'in_stock',
      display_name: 'In stock',
      data_type: 'bool',
      sort_order: 70,
    },
    declared_in: { id: 'type-product', internal_name: 'product', display_name: 'Product' },
  },
  {
    // The field only THIS merchant has. Nothing in the console names it.
    attribute: {
      id: 'attr-voltage',
      internal_name: 'voltage',
      display_name: 'Voltage',
      data_type: 'integer',
      sort_order: 100,
      help_text: 'Mains voltage the product expects.',
    },
    declared_in: { id: 'type-electronics', internal_name: 'electronics', display_name: 'Electronics' },
  },
]
