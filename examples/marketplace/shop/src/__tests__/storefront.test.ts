import { afterEach, describe, expect, it, vi } from 'vitest'

import { formatPrice, getProduct, searchPath, searchProducts, StorefrontError } from '../lib/storefront.js'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('the shopper API client', () => {
  it('leaves an empty filter out of the query rather than sending it blank', () => {
    expect(searchPath({})).toBe('/api/products')
    expect(searchPath({ q: '', merchant: '' })).toBe('/api/products')
    expect(searchPath({ q: 'merino', merchant: 'alpine', minPrice: '10', limit: 24 })).toBe(
      '/api/products?q=merino&merchant=alpine&min_price=10&limit=24',
    )
  })

  it('escapes a search term instead of letting it change the query', () => {
    expect(searchPath({ q: 'a&merchant=bolt' })).toBe('/api/products?q=a%26merchant%3Dbolt')
  })

  it('sends no credential: the shopper API is public', async () => {
    const calls: RequestInit[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, init: RequestInit = {}) => {
        calls.push(init)
        return Response.json({ items: [] })
      }),
    )

    await searchProducts({ q: 'merino' })

    expect(new Headers(calls[0]?.headers).get('Authorization')).toBeNull()
  })

  it('reports a withdrawn product as a 404, which is what the API answers', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => Response.json({ error: { message: 'no such product' } }, { status: 404 })),
    )

    const failure = await getProduct('alpine', 'draft-1').catch((error: unknown) => error)
    expect(failure).toBeInstanceOf(StorefrontError)
    expect((failure as StorefrontError).status).toBe(404)
  })
})

describe('rendering a price', () => {
  it('keeps the decimal as text', () => {
    // 89.50 must not become 89.5, and a long price must not lose digits: the
    // projection stores a decimal, and a float parse is where both happen.
    expect(formatPrice({ price: '89.50', currency: 'EUR' })).toBe('89.50 EUR')
    expect(formatPrice({ price: '12345678901234.99', currency: 'EUR' })).toBe('12345678901234.99 EUR')
  })

  it('renders nothing when a product carries no price', () => {
    expect(formatPrice({ currency: 'EUR' })).toBe('')
  })
})
