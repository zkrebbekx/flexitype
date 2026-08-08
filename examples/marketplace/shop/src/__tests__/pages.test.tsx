import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

import BrowsePage from '../pages/BrowsePage.js'
import ProductPage from '../pages/ProductPage.js'
import type { Product } from '../lib/storefront.js'

/** A product of a subtype this app has never heard of. */
const electronics: Product = {
  tenant: 'bolt',
  merchant: 'Bolt Electronics',
  entity_id: 'bolt-kettle-1',
  subtype: 'electronics',
  name: 'Travel Kettle',
  description: 'Boils half a litre.',
  sku: 'BOLT-9',
  status: 'active',
  price: '39.00',
  currency: 'EUR',
  in_stock: true,
  attributes: { voltage: 240, warranty_months: 24 },
  updated_at: '2026-08-01T00:00:00Z',
}

const apparel: Product = {
  tenant: 'alpine',
  merchant: 'Alpine Apparel',
  entity_id: 'alp-merino-1',
  subtype: 'apparel',
  name: 'Merino Base Layer',
  description: '',
  sku: 'ALP-1',
  status: 'active',
  price: '89.50',
  currency: 'EUR',
  attributes: { size: 'M', colour: 'slate' },
  updated_at: '2026-08-01T00:00:00Z',
}

function renderWith(ui: React.ReactNode, path = '/') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[path]}>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )
}

/** Answers the shopper API from a fixed catalog, recording every URL. */
function stubCatalog(products: Product[]) {
  const urls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      urls.push(url)
      if (url.startsWith('/api/merchants')) {
        return Response.json({
          items: [
            { tenant: 'alpine', display_name: 'Alpine Apparel' },
            { tenant: 'bolt', display_name: 'Bolt Electronics' },
          ],
        })
      }
      if (url.includes('/image')) return new Response('', { status: 404 })
      const parsed = new URL(url, 'http://shop.test')
      const merchant = parsed.searchParams.get('merchant')
      const term = parsed.searchParams.get('q')
      let items = products
      if (merchant !== null) items = items.filter((product) => product.tenant === merchant)
      if (term !== null) items = items.filter((product) => product.name.toLowerCase().includes(term.toLowerCase()))
      return Response.json({ items })
    }),
  )
  return urls
}

beforeEach(() => {
  window.localStorage.clear()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('browsing every merchant at once', () => {
  it('shows products from more than one tenant in one grid', async () => {
    stubCatalog([apparel, electronics])
    renderWith(<BrowsePage />)

    expect(await screen.findByText('Merino Base Layer')).toBeInTheDocument()
    expect(screen.getByText('Travel Kettle')).toBeInTheDocument()
    // Each merchant appears on its card and again in the filter, so both
    // names are present more than once.
    expect(screen.getAllByText('Alpine Apparel').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Bolt Electronics').length).toBeGreaterThan(0)
  })

  it('passes the filters to the API rather than filtering in the browser', async () => {
    const user = userEvent.setup()
    const urls = stubCatalog([apparel, electronics])
    renderWith(<BrowsePage />)
    await screen.findByText('Merino Base Layer')

    await user.type(screen.getByLabelText('Search'), 'kettle')
    await user.selectOptions(screen.getByLabelText('Merchant'), 'bolt')
    await user.type(screen.getByLabelText('Max price'), '50')
    await user.click(screen.getByRole('button', { name: /Search/ }))

    expect(await screen.findByText('Travel Kettle')).toBeInTheDocument()
    expect(screen.queryByText('Merino Base Layer')).not.toBeInTheDocument()
    const searched = urls.filter((url) => url.startsWith('/api/products?'))
    expect(searched[searched.length - 1]).toContain('q=kettle')
    expect(searched[searched.length - 1]).toContain('merchant=bolt')
    expect(searched[searched.length - 1]).toContain('max_price=50')
  })
})

describe('one product page over heterogeneous schemas', () => {
  it('renders a merchant’s own fields without knowing their names', async () => {
    stubCatalog([electronics])
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url.includes('/image')) return new Response('', { status: 404 })
        return Response.json(electronics)
      }),
    )

    renderWith(
      <Routes>
        <Route path="/p/:tenant/:entityId" element={<ProductPage />} />
      </Routes>,
      '/p/bolt/bolt-kettle-1',
    )

    expect(await screen.findByRole('heading', { name: 'Travel Kettle' })).toBeInTheDocument()
    // Neither name appears anywhere in this application's source.
    expect(screen.getByText('voltage')).toBeInTheDocument()
    expect(screen.getByText('240')).toBeInTheDocument()
    expect(screen.getByText('warranty_months')).toBeInTheDocument()
  })

  it('says a withdrawn product does not exist, rather than showing an empty page', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => Response.json({ error: { message: 'no such product' } }, { status: 404 })),
    )

    renderWith(
      <Routes>
        <Route path="/p/:tenant/:entityId" element={<ProductPage />} />
      </Routes>,
      '/p/alpine/draft-1',
    )

    expect(await screen.findByRole('alert')).toHaveTextContent('No such product')
  })

  it('keeps two merchants’ products apart in the basket', async () => {
    const user = userEvent.setup()
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url.includes('/image')) return new Response('', { status: 404 })
        return Response.json(url.includes('/bolt/') ? electronics : apparel)
      }),
    )

    const view = renderWith(
      <Routes>
        <Route path="/p/:tenant/:entityId" element={<ProductPage />} />
      </Routes>,
      '/p/bolt/bolt-kettle-1',
    )
    await screen.findByRole('heading', { name: 'Travel Kettle' })
    await user.click(screen.getByRole('button', { name: 'Add to basket' }))
    view.unmount()

    renderWith(
      <Routes>
        <Route path="/p/:tenant/:entityId" element={<ProductPage />} />
      </Routes>,
      '/p/alpine/alp-merino-1',
    )
    await screen.findByRole('heading', { name: 'Merino Base Layer' })
    await user.click(screen.getByRole('button', { name: 'Add to basket' }))

    const stored: unknown = JSON.parse(window.localStorage.getItem('marketplace.basket') ?? '[]')
    expect(stored).toHaveLength(2)
    // Two tenants, two lines. Keying on the entity id alone would merge two
    // different merchants' products whenever their ids collided.
    expect((stored as { tenant: string }[]).map((line) => line.tenant).sort()).toEqual(['alpine', 'bolt'])
  })
})
