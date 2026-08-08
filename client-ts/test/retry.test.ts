import { describe, expect, it } from 'vitest'
import { createClient } from '../src/client.js'
import type { FlexitypeError } from '../src/errors.js'
import { defaultRetryPolicy, isIdempotent, retries, waitBeforeMs } from '../src/retry.js'
import { errorBody, mockFetch } from './helpers.js'

/** The default policy with the waits removed, so a test does not sleep. */
const fastPolicy = { ...defaultRetryPolicy(), baseDelayMs: 0, maxDelayMs: 0, jitter: 0 }

describe('the retry policy', () => {
  it('treats GET, HEAD, PUT and DELETE as replayable and POST as not', () => {
    expect(isIdempotent('GET')).toBe(true)
    expect(isIdempotent('HEAD')).toBe(true)
    expect(isIdempotent('PUT')).toBe(true)
    expect(isIdempotent('DELETE')).toBe(true)
    expect(isIdempotent('POST')).toBe(false)
    expect(isIdempotent('PATCH')).toBe(false)
  })

  it('retries 429 and the transient 5xx statuses, and nothing else', () => {
    const policy = defaultRetryPolicy()
    for (const status of [429, 502, 503, 504]) expect(retries(policy, 'GET', status)).toBe(true)
    for (const status of [400, 401, 403, 404, 409, 422, 500, 501]) {
      expect(retries(policy, 'GET', status)).toBe(false)
    }
  })

  it('never retries a write, whatever the status', () => {
    const policy = defaultRetryPolicy()
    expect(retries(policy, 'POST', 429)).toBe(false)
    expect(retries(policy, 'POST', 503)).toBe(false)
    expect(retries(policy, 'PATCH', 503)).toBe(false)
  })

  it('doubles the wait and stops at maxDelayMs', () => {
    const policy = { ...defaultRetryPolicy(), jitter: 0 }
    expect(waitBeforeMs(policy, 2)).toBe(200)
    expect(waitBeforeMs(policy, 3)).toBe(400)
    expect(waitBeforeMs(policy, 6)).toBe(3200)
    expect(waitBeforeMs(policy, 10)).toBe(5000)
  })

  it('prefers the server hint over its own backoff, even past maxDelayMs', () => {
    const policy = { ...defaultRetryPolicy(), jitter: 0 }
    // The service knows when its token bucket refills; waiting less earns
    // another 429.
    expect(waitBeforeMs(policy, 2, 30_000)).toBe(30_000)
  })

  it('keeps the jittered wait inside its band', () => {
    const policy = { ...defaultRetryPolicy(), jitter: 0.2 }
    expect(waitBeforeMs(policy, 2, undefined, () => 0)).toBeCloseTo(160)
    expect(waitBeforeMs(policy, 2, undefined, () => 1)).toBeCloseTo(240)
  })
})

describe('retrying through the transport', () => {
  it('retries an idempotent read and returns the answer that finally succeeds', async () => {
    const http = mockFetch(
      { status: 503, body: errorBody('INTERNAL') },
      { status: 503, body: errorBody('INTERNAL') },
      { status: 200, body: { id: 't1', internal_name: 'product' } },
    )
    const client = createClient({ baseUrl: 'https://example.test', retry: fastPolicy, fetch: http.fetch })

    const type = await client.types.get('t1')

    expect(type.internal_name).toBe('product')
    expect(http.calls).toHaveLength(3)
  })

  it('does not retry a write, so a create is never applied twice', async () => {
    const http = mockFetch({ status: 503, body: errorBody('INTERNAL') })
    const client = createClient({ baseUrl: 'https://example.test', retry: fastPolicy, fetch: http.fetch })

    await expect(client.types.create({ internal_name: 'p', display_name: 'P' })).rejects.toThrow()

    expect(http.calls).toHaveLength(1)
    expect(http.calls[0]?.method).toBe('POST')
  })

  it('does not retry a bad cursor, because 422 is a permanent fault in the request', async () => {
    // A cursor of the wrong arity, or one the ordering column cannot parse,
    // answers 422 by design. Replaying it only repeats the same answer.
    const http = mockFetch({
      status: 422,
      body: errorBody('VALIDATION', 'cursor carries 1 value, the ordering needs 2'),
    })
    const client = createClient({ baseUrl: 'https://example.test', retry: fastPolicy, fetch: http.fetch })

    const error = (await client.types.list({ cursor: 'bad' }).catch((e: unknown) => e)) as FlexitypeError

    expect(error.code).toBe('VALIDATION')
    expect(http.calls).toHaveLength(1)
  })

  it('gives up after maxAttempts and reports the last real failure', async () => {
    const http = mockFetch(
      { status: 429, body: errorBody('RATE_LIMITED', 'first') },
      { status: 429, body: errorBody('RATE_LIMITED', 'second') },
      { status: 429, body: errorBody('RATE_LIMITED', 'third') },
    )
    const client = createClient({ baseUrl: 'https://example.test', retry: fastPolicy, fetch: http.fetch })

    const error = (await client.types.get('t1').catch((e: unknown) => e)) as FlexitypeError

    expect(http.calls).toHaveLength(3)
    expect(error.code).toBe('RATE_LIMITED')
    expect(error.message).toBe('third')
  })

  it('retries a transport failure on a read but not on a write', async () => {
    const readMock = mockFetch({ throws: new TypeError('reset') }, { status: 200, body: { id: 't1' } })
    const readClient = createClient({ baseUrl: 'https://example.test', retry: fastPolicy, fetch: readMock.fetch })
    await readClient.types.get('t1')
    expect(readMock.calls).toHaveLength(2)

    const writeMock = mockFetch({ throws: new TypeError('reset') })
    const writeClient = createClient({ baseUrl: 'https://example.test', retry: fastPolicy, fetch: writeMock.fetch })
    await expect(writeClient.types.create({ internal_name: 'p', display_name: 'P' })).rejects.toThrow()
    expect(writeMock.calls).toHaveLength(1)
  })

  it('sends exactly one request when retrying is switched off', async () => {
    const http = mockFetch({ status: 503, body: errorBody('INTERNAL') })
    const client = createClient({ baseUrl: 'https://example.test', retry: false, fetch: http.fetch })

    await expect(client.types.get('t1')).rejects.toThrow()
    expect(http.calls).toHaveLength(1)
  })

  it('stops retrying when the caller aborts', async () => {
    const controller = new AbortController()
    const http = mockFetch({ status: 503, body: errorBody('INTERNAL') })
    http.fallback({ status: 503, body: errorBody('INTERNAL') })
    const client = createClient({
      baseUrl: 'https://example.test',
      retry: { ...defaultRetryPolicy(), baseDelayMs: 50, jitter: 0 },
      fetch: http.fetch,
    })

    const pending = client.types.get('t1', { signal: controller.signal }).catch((e: unknown) => e)
    controller.abort()
    await pending

    expect(http.calls).toHaveLength(1)
  })
})
