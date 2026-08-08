import { afterEach, describe, expect, it, vi } from 'vitest'

import { deleteProduct, listMerchants, onboardMerchant, PlatformError, putProduct } from '../lib/platform.js'

/** Replaces fetch with one that records the call and answers with `body`. */
function stubFetch(status: number, body: unknown) {
  const calls: { url: string; init: RequestInit }[] = []
  const fetchMock = vi.fn(async (url: string, init: RequestInit = {}) => {
    calls.push({ url, init })
    const text = body === undefined ? '' : JSON.stringify(body)
    return new Response(text, { status, headers: { 'Content-Type': 'application/json' } })
  })
  vi.stubGlobal('fetch', fetchMock)
  return calls
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('the platform client', () => {
  it('sends no credential of its own', async () => {
    // The console holds NO token. nginx adds the credential to every proxied
    // request, so a browser extension, an XSS payload or a screenshot of the
    // network tab cannot carry one away.
    const calls = stubFetch(200, { items: [] })
    await listMerchants()

    const headers = new Headers(calls[0]?.init.headers)
    expect(headers.get('Authorization')).toBeNull()
  })

  it('writes a whole product in one request', async () => {
    const calls = stubFetch(200, { entity_id: 'alp-1', written: 3 })
    await putProduct('alpine', 'alp-1', {
      type: 'apparel',
      values: { name: 'Merino', price: '89.50', status: 'active' },
    })

    expect(calls).toHaveLength(1)
    expect(calls[0]?.init.method).toBe('PUT')
    expect(calls[0]?.url).toBe('/api/merchants/alpine/products/alp-1')
    expect(JSON.parse(String(calls[0]?.init.body))).toEqual({
      type: 'apparel',
      values: { name: 'Merino', price: '89.50', status: 'active' },
    })
  })

  it('escapes an id rather than letting it change the path', async () => {
    const calls = stubFetch(204, undefined)
    await deleteProduct('alpine', 'a/../../admin', 'product')

    expect(calls[0]?.url).toBe('/api/merchants/alpine/products/a%2F..%2F..%2Fadmin?type=product')
  })

  it('carries the service message out of a failure, not the status alone', async () => {
    stubFetch(422, { error: { message: 'sku must be unique' } })

    await expect(onboardMerchant({ id: 'alpine', display_name: 'Alpine' })).rejects.toThrow(
      'sku must be unique',
    )
  })

  it('reports the status when a failure carries no message', async () => {
    stubFetch(500, undefined)

    const failure = await listMerchants().catch((error: unknown) => error)
    expect(failure).toBeInstanceOf(PlatformError)
    expect((failure as PlatformError).status).toBe(500)
  })
})
