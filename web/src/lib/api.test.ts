import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError, friendlyError } from './api'

function stubFetch(status: number, body: unknown) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(JSON.stringify(body), { status })),
  )
}

afterEach(() => vi.unstubAllGlobals())

describe('api client', () => {
  it('returns parsed JSON on success', async () => {
    stubFetch(200, { id: 'x', internal_name: 'product' })
    const t = await api.getType('x')
    expect(t.internal_name).toBe('product')
  })

  it('maps API error envelopes onto ApiError with the domain code', async () => {
    stubFetch(422, {
      error: { code: 'DEPENDENCY_VIOLATION', message: 'value is not allowed', details: { value: 'sedan' } },
    })
    const err = await api
      .setValue({ attribute_definition_id: 'a', entity_id: 'e', value: 'sedan' })
      .catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.code).toBe('DEPENDENCY_VIOLATION')
    expect(err.details.value).toBe('sedan')
  })

  it('serialises query filters and pagination', async () => {
    const spy = vi.fn(async (..._args: unknown[]) => new Response(JSON.stringify({ items: [], page_info: {} }), { status: 200 }))
    vi.stubGlobal('fetch', spy)
    await api.listAttributes({ type_definition_id: 'td', include_archived: true, limit: 10, cursor: 'abc' })
    const url = spy.mock.calls[0]![0] as string
    expect(url).toContain('/api/v1/attributes?')
    expect(url).toContain('type_definition_id=td')
    expect(url).toContain('include_archived=true')
    expect(url).toContain('limit=10')
    expect(url).toContain('cursor=abc')
  })
})

describe('friendlyError', () => {
  it('renders domain codes in plain language', () => {
    expect(friendlyError(new ApiError(410, 'ARCHIVED', 'x'))).toMatch(/archived/)
    expect(friendlyError(new ApiError(422, 'DEPENDENCY_VIOLATION', 'no'))).toMatch(/dependency/i)
    expect(friendlyError(new ApiError(404, 'NOT_FOUND', 'x'))).toMatch(/Not found/)
    expect(friendlyError(new Error('boom'))).toBe('boom')
  })
})
