import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import DiffView from './DiffView.vue'

describe('DiffView', () => {
  it('highlights changed fields and hides unchanged ones behind a toggle', async () => {
    const wrapper = mount(DiffView, {
      props: {
        before: { display_name: 'Old', version: 1, internal_name: 'thing' },
        after: { display_name: 'New', version: 2, internal_name: 'thing' },
      },
    })

    const text = wrapper.text()
    expect(text).toContain('display_name')
    expect(text).toContain('"Old"')
    expect(text).toContain('"New"')
    // Unchanged field is collapsed by default.
    expect(text).not.toContain('"thing"')
    expect(text).toContain('1 unchanged field')

    await wrapper.find('button').trigger('click')
    expect(wrapper.text()).toContain('"thing"')
  })

  it('renders creation diffs with an empty before side', () => {
    const wrapper = mount(DiffView, {
      props: { after: { internal_name: 'product' } },
    })
    expect(wrapper.text()).toContain('internal_name')
    expect(wrapper.text()).toContain('—')
  })
})
