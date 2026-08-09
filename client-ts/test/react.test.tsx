import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { afterEach, describe, expect, it } from 'vitest'
import { createClient, type FlexitypeClient } from '../src/client.js'
import {
  flattenPages,
  flexitypeKeysFor,
  FlexitypeProvider,
  useEffectiveAttributes,
  useEntityValues,
  useFlexitypeClient,
  useFormDescriptor,
  useInfiniteQueryEntities,
  useSetValue,
  useTypes,
} from '../src/react/index.js'
import { errorBody, mockFetch, queryOf, type FetchMock } from './helpers.js'

function harness(http: FetchMock): { wrapper: (props: { children: ReactNode }) => ReactNode; client: FlexitypeClient; queryClient: QueryClient } {
  const client = createClient({ baseUrl: 'https://example.test', retry: false, fetch: http.fetch })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(
      QueryClientProvider,
      { client: queryClient },
      createElement(FlexitypeProvider, { client }, children),
    )
  return { wrapper, client, queryClient }
}

const queryClients: QueryClient[] = []
afterEach(() => {
  for (const qc of queryClients.splice(0)) qc.clear()
})

describe('FlexitypeProvider', () => {
  it('supplies the client to the hooks below it', () => {
    const http = mockFetch()
    const { wrapper, client } = harness(http)
    const { result } = renderHook(() => useFlexitypeClient(), { wrapper })
    expect(result.current).toBe(client)
  })

  it('raises without a provider, rather than looking like an empty result', () => {
    expect(() => renderHook(() => useFlexitypeClient())).toThrow(/no client in context/)
  })
})

describe('read hooks', () => {
  it('separates pending, error and empty, so a failure never renders as "no results"', async () => {
    // The console shipped exactly this bug (#496): a failed request rendered
    // as an empty list, and nobody could tell the two apart.
    const failing = mockFetch({ status: 500, body: errorBody('INTERNAL', 'database down') })
    const { wrapper } = harness(failing)
    const failed = renderHook(() => useTypes(), { wrapper })

    expect(failed.result.current.state).toBe('pending')
    await waitFor(() => expect(failed.result.current.state).toBe('error'))
    expect(failed.result.current.isEmpty).toBe(false)
    expect(failed.result.current.error?.code).toBe('INTERNAL')
    expect(failed.result.current.error?.message).toBe('database down')

    const empty = mockFetch({ body: { items: [], page_info: {} } })
    const emptyHarness = harness(empty)
    const emptied = renderHook(() => useTypes(), { wrapper: emptyHarness.wrapper })
    await waitFor(() => expect(emptied.result.current.state).toBe('empty'))
    expect(emptied.result.current.isEmpty).toBe(true)
    expect(emptied.result.current.error).toBeNull()

    const ready = mockFetch({ body: { items: [{ id: 'T-1' }], page_info: {} } })
    const readyHarness = harness(ready)
    const loaded = renderHook(() => useTypes(), { wrapper: readyHarness.wrapper })
    await waitFor(() => expect(loaded.result.current.state).toBe('ready'))
    expect(loaded.result.current.data?.items).toHaveLength(1)
  })

  it('reads a type’s effective attributes and builds a form descriptor from them', async () => {
    const http = mockFetch({
      body: {
        items: [
          {
            attribute: {
              id: 'A-sku',
              internal_name: 'sku',
              display_name: 'SKU',
              data_type: 'string',
              required: true,
            },
            declared_in: { id: 'T-1', internal_name: 'product', display_name: 'Product' },
          },
          {
            attribute: {
              id: 'A-status',
              internal_name: 'status',
              display_name: 'Status',
              data_type: 'enum',
              constraints: [{ kind: 'one_of', values: [{ type: 'enum', value: 'active' }] }],
            },
            declared_in: { id: 'T-1', internal_name: 'product', display_name: 'Product' },
          },
        ],
      },
    })
    const { wrapper } = harness(http)

    const { result } = renderHook(() => useFormDescriptor('T-1'), { wrapper })
    await waitFor(() => expect(result.current.state).toBe('ready'))

    expect(result.current.descriptor?.fields.map((f) => f.name)).toEqual(['sku', 'status'])
    expect(result.current.descriptor?.byName.sku?.required).toBe(true)
    expect(result.current.descriptor?.byName.status?.kind).toBe('select')
    expect(result.current.descriptor?.byName.status?.options).toEqual([{ value: 'active', label: 'active' }])
  })

  it('does not fetch until the id it needs is known', async () => {
    const http = mockFetch()
    const { wrapper } = harness(http)
    const { result } = renderHook(() => useEffectiveAttributes(undefined), { wrapper })
    await waitFor(() => expect(result.current.fetchStatus).toBe('idle'))
    expect(http.calls).toHaveLength(0)
  })

  it('groups an entity’s values by attribute and scope', async () => {
    const http = mockFetch({
      body: {
        items: [
          { id: 'v1', attribute_definition_id: 'A-name', entity_id: 'e1', value: 'Widget' },
          { id: 'v2', attribute_definition_id: 'A-name', entity_id: 'e1', locale: 'fr', value: 'Bidule' },
        ],
      },
    })
    const { wrapper } = harness(http)

    const { result } = renderHook(() => useEntityValues('T-1', 'e1'), { wrapper })
    await waitFor(() => expect(result.current.state).toBe('ready'))

    expect(result.current.scoped?.get('A-name')?.value).toBe('Widget')
    expect(result.current.scoped?.get('A-name', { locale: 'fr' })?.value).toBe('Bidule')
  })
})

describe('the infinite FQL query', () => {
  it('walks a page boundary with the cursor the service returned', async () => {
    const http = mockFetch(
      {
        body: {
          items: [{ entity_id: 'e1' }, { entity_id: 'e2' }],
          page_info: { has_next_page: true, next_cursor: 'CURSOR-2' },
        },
      },
      { body: { items: [{ entity_id: 'e3' }], page_info: { has_next_page: false } } },
    )
    const { wrapper } = harness(http)

    const { result } = renderHook(() => useInfiniteQueryEntities('product', 'price > 10', { limit: 2 }), {
      wrapper,
    })
    await waitFor(() => expect(result.current.state).toBe('ready'))

    expect(flattenPages(result.current.data).map((e) => e.entity_id)).toEqual(['e1', 'e2'])
    expect(result.current.hasNextPage).toBe(true)

    await act(async () => {
      await result.current.fetchNextPage()
    })
    await waitFor(() => expect(result.current.isFetching).toBe(false))

    expect(flattenPages(result.current.data).map((e) => e.entity_id)).toEqual(['e1', 'e2', 'e3'])
    expect(result.current.hasNextPage).toBe(false)
    expect(http.calls).toHaveLength(2)
    expect(queryOf(http.calls[0]?.url ?? '').cursor).toBeUndefined()
    expect(queryOf(http.calls[1]?.url ?? '').cursor).toBe('CURSOR-2')
  })

  it('reports a refused cursor as an error, not as the end of the list', async () => {
    const http = mockFetch({ status: 422, body: errorBody('VALIDATION', 'cursor arity') })
    const { wrapper } = harness(http)

    const { result } = renderHook(() => useInfiniteQueryEntities('product', 'price > 10'), { wrapper })
    await waitFor(() => expect(result.current.state).toBe('error'))
    expect(result.current.error?.code).toBe('VALIDATION')
  })

  it('says empty when the query matched nothing', async () => {
    const http = mockFetch({ body: { items: [], page_info: { has_next_page: false } } })
    const { wrapper } = harness(http)

    const { result } = renderHook(() => useInfiniteQueryEntities('product', 'price > 999999'), { wrapper })
    await waitFor(() => expect(result.current.state).toBe('empty'))
  })
})

describe('mutations', () => {
  it('writes a value and refetches the entity that changed', async () => {
    const http = mockFetch({
      body: { items: [{ id: 'v1', attribute_definition_id: 'A-sku', entity_id: 'e1', value: 'OLD' }] },
    })
    const { wrapper } = harness(http)

    const view = renderHook(
      () => ({ values: useEntityValues('T-1', 'e1'), write: useSetValue() }),
      { wrapper },
    )
    await waitFor(() => expect(view.result.current.values.state).toBe('ready'))
    expect(view.result.current.values.scoped?.valueOf('A-sku')).toBe('OLD')

    // The write, then the refetch the invalidation triggers.
    http.push(
      { body: { id: 'v1', attribute_definition_id: 'A-sku', entity_id: 'e1', value: 'NEW' } },
      { body: { items: [{ id: 'v1', attribute_definition_id: 'A-sku', entity_id: 'e1', value: 'NEW' }] } },
    )

    await act(async () => {
      await view.result.current.write.mutateAsync({
        attribute_definition_id: 'A-sku',
        entity_id: 'e1',
        type_definition_id: 'T-1',
        value: 'NEW',
      })
    })

    await waitFor(() => expect(view.result.current.values.scoped?.valueOf('A-sku')).toBe('NEW'))
    expect(http.calls.map((c) => c.method)).toEqual(['GET', 'POST', 'GET'])
  })

  it('reports a rejected write with its code, instead of swallowing it', async () => {
    const http = mockFetch({
      status: 422,
      body: errorBody('DEPENDENCY_VIOLATION', 'colour requires a material'),
    })
    const { wrapper } = harness(http)

    const { result } = renderHook(() => useSetValue(), { wrapper })
    await act(async () => {
      await result.current
        .mutateAsync({ attribute_definition_id: 'A-1', entity_id: 'e1', value: 'x' })
        .catch(() => undefined)
    })

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(result.current.error?.code).toBe('DEPENDENCY_VIOLATION')
  })
})

describe('the query-key scheme', () => {
  it('starts every key with the package root', () => {
    const flexitypeKeys = flexitypeKeysFor('c1')
    expect(flexitypeKeys.all).toEqual(['flexitype', 'c1'])
    expect(flexitypeKeys.types.list({ limit: 10 })[0]).toBe('flexitype')
    expect(flexitypeKeys.types.list({ limit: 10 })[1]).toBe('c1')
    expect(flexitypeKeys.entities.values('T-1', 'e1')[0]).toBe('flexitype')
  })

  it('nests a derived key under the record it derives from, so a prefix invalidates it', () => {
    const flexitypeKeys = flexitypeKeysFor('c1')
    const detail = flexitypeKeys.types.detail('T-1')
    const derived = flexitypeKeys.types.effectiveAttributes('T-1')
    expect(derived.slice(0, detail.length)).toEqual([...detail])

    const entity = flexitypeKeys.entities.detail('T-1', 'e1')
    for (const key of [
      flexitypeKeys.entities.values('T-1', 'e1'),
      flexitypeKeys.entities.relationships('T-1', 'e1'),
      flexitypeKeys.entities.completeness('T-1', 'e1'),
      flexitypeKeys.entities.revisions('T-1', 'e1'),
      flexitypeKeys.entities.effectiveSchema('T-1', 'e1', 'A-1'),
    ]) {
      expect(key.slice(0, entity.length)).toEqual([...entity])
    }
  })

  it('keeps one entity’s key clear of another’s', () => {
    const flexitypeKeys = flexitypeKeysFor('c1')
    const first = flexitypeKeys.entities.detail('T-1', 'e1')
    const second = flexitypeKeys.entities.values('T-1', 'e2')
    expect(second.slice(0, first.length)).not.toEqual([...first])
  })
})

describe('one cache, two tenants (#589)', () => {
  // One client is one tenant, because the tenant travels in the token. The
  // documented multi-tenant pattern swaps the client prop over a cache the
  // application owns — so if the cache key does not name the client, a hook
  // under tenant B reads tenant A's entry and, within staleTime, never
  // contacts B at all. The server's isolation cannot help: it is not asked.
  function tenant(name: string): { client: FlexitypeClient; http: FetchMock } {
    const http = mockFetch(
      { body: { items: [{ id: `type-${name}`, internal_name: `type_${name}`, display_name: name }] } },
      { body: { items: [{ id: `type-${name}`, internal_name: `type_${name}`, display_name: name }] } },
    )
    const client = createClient({
      baseUrl: 'https://example.test',
      token: `token-${name}`,
      retry: false,
      fetch: http.fetch,
    })
    return { client, http }
  }

  it('does not serve tenant A’s types to tenant B', async () => {
    const a = tenant('A')
    const b = tenant('B')
    const queryClient = new QueryClient({
      // The reported configuration: one shared cache, results considered
      // fresh for a while.
      defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 5000 } },
    })
    queryClients.push(queryClient)

    const render = (client: FlexitypeClient) =>
      renderHook(() => useTypes(), {
        wrapper: ({ children }: { children: ReactNode }) =>
          createElement(
            QueryClientProvider,
            { client: queryClient },
            createElement(FlexitypeProvider, { client }, children),
          ),
      })

    const first = render(a.client)
    await waitFor(() => expect(first.result.current.isSuccess).toBe(true))
    expect(first.result.current.data?.items[0]?.id).toBe('type-A')

    const second = render(b.client)
    await waitFor(() => expect(second.result.current.isSuccess).toBe(true))

    // B's own backend was asked, and B's data is what B sees.
    expect(b.http.calls.length).toBeGreaterThan(0)
    expect(second.result.current.data?.items[0]?.id).toBe('type-B')
  })

  it('gives two clients distinct cache identities', () => {
    const a = tenant('A')
    const b = tenant('B')
    expect(a.client.cacheKey).not.toBe(b.client.cacheKey)
    // The token must not travel in a query key: keys show up in devtools and
    // in application logs.
    expect(a.client.cacheKey).not.toContain('token-A')
  })

  it('gives the same client the same identity every time', () => {
    const one = createClient({ baseUrl: 'https://example.test', token: 't' })
    const two = createClient({ baseUrl: 'https://example.test', token: 't' })
    expect(one.cacheKey).toBe(two.cacheKey)
  })

  it('honours an explicit cacheKeyPrefix', () => {
    const client = createClient({ baseUrl: 'https://example.test', token: 't', cacheKeyPrefix: 'merchant-7' })
    expect(client.cacheKey).toBe('merchant-7')
  })
})
