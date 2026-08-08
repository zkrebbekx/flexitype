import { describe, expect, it } from 'vitest'
import { createClient } from '../src/client.js'
import type { FlexitypeError } from '../src/errors.js'
import { collect, CursorStack, paginate, paginatePages, type Page } from '../src/pagination.js'
import { errorBody, mockFetch, queryOf } from './helpers.js'

function page<T>(items: T[], nextCursor?: string): Page<T> {
  return {
    items,
    page_info:
      nextCursor === undefined
        ? { has_next_page: false }
        : { has_next_page: true, next_cursor: nextCursor },
  }
}

describe('the pagination iterator', () => {
  it('walks across a page boundary and sends the cursor the service gave it', async () => {
    const http = mockFetch(
      { body: page([{ entity_id: 'e1' }, { entity_id: 'e2' }], 'CURSOR-2') },
      { body: page([{ entity_id: 'e3' }]) },
    )
    const client = createClient({ baseUrl: 'https://example.test', retry: false, fetch: http.fetch })

    const seen = await collect(client.entities.listAll('t1', { limit: 2 }))

    expect(seen.map((e) => e.entity_id)).toEqual(['e1', 'e2', 'e3'])
    expect(http.calls).toHaveLength(2)
    expect(queryOf(http.calls[0]?.url ?? '').cursor).toBeUndefined()
    expect(queryOf(http.calls[0]?.url ?? '').limit).toBe('2')
    expect(queryOf(http.calls[1]?.url ?? '').cursor).toBe('CURSOR-2')
  })

  it('stops when the service reports no next page, even if it still sends a cursor', async () => {
    const fetched: (string | undefined)[] = []
    const items = await collect(
      paginate<number>(async (cursor) => {
        fetched.push(cursor)
        return { items: [1], page_info: { has_next_page: false, next_cursor: 'ignored' } }
      }),
    )
    expect(items).toEqual([1])
    expect(fetched).toEqual([undefined])
  })

  it('yields whole pages when the caller needs the boundaries', async () => {
    const pages: number[][] = []
    for await (const p of paginatePages<number>(async (cursor) =>
      cursor === undefined ? page([1, 2], 'c2') : page([3]),
    )) {
      pages.push(p.items)
    }
    expect(pages).toEqual([[1, 2], [3]])
  })

  it('stops rather than looping when the service repeats a cursor', async () => {
    // A service that answers the same cursor for ever would otherwise spin
    // until the process died, with no error to point at the cause.
    const walk = paginate<number>(async () => page([1], 'SAME'))
    await expect(collect(walk)).rejects.toThrow(/repeated a pagination cursor/)
  })

  it('surfaces a mid-walk failure instead of ending quietly', async () => {
    const http = mockFetch(
      { body: page([{ entity_id: 'e1' }], 'CURSOR-2') },
      { status: 422, body: errorBody('VALIDATION', 'unparseable cursor') },
    )
    const client = createClient({ baseUrl: 'https://example.test', retry: false, fetch: http.fetch })

    const walk = client.entities.listAll('t1')
    const first = await walk.next()
    expect(first.value?.entity_id).toBe('e1')

    const error = (await walk.next().catch((e: unknown) => e)) as FlexitypeError
    expect(error.code).toBe('VALIDATION')
  })

  it('stops early when the caller breaks out of the loop', async () => {
    let fetches = 0
    const walk = paginate<number>(async () => {
      fetches += 1
      return page([1, 2], 'more')
    })
    for await (const item of walk) {
      expect(item).toBe(1)
      break
    }
    expect(fetches).toBe(1)
  })
})

describe('CursorStack', () => {
  it('steps forward and back over a forward-only API', () => {
    const stack = new CursorStack()
    expect(stack.current()).toBeUndefined()
    expect(stack.depth()).toBe(0)

    expect(stack.push({ has_next_page: true, next_cursor: 'c2' })).toBe(true)
    expect(stack.current()).toBe('c2')
    expect(stack.depth()).toBe(1)

    expect(stack.push({ has_next_page: true, next_cursor: 'c3' })).toBe(true)
    expect(stack.current()).toBe('c3')

    expect(stack.pop()).toBe(true)
    expect(stack.current()).toBe('c2')
    expect(stack.pop()).toBe(true)
    expect(stack.current()).toBeUndefined()
    expect(stack.pop()).toBe(false)
  })

  it('refuses to move past the last page', () => {
    const stack = new CursorStack()
    expect(stack.push({ has_next_page: false })).toBe(false)
    expect(stack.current()).toBeUndefined()
  })

  it('returns to the first page when reset', () => {
    const stack = new CursorStack()
    stack.push({ has_next_page: true, next_cursor: 'c2' })
    stack.reset()
    expect(stack.current()).toBeUndefined()
    expect(stack.depth()).toBe(0)
  })
})
