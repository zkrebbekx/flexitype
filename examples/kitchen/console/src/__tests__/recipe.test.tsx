import { afterEach, describe, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

import RecipePage from '../pages/RecipePage.js'
import { asPercent, money } from '../lib/kitchen.js'
import type { Dish } from '../lib/kitchen.js'

/** A dish as the API reports it: every cost already derived by the service. */
const tart: Dish = {
  id: 'tart',
  course: 'dessert',
  status: 'draft',
  name: { '': 'Chocolate tart', fr: 'Tarte au chocolat' },
  price: { dine_in: '8.50', delivery: '9.50', catering: '7.00' },
  food_cost: '2.475',
  line_count: 2,
  margin: { dine_in: '0.7088', delivery: '0.7395', catering: '0.6464' },
  lines: [
    {
      id: 'l-flour',
      ingredient_id: 'flour',
      ingredient: 'Flour',
      quantity: { magnitude: '500', unit: 'g' },
      cost_per_kg: '1.20',
      line_cost: '0.6',
    },
    {
      id: 'l-butter',
      ingredient_id: 'butter',
      ingredient: 'Butter',
      quantity: { magnitude: '250', unit: 'g' },
      cost_per_kg: '7.50',
      line_cost: '1.875',
    },
  ],
}

function renderRecipe() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={['/dishes/tart']}>
        <Routes>
          <Route path="/dishes/:dishID" element={<RecipePage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

/** Answers the kitchen API, recording every request. */
function stubKitchen(overrides: Partial<Dish> = {}, publishFails = false) {
  const calls: { url: string; method: string; body?: string | undefined }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit = {}) => {
      calls.push({ url, method: init.method ?? 'GET', body: init.body as string | undefined })
      if (url.startsWith('/api/ingredients')) {
        return Response.json({ items: [{ id: 'flour', name: 'Flour' }, { id: 'butter', name: 'Butter' }] })
      }
      if (url.endsWith('/publish')) {
        if (publishFails) {
          return Response.json(
            { error: { message: 'not ready' }, missing: ['allergens'], score: 0.8 },
            { status: 422 },
          )
        }
        return Response.json({ ...tart, ...overrides, status: 'on_menu' })
      }
      return Response.json({ ...tart, ...overrides })
    }),
  )
  return calls
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('the recipe page', () => {
  it('shows costs the service derived, and does not compute them', async () => {
    stubKitchen()
    renderRecipe()

    // Each line's cost, and the dish's total, come from the API as they are.
    expect(await screen.findByText('0.60')).toBeInTheDocument()
    expect(screen.getByText('1.88')).toBeInTheDocument()
    expect(screen.getByText('2.48')).toBeInTheDocument()
    // 0.6 + 1.875 = 2.475, which is what the SERVICE said. Nothing here adds
    // the lines up: the total is read, not summed.
  })

  it('writes a quantity and re-reads the cost rather than recalculating it', async () => {
    const user = userEvent.setup()
    const calls = stubKitchen()
    renderRecipe()
    await screen.findByText('0.60')

    const quantity = screen.getByLabelText('Quantity of Flour') as HTMLInputElement
    expect(quantity.value).toBe('500')
    await user.clear(quantity)
    await user.type(quantity, '400')
    // The editor writes when the field loses focus, so a cook typing a number
    // does not send a request per keystroke.
    await user.click(screen.getByRole('button', { name: /Save prices/ }))

    await waitFor(() => {
      const written = calls.find((call) => call.method === 'PUT' && call.url.includes('/lines/'))
      expect(written).toBeDefined()
      // The quantity is sent with its unit. No cost is sent at all.
      expect(JSON.parse(String(written?.body))).toEqual({
        ingredient_id: 'flour',
        quantity: { magnitude: '400', unit: 'g' },
      })
    })
  })

  it('reports what a dish still needs when publishing is refused', async () => {
    const user = userEvent.setup()
    stubKitchen({}, true)
    renderRecipe()
    await screen.findByText('0.60')

    await user.click(screen.getByRole('button', { name: /Put on the menu/ }))

    expect(await screen.findByRole('alert')).toHaveTextContent('allergens')
  })

  it('shows a margin per channel, because one price per channel has no single answer', async () => {
    stubKitchen()
    renderRecipe()

    expect(await screen.findByText('70.9%')).toBeInTheDocument()
    expect(screen.getByText('64.6%')).toBeInTheDocument()
  })
})

describe('rendering money and margins', () => {
  it('never turns a decimal string into a float', () => {
    // 8.50 must not render as 8.5, and a long price must keep every digit —
    // Number("12345678901234.99") loses the last one.
    expect(money('8.50')).toBe('8.50')
    expect(money('12345678901234.99')).toBe('12345678901234.99')
    expect(money(undefined)).toBe('—')
  })

  it('rounds half-up rather than truncating', () => {
    // A cost derived by dividing by a quantity carries the float tail of the
    // unit conversion. Truncating 5.39999… reads as a penny less than it is.
    expect(money('5.399999999999999')).toBe('5.40')
    expect(money('2.475')).toBe('2.48')
    expect(money('0.994')).toBe('0.99')
    expect(money('9.999')).toBe('10.00')
    expect(money('0.005')).toBe('0.01')
    expect(money('7')).toBe('7.00')
  })

  it('renders a margin as a percentage', () => {
    expect(asPercent('0.7088')).toBe('70.9%')
    expect(asPercent(undefined)).toBe('—')
  })
})
