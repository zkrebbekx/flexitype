import { describe, expect, it } from 'vitest'
import { createClient, normalizeBaseUrl } from '../src/client.js'
import { encodeQuery } from '../src/http.js'
import { mockFetch, queryOf } from './helpers.js'

describe('the base URL', () => {
  it('adds the /api/v1 prefix and trims a trailing slash', () => {
    expect(normalizeBaseUrl('https://flexitype.internal')).toBe('https://flexitype.internal/api/v1')
    expect(normalizeBaseUrl('https://flexitype.internal/')).toBe('https://flexitype.internal/api/v1')
    expect(normalizeBaseUrl('https://flexitype.internal/api/v1')).toBe('https://flexitype.internal/api/v1')
    expect(normalizeBaseUrl('http://localhost:8080')).toBe('http://localhost:8080/api/v1')
  })

  it('accepts a path-only base, which is how a same-origin console calls the API', () => {
    expect(normalizeBaseUrl('/')).toBe('/api/v1')
    expect(normalizeBaseUrl('/api/v1')).toBe('/api/v1')
  })

  it('refuses a base with no scheme, rather than failing on every later request', () => {
    // "localhost:8080" parses as the scheme "localhost", so it used to build a
    // client that failed every call with an unrelated message.
    expect(() => normalizeBaseUrl('localhost:8080')).toThrow(/must include a scheme/)
    expect(() => normalizeBaseUrl('')).toThrow(/baseUrl is required/)
    expect(() => normalizeBaseUrl('ftp://example.test')).toThrow(/must include a scheme/)
  })
})

describe('request building', () => {
  it('sends the bearer token, which is what carries the tenant', async () => {
    const http = mockFetch({ body: { items: [] } })
    const client = createClient({ baseUrl: 'https://example.test', token: 'ft_a_b', fetch: http.fetch })

    await client.types.list()

    expect(http.calls[0]?.headers.authorization).toBe('Bearer ft_a_b')
    expect(http.calls[0]?.headers.accept).toBe('application/json')
  })

  it('omits the Authorization header when no token is configured', async () => {
    const http = mockFetch({ body: { items: [] } })
    const client = createClient({ baseUrl: 'https://example.test', fetch: http.fetch })

    await client.types.list()

    expect(http.calls[0]?.headers.authorization).toBeUndefined()
  })

  it('sends extra headers a caller configured', async () => {
    const http = mockFetch({ body: { items: [] } })
    const client = createClient({
      baseUrl: 'https://example.test',
      headers: { 'X-Request-Source': 'storefront' },
      fetch: http.fetch,
    })

    await client.types.list()
    expect(http.calls[0]?.headers['x-request-source']).toBe('storefront')
  })

  it('drops an empty query parameter and joins a list with commas', () => {
    expect(encodeQuery({ a: 1, b: undefined, c: '', d: null, e: false })).toBe('?a=1&e=false')
    expect(encodeQuery({ attributes: ['sku', 'name'] })).toBe('?attributes=sku%2Cname')
    expect(encodeQuery({ attributes: [] })).toBe('')
    expect(encodeQuery()).toBe('')
  })

  it('escapes a path segment, so an entity id may hold a slash', async () => {
    const http = mockFetch({ body: { items: [] } })
    const client = createClient({ baseUrl: 'https://example.test', fetch: http.fetch })

    await client.entities.values('T-1', 'sku/with slash')

    expect(http.calls[0]?.url).toBe('https://example.test/api/v1/entities/T-1/sku%2Fwith%20slash/values')
  })

  it('sends a JSON body with its content type on a write', async () => {
    const http = mockFetch({ body: { id: 'T-1' } })
    const client = createClient({ baseUrl: 'https://example.test', fetch: http.fetch })

    await client.types.create({ internal_name: 'product', display_name: 'Product' })

    expect(http.calls[0]?.method).toBe('POST')
    expect(http.calls[0]?.headers['content-type']).toBe('application/json')
    expect(JSON.parse(http.calls[0]?.body ?? '{}')).toEqual({
      internal_name: 'product',
      display_name: 'Product',
    })
  })

  it('resolves a 204 to undefined instead of failing to parse an empty body', async () => {
    const http = mockFetch({ status: 204 })
    const client = createClient({ baseUrl: 'https://example.test', fetch: http.fetch })

    await expect(client.savedViews.remove('sv1')).resolves.toBeUndefined()
  })
})

describe('the service surface', () => {
  it('maps list options onto the query parameters the API documents', async () => {
    const http = mockFetch({ body: { items: [], page_info: {} } })
    const client = createClient({ baseUrl: 'https://example.test', fetch: http.fetch })

    await client.attributes.list({
      typeDefinitionId: 'T-1',
      internalName: ['sku', 'name'],
      dataType: ['string'],
      includeArchived: true,
      limit: 10,
      total: true,
    })

    expect(queryOf(http.calls[0]?.url ?? '')).toEqual({
      type_definition_id: 'T-1',
      internal_name: 'sku,name',
      data_type: 'string',
      include_archived: 'true',
      limit: '10',
      total: 'true',
    })
  })

  it('unwraps an {"items":[...]} body into an array', async () => {
    const http = mockFetch({ body: { items: [{ attribute: { id: 'A-1' } }] } })
    const client = createClient({ baseUrl: 'https://example.test', fetch: http.fetch })

    const attributes = await client.types.effectiveAttributes('T-1')
    expect(attributes).toHaveLength(1)
    expect(http.calls[0]?.url).toBe('https://example.test/api/v1/type-definitions/T-1/effective-attributes')
  })

  it('normalizes a page body that omits items or page_info', async () => {
    const http = mockFetch({ body: {} })
    const client = createClient({ baseUrl: 'https://example.test', fetch: http.fetch })

    const page = await client.types.list()
    expect(page.items).toEqual([])
    expect(page.page_info).toEqual({})
  })

  it('sends an FQL query as documented', async () => {
    const http = mockFetch({ body: { items: [], page_info: {} } })
    const client = createClient({ baseUrl: 'https://example.test', fetch: http.fetch })

    await client.query.run('product', 'price > 10', { locale: 'fr', limit: 50 })

    expect(queryOf(http.calls[0]?.url ?? '')).toEqual({
      type: 'product',
      q: 'price > 10',
      locale: 'fr',
      limit: '50',
    })
  })

  it('builds an export URL a browser can follow on its own', () => {
    const client = createClient({ baseUrl: 'https://example.test', fetch: mockFetch().fetch })
    expect(client.entities.exportUrl('T-1', { attributes: ['sku', 'name'], query: 'price > 10' })).toBe(
      'https://example.test/api/v1/entities/T-1/export?attributes=sku%2Cname&query=price+%3E+10',
    )
    expect(client.entities.mediaUrl('tenant/abc.png')).toBe(
      'https://example.test/api/v1/media/tenant%2Fabc.png',
    )
  })

  it('uploads media as multipart, without overriding the boundary', async () => {
    const http = mockFetch({ body: { id: 'v1' } })
    const client = createClient({ baseUrl: 'https://example.test', fetch: http.fetch })

    await client.entities.uploadMedia('T-1', 'e1', 'A-1', new Blob(['x']), { filename: 'logo.png' })

    expect(http.calls[0]?.method).toBe('POST')
    // Naming a Content-Type here would break the multipart boundary.
    expect(http.calls[0]?.headers['content-type']).toBeUndefined()
    expect(http.calls[0]?.url).toBe(
      'https://example.test/api/v1/entities/T-1/e1/attributes/A-1/media',
    )
  })

  it('mints a signed media link a public page can use with no credential', async () => {
    const http = mockFetch({ body: { url: '/media/signed/eyJ2IjoidjEifQ.abc', expires_at: '2026-08-09T12:15:00Z' } })
    const client = createClient({ baseUrl: 'https://example.test', fetch: http.fetch })

    const link = await client.entities.signMediaUrl('tenant/abc.png', { ttlSeconds: 600 })

    expect(http.calls[0]?.method).toBe('POST')
    expect(http.calls[0]?.url).toBe('https://example.test/api/v1/media/tenant%2Fabc.png/signed-url')
    expect(JSON.parse(String(http.calls[0]?.body))).toEqual({ ttl_seconds: 600 })
    // The link is relative to the service root, and the caller redeems it
    // without a token: the signature is the credential.
    expect(link.url).toBe('/media/signed/eyJ2IjoidjEifQ.abc')
    expect(link.expires_at).toBe('2026-08-09T12:15:00Z')
  })

  it('reports a GraphQL query-level error instead of returning a hollow result', async () => {
    // A GraphQL error arrives with a 200 status, so a client that only checks
    // the status hands back data that is missing without saying so.
    const http = mockFetch({ body: { errors: [{ message: 'unknown field "sku"' }] } })
    const client = createClient({ baseUrl: 'https://example.test', fetch: http.fetch })

    await expect(client.graphql('{ product { sku } }')).rejects.toThrow(/unknown field "sku"/)
  })

  it('returns the data of a successful GraphQL query', async () => {
    const http = mockFetch({ body: { data: { products: [{ entityId: 'e1' }] } } })
    const client = createClient({ baseUrl: 'https://example.test', fetch: http.fetch })

    const data = await client.graphql<{ products: { entityId: string }[] }>('{ products { entityId } }')
    expect(data.products[0]?.entityId).toBe('e1')
  })

  it('reads the counts the operations endpoints report', async () => {
    const http = mockFetch({ body: { reindexed: 12 } }, { body: { recomputed: 3 } })
    const client = createClient({ baseUrl: 'https://example.test', fetch: http.fetch })

    expect(await client.reindex()).toBe(12)
    expect(await client.recompute()).toBe(3)
  })
})
