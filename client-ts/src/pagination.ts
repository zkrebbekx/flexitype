import { FlexitypeError } from './errors.js'
import type { RequestOptions } from './http.js'

/**
 * Where a page sits in the whole result set.
 *
 * `has_previous_page` reports only that this is not the first page. It is not
 * a backward-paging capability: the API has no "before" cursor and no
 * direction. To offer a Back button, keep the cursors you have already used —
 * `CursorStack` does exactly that.
 */
export interface PageInfo {
  has_next_page?: boolean
  has_previous_page?: boolean
  next_cursor?: string
  /** Present only when the request asked for it with `total: true`. */
  total_count?: number
}

/** One page of a list response. */
export interface Page<T> {
  items: T[]
  page_info: PageInfo
}

/**
 * The pagination arguments every list call accepts.
 *
 * The cursor is an opaque keyset cursor, so pages stay stable while other
 * writers work. A cursor the ordering column cannot parse, or one carrying the
 * wrong number of values, answers 422 — a VALIDATION FlexitypeError, which the
 * retry policy never replays.
 */
export interface ListOptions extends RequestOptions {
  /** The page size. Omit it for the server default; the maximum is 500. */
  limit?: number | undefined
  /** The opaque cursor of the page to fetch. Omit it for the first page. */
  cursor?: string | undefined
  /** Asks the service to compute `page_info.total_count`. It costs a second scan. */
  total?: boolean | undefined
}

/** Fetches one page given a cursor. It is what the iterators walk. */
export type PageFetcher<T> = (cursor: string | undefined) => Promise<Page<T>>

/**
 * Walks every page by following the keyset cursor.
 *
 * The walk stops when the service reports no next page. It also stops if the
 * service ever returns the cursor it was given, which would otherwise loop for
 * ever on the same page.
 */
export async function* paginatePages<T>(fetchPage: PageFetcher<T>): AsyncGenerator<Page<T>, void, undefined> {
  let cursor: string | undefined
  const seen = new Set<string>()
  for (;;) {
    const page = await fetchPage(cursor)
    yield page
    const next = page.page_info?.next_cursor
    if (page.page_info?.has_next_page !== true || next === undefined || next === '') return
    if (seen.has(next)) {
      throw new FlexitypeError({
        code: 'INTERNAL',
        message: 'flexitype: the service repeated a pagination cursor; the walk was stopped',
        status: 0,
      })
    }
    seen.add(next)
    cursor = next
  }
}

/** Walks every item across every page. */
export async function* paginate<T>(fetchPage: PageFetcher<T>): AsyncGenerator<T, void, undefined> {
  for await (const page of paginatePages(fetchPage)) {
    for (const item of page.items) yield item
  }
}

/**
 * Drains an async iterable into an array.
 *
 * It loads every page, so bound it with an FQL filter or a limit before using
 * it on a type that holds millions of entities.
 */
export async function collect<T>(source: AsyncIterable<T>): Promise<T[]> {
  const out: T[] = []
  for await (const item of source) out.push(item)
  return out
}

/**
 * Remembers the cursor of each page a caller has visited, so a paged screen
 * can step backwards over a forward-only keyset API. It mirrors `CursorStack`
 * in the Go client.
 *
 * A CursorStack belongs to one view and is not safe to share between two.
 *
 * ```ts
 * const stack = new CursorStack()
 * let page = await client.types.list({ cursor: stack.current() })
 * if (stack.push(page.page_info)) page = await client.types.list({ cursor: stack.current() })
 * if (stack.pop()) page = await client.types.list({ cursor: stack.current() })
 * ```
 */
export class CursorStack {
  /** The cursor of each visited page in order. The first entry is always "". */
  private visited: string[] = []

  /** The cursor for the page the stack is on. It is undefined on page one. */
  current(): string | undefined {
    const top = this.visited[this.visited.length - 1]
    return top === undefined || top === '' ? undefined : top
  }

  /**
   * Records a move forward. It reports false when the page info has no next
   * page and leaves the stack untouched, so a Next button at the end of a list
   * does nothing instead of fetching an empty page.
   */
  push(info: PageInfo): boolean {
    const next = info.next_cursor
    if (info.has_next_page !== true || next === undefined || next === '') return false
    if (this.visited.length === 0) this.visited.push('')
    this.visited.push(next)
    return true
  }

  /** Steps back one page. It reports false on the first page. */
  pop(): boolean {
    if (this.visited.length < 2) return false
    this.visited.pop()
    return true
  }

  /** The zero-based index of the page the stack is on. */
  depth(): number {
    return this.visited.length === 0 ? 0 : this.visited.length - 1
  }

  /** Forgets every visited page, returning to page one. */
  reset(): void {
    this.visited = []
  }
}
