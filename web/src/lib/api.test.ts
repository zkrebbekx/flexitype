import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError, friendlyError, isConflict } from './api'

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

// Issue #597: the console never sent the version, so the server's saved-view
// compare-and-swap was dead code from the console's side and two operators
// editing one view silently lost an edit.
describe('optimistic concurrency', () => {
  it('sends the version the operator read on a saved-view patch', async () => {
    const spy = vi.fn(async (..._args: unknown[]) => new Response(JSON.stringify({ id: 'v1' }), { status: 200 }))
    vi.stubGlobal('fetch', spy)
    await api.updateSavedView('v1', { name: 'Mine', root_type: 'product', version: 3 })
    const body = JSON.parse((spy.mock.calls[0]![1] as RequestInit).body as string)
    expect(body.version).toBe(3)
  })

  it('sends the version the operator read on an attribute update', async () => {
    const spy = vi.fn(async (..._args: unknown[]) => new Response(JSON.stringify({ id: 'a1' }), { status: 200 }))
    vi.stubGlobal('fetch', spy)
    await api.updateAttribute('a1', { display_name: 'SKU', version: 7 })
    const body = JSON.parse((spy.mock.calls[0]![1] as RequestInit).body as string)
    expect(body.version).toBe(7)
  })

  it('recognises a stale write so the caller can pull the other version in', async () => {
    stubFetch(409, {
      error: { code: 'CONFLICT', message: 'the saved view was modified by someone else; re-read it and retry' },
    })
    const err = await api
      .updateSavedView('v1', { name: 'Mine', root_type: 'product', version: 3 })
      .catch((e) => e)
    expect(isConflict(err)).toBe(true)
    // The operator is told what happened, not just that it failed.
    expect(friendlyError(err)).toMatch(/modified by someone else/)
  })

  it('does not mistake another failure for a conflict', () => {
    expect(isConflict(new ApiError(422, 'VALIDATION', 'no'))).toBe(false)
    expect(isConflict(new Error('boom'))).toBe(false)
  })
})

// The relationship-definition PATCH never sent its body. Every console edit
// therefore asked the server to replace the record with nothing, which the
// full-replace semantics answer by clearing what the request omits — and
// display_name is required, so the save simply failed.
describe('request bodies', () => {
  it('sends the body on a relationship-definition update', async () => {
    const spy = vi.fn(async (..._args: unknown[]) => new Response(JSON.stringify({ id: 'r1' }), { status: 200 }))
    vi.stubGlobal('fetch', spy)
    await api.updateRelationshipDefinition('r1', { display_name: 'Goes with', version: 2 })
    const init = spy.mock.calls[0]![1] as RequestInit
    expect(init.body).toBeDefined()
    const body = JSON.parse(init.body as string)
    expect(body.display_name).toBe('Goes with')
    expect(body.version).toBe(2)
  })

  it('sends the version on a type and a dependency update', async () => {
    const spy = vi.fn(async (..._args: unknown[]) => new Response(JSON.stringify({ id: 'x' }), { status: 200 }))
    vi.stubGlobal('fetch', spy)
    await api.updateType('t1', { display_name: 'Product', version: 4 })
    await api.updateDependency('d1', { conditions: [], effect: {}, version: 9 })
    expect(JSON.parse((spy.mock.calls[0]![1] as RequestInit).body as string).version).toBe(4)
    expect(JSON.parse((spy.mock.calls[1]![1] as RequestInit).body as string).version).toBe(9)
  })
})
