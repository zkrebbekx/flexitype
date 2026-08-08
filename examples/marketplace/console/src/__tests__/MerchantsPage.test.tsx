import { afterEach, describe, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

import MerchantsPage from '../pages/MerchantsPage.js'

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <MerchantsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('the merchants page', () => {
  it('lists what the platform returns', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        Response.json({
          items: [{ id: 'alpine', display_name: 'Alpine Apparel', tenant: 'alpine', created_at: '' }],
        }),
      ),
    )

    renderPage()

    expect(await screen.findByText('Alpine Apparel')).toBeInTheDocument()
    expect(screen.getByText('alpine')).toBeInTheDocument()
  })

  it('onboards a merchant and re-reads the list', async () => {
    const user = userEvent.setup()
    let onboarded = false
    const fetchMock = vi.fn(async (_url: string, init: RequestInit = {}) => {
      if (init.method === 'POST') {
        onboarded = true
        return Response.json({ id: 'bolt', display_name: 'Bolt', tenant: 'bolt', created_at: '' })
      }
      return Response.json({
        items: onboarded ? [{ id: 'bolt', display_name: 'Bolt', tenant: 'bolt', created_at: '' }] : [],
      })
    })
    vi.stubGlobal('fetch', fetchMock)

    renderPage()
    await screen.findByText(/No merchants yet/)

    await user.type(screen.getByLabelText('Merchant id'), 'bolt')
    await user.type(screen.getByLabelText('Display name'), 'Bolt')
    await user.click(screen.getByRole('button', { name: 'Onboard' }))

    expect(await screen.findByText('Bolt')).toBeInTheDocument()
  })

  it('shows the reason onboarding failed', async () => {
    const user = userEvent.setup()
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, init: RequestInit = {}) => {
        if (init.method === 'POST') {
          return Response.json({ error: { message: 'a merchant with that id already exists' } }, { status: 409 })
        }
        return Response.json({ items: [] })
      }),
    )

    renderPage()
    await screen.findByText(/No merchants yet/)

    await user.type(screen.getByLabelText('Merchant id'), 'alpine')
    await user.type(screen.getByLabelText('Display name'), 'Alpine')
    await user.click(screen.getByRole('button', { name: 'Onboard' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('already exists')
  })
})
