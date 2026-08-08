import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toFormDescriptor } from '@flexitype/client'

import { DynamicForm, toFormState, type FormState } from '../components/DynamicForm.js'
import { productAttributes } from './fixtures.js'

/** A small harness that owns the form state, as the editor page does. */
function Harness({
  onSubmit,
  locale = '',
  initial = {},
}: {
  onSubmit: (values: Record<string, unknown>) => void
  locale?: string
  initial?: FormState
}) {
  const [state, setState] = useState<FormState>(initial)
  return (
    <DynamicForm
      descriptor={toFormDescriptor(productAttributes)}
      state={state}
      onChange={setState}
      locale={locale}
      onSubmit={onSubmit}
    />
  )
}

describe('the product form the schema draws', () => {
  it('renders a control for every attribute, including one the console never names', () => {
    render(<Harness onSubmit={vi.fn()} />)

    expect(screen.getByLabelText(/Name/)).toBeInTheDocument()
    expect(screen.getByLabelText(/Price/)).toBeInTheDocument()
    // The merchant's own subtype field, and where it came from.
    expect(screen.getByLabelText(/Voltage/)).toBeInTheDocument()
    expect(screen.getByText('from Electronics')).toBeInTheDocument()
  })

  it('picks the control from the data type', () => {
    render(<Harness onSubmit={vi.fn()} />)

    // An enum with a one_of constraint is a select carrying its choices.
    const status = screen.getByLabelText(/Status/) as HTMLSelectElement
    expect(status.tagName).toBe('SELECT')
    expect([...status.options].map((option) => option.value)).toEqual(['', 'draft', 'active'])

    expect((screen.getByLabelText(/In stock/) as HTMLInputElement).type).toBe('checkbox')
    expect((screen.getByLabelText(/Voltage/) as HTMLInputElement).type).toBe('number')
  })

  it('submits each value in the wire form its data type takes', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(<Harness onSubmit={onSubmit} />)

    await user.type(screen.getByLabelText(/Name/), 'Merino Base Layer')
    await user.selectOptions(screen.getByLabelText(/Status/), 'active')
    await user.type(screen.getByLabelText(/Price/), '89.50')
    await user.click(screen.getByLabelText(/In stock/))
    await user.type(screen.getByLabelText(/Voltage/), '240')
    await user.click(screen.getByRole('button', { name: /Save product/ }))

    expect(onSubmit).toHaveBeenCalledTimes(1)
    expect(onSubmit.mock.calls[0]?.[0]).toEqual({
      name: 'Merino Base Layer',
      status: 'active',
      // A decimal stays a STRING: sending 89.5 as a float would lose the
      // trailing zero and, at other magnitudes, digits.
      price: '89.50',
      in_stock: true,
      voltage: 240,
    })
  })

  it('refuses to send anything when one value cannot be its type', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(<Harness onSubmit={onSubmit} />)

    await user.type(screen.getByLabelText(/Name/), 'Bad Voltage')
    // A number input keeps a bare "e" — the exponent character — which is not
    // an integer. The SDK's toWire catches it before any request is made.
    await user.type(screen.getByLabelText(/Voltage/), '1e')
    await user.click(screen.getByRole('button', { name: /Save product/ }))

    expect(onSubmit).not.toHaveBeenCalled()
    expect(await screen.findByText(/Nothing was sent/)).toBeInTheDocument()
  })

  it('writes a localizable field into the chosen locale', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(<Harness onSubmit={onSubmit} locale="fr" />)

    await user.type(screen.getByLabelText(/Name/), 'Couche de base')
    await user.type(screen.getByLabelText(/Price/), '89.50')
    await user.click(screen.getByRole('button', { name: /Save product/ }))

    // name is localizable, so it is addressed by locale. price is not, so it
    // keeps its base key — a locale must not fork a value that has no locale.
    expect(onSubmit.mock.calls[0]?.[0]).toEqual({ 'name@fr': 'Couche de base', price: '89.50' })
  })

  it('sends nothing at all when every field is empty', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(<Harness onSubmit={onSubmit} />)

    await user.click(screen.getByRole('button', { name: /Save product/ }))

    expect(onSubmit).not.toHaveBeenCalled()
    expect(await screen.findByText(/Fill at least one field/)).toBeInTheDocument()
  })
})

describe('seeding the form from values an entity already holds', () => {
  it('round-trips a value through the editor without changing it', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    const descriptor = toFormDescriptor(productAttributes)
    const initial = toFormState(descriptor, {
      name: 'Merino Base Layer',
      price: '89.50',
      in_stock: true,
      voltage: 240,
      status: 'active',
    })

    render(<Harness onSubmit={onSubmit} initial={initial} />)
    await user.click(screen.getByRole('button', { name: /Save product/ }))

    expect(onSubmit.mock.calls[0]?.[0]).toEqual({
      name: 'Merino Base Layer',
      price: '89.50',
      in_stock: true,
      voltage: 240,
      status: 'active',
    })
  })
})
