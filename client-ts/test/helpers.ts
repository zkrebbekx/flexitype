import type { FetchLike } from '../src/http.js'

/** One recorded request. */
export interface RecordedRequest {
  url: string
  method: string
  headers: Record<string, string>
  body: string | undefined
}

/** A scripted answer. */
export interface MockResponse {
  status?: number
  body?: unknown
  headers?: Record<string, string>
  /** Fails at the transport level instead of answering. */
  throws?: Error
}

/** A fetch mock that answers from a script and records what it was asked. */
export interface FetchMock {
  fetch: FetchLike
  calls: RecordedRequest[]
  /** Queues one answer. The queue is consumed in order. */
  push(...responses: MockResponse[]): void
  /** Answers every unscripted request with this. */
  fallback(response: MockResponse): void
}

/**
 * Builds a fetch mock.
 *
 * The tests drive the client through this rather than through a live service:
 * a test that needs a running Postgres cannot run on a laptop in a second, and
 * the behaviour under test here is the client's, not the service's.
 */
export function mockFetch(...initial: MockResponse[]): FetchMock {
  const queue: MockResponse[] = [...initial]
  const calls: RecordedRequest[] = []
  let fallbackResponse: MockResponse | undefined

  const fetchImpl: FetchLike = async (url, init) => {
    calls.push({
      url,
      method: init?.method ?? 'GET',
      headers: normalizeHeaders(init?.headers),
      body: typeof init?.body === 'string' ? init.body : undefined,
    })
    const next = queue.shift() ?? fallbackResponse
    if (next === undefined) throw new Error(`unscripted request: ${init?.method ?? 'GET'} ${url}`)
    if (next.throws !== undefined) throw next.throws
    const status = next.status ?? 200
    const headers = new Headers(next.headers ?? {})
    if (!headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
    const body = next.body === undefined ? '' : JSON.stringify(next.body)
    return new Response(status === 204 ? null : body, { status, headers })
  }

  return {
    fetch: fetchImpl,
    calls,
    push: (...responses) => queue.push(...responses),
    fallback: (response) => {
      fallbackResponse = response
    },
  }
}

function normalizeHeaders(headers: HeadersInit | undefined): Record<string, string> {
  const out: Record<string, string> = {}
  if (headers === undefined) return out
  new Headers(headers).forEach((value, key) => {
    out[key.toLowerCase()] = value
  })
  return out
}

/** An error body in the API's envelope. */
export function errorBody(code: string, message = 'boom', details?: Record<string, unknown>): unknown {
  return { error: { code, message, ...(details === undefined ? {} : { details }) } }
}

/** The query part of a recorded URL, as a plain object. */
export function queryOf(url: string): Record<string, string> {
  const index = url.indexOf('?')
  if (index < 0) return {}
  const out: Record<string, string> = {}
  new URLSearchParams(url.slice(index + 1)).forEach((value, key) => {
    out[key] = value
  })
  return out
}
