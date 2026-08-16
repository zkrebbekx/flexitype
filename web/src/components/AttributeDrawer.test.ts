import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { VueQueryPlugin } from '@tanstack/vue-query'
import AttributeDrawer from './AttributeDrawer.vue'
import { api } from '@/lib/api'
import type { AttributeDefinition } from '@/lib/api'

/**
 * These mount the real drawer and inspect the body it sends.
 *
 * The unit tests beside `attribute-edit.ts` call the helpers directly, and a
 * complete set of them passed while the drawer computed the compare-and-swap
 * version and then never assigned it — so every attribute save was still
 * last-write-wins and the 409 branch was dead code. A helper being correct
 * says nothing about the component calling it. This is the level that catches
 * that, so it is the level the full-replace rule is guarded at.
 */

const attribute = (over: Partial<AttributeDefinition> = {}): AttributeDefinition =>
  ({
    id: 'a1',
    tenant_id: 'default',
    type_definition_id: 't1',
    internal_name: 'sku',
    display_name: 'SKU',
    data_type: 'string',
    required: false,
    multi_valued: false,
    unique: false,
    localizable: false,
    scopable: false,
    version: 7,
    constraints: [],
    created_at: '',
    updated_at: '',
    ...over,
  }) as AttributeDefinition

function mountDrawer(attr: AttributeDefinition) {
  // The drawer teleports to <body>, which persists between tests — without
  // this, every case submitted the first case's form.
  document.body.innerHTML = ''
  return mount(AttributeDrawer, {
    global: {
      plugins: [[VueQueryPlugin, { queryClientConfig: { defaultOptions: { queries: { retry: false } } } }]],
    },
    props: { open: true, typeId: 't1', attribute: attr },
  })
}

async function saveAndCaptureBody(attr: AttributeDefinition) {
  const update = vi.spyOn(api, 'updateAttribute').mockResolvedValue(attr)
  vi.spyOn(api, 'listUnitFamilies').mockResolvedValue({ items: [] })
  const wrapper = mountDrawer(attr)
  await flushPromises()
  // The drawer body is teleported to <body>, so the form is not under the
  // wrapper's own root.
  const form = document.body.querySelector('form')
  if (!form) throw new Error('the drawer rendered no form')
  form.dispatchEvent(new Event('submit'))
  await flushPromises()
  expect(update).toHaveBeenCalled()
  const body = update.mock.calls[0]![1]
  wrapper.unmount()
  return body
}

describe('AttributeDrawer save body', () => {
  it('sends the version it loaded, so the compare-and-swap is armed', async () => {
    const body = await saveAndCaptureBody(attribute({ version: 7 }))
    expect(body.version).toBe(7)
  })

  it('keeps a media constraint it has no input for', async () => {
    // The drawer says "media attributes have no additional constraints", so
    // without carrying it the MIME allow-list and size cap were deleted and
    // the attribute began accepting any upload of any size.
    const media = { kind: 'media' as const, mime: ['image/png'], max_size: 1024 }
    const body = await saveAndCaptureBody(attribute({ data_type: 'media', constraints: [media] }))
    expect(body.constraints).toContainEqual(media)
  })

  it('keeps a one_of allow-list on a type that is not an enum', async () => {
    // The server accepts one_of on any type whose members match; the drawer
    // only edits it for enums, and dropped it everywhere else.
    const oneOf = { kind: 'one_of' as const, values: [{ type: 'string' as const, value: 'a' }] }
    const body = await saveAndCaptureBody(attribute({ data_type: 'string', constraints: [oneOf] }))
    expect(body.constraints).toContainEqual(oneOf)
  })

  it('keeps the bounds on a quantity attribute', async () => {
    // ORDERED omitted `quantity`, so the min/max inputs never rendered and the
    // full replace then deleted the bound the attribute had.
    const min = {
      kind: 'min_value' as const,
      value: { type: 'quantity' as const, value: { magnitude: '1', unit: 'kg' } },
    }
    const body = await saveAndCaptureBody(
      attribute({ data_type: 'quantity', constraints: [min], unit_family_id: 'f1' }),
    )
    expect(body.constraints).toHaveLength(1)
    expect(body.constraints![0]!.kind).toBe('min_value')
  })

  it('keeps a pattern anchored the way it was loaded', async () => {
    const body = await saveAndCaptureBody(
      attribute({ constraints: [{ kind: 'pattern', expr: '^a', substring: true }] }),
    )
    expect(body.constraints).toContainEqual({ kind: 'pattern', expr: '^a', substring: true })
  })

  it('keeps a rollup rather than converting it to a plain attribute', async () => {
    const rollup = { kind: 'rollup' as const, rollup: { relationship: 'r', aggregate: 'count' as const } }
    const body = await saveAndCaptureBody(attribute({ computed: rollup }))
    expect(body.computed).toEqual(rollup)
  })

  it('keeps a stored default value through a rename', async () => {
    const fallback = { static: { type: 'string' as const, value: 'n/a' } }
    const body = await saveAndCaptureBody(attribute({ default_value: fallback }))
    expect(body.default_value).toEqual(fallback)
  })
})
