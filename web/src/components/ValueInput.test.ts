import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { VueQueryPlugin } from '@tanstack/vue-query'
import ValueInput from './ValueInput.vue'
import { api } from '@/lib/api'
import { toApiValue } from '@/lib/values'

function mountQuantity() {
  return mount(ValueInput, {
    global: {
      plugins: [[VueQueryPlugin, { queryClientConfig: { defaultOptions: { queries: { retry: false } } } }]],
    },
    props: { dataType: 'quantity' as const, unitFamilyId: 'fam1', displayUnit: 'kg' },
  })
}

describe('ValueInput (quantity)', () => {
  it('sources units from the attribute unit family and emits {magnitude, unit}', async () => {
    vi.spyOn(api, 'getUnitFamily').mockResolvedValue({
      id: 'fam1',
      tenant_id: 't',
      name: 'Mass',
      base_unit: 'g',
      units: { g: 1, kg: 1000, lb: 453.592 },
    })

    const wrapper = mountQuantity()
    await flushPromises()

    // The unit dropdown lists exactly the family's units and defaults to the
    // attribute's display unit.
    const units = wrapper.findAll('option').map((o) => (o.element as HTMLOptionElement).value)
    expect(units).toEqual(['g', 'kg', 'lb'])
    expect((wrapper.find('select').element as HTMLSelectElement).value).toBe('kg')

    // Typing a magnitude yields a payload of {magnitude, unit}.
    await wrapper.find('input[inputmode="decimal"]').setValue('2.5')
    const emitted = wrapper.emitted('update:modelValue') as string[][]
    expect(toApiValue('quantity', emitted[emitted.length - 1][0])).toEqual({ magnitude: '2.5', unit: 'kg' })

    // Switching the unit re-emits with the new unit.
    await wrapper.find('select').setValue('lb')
    const after = wrapper.emitted('update:modelValue') as string[][]
    expect(toApiValue('quantity', after[after.length - 1][0])).toEqual({ magnitude: '2.5', unit: 'lb' })
  })
})
