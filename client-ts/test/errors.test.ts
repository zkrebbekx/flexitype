import { describe, expect, it } from 'vitest'
import { createClient } from '../src/client.js'
import {
  ERROR_CODES,
  FlexitypeError,
  errorFromResponse,
  isArchived,
  isConflict,
  isCursorConflict,
  isCursorExpired,
  isDependencyViolation,
  isFeatureDisabled,
  isFlexitypeError,
  isForbidden,
  isNotFound,
  isRateLimited,
  isUnauthenticated,
  isValidation,
  parseRetryAfter,
  type ErrorCode,
} from '../src/errors.js'
import { errorBody, mockFetch } from './helpers.js'

const statusForCode: Record<ErrorCode, number> = {
  VALIDATION: 422,
  NOT_FOUND: 404,
  CONFLICT: 409,
  ARCHIVED: 409,
  DEPENDENCY_VIOLATION: 422,
  FEATURE_DISABLED: 501,
  CURSOR_CONFLICT: 409,
  CURSOR_EXPIRED: 410,
  UNAUTHENTICATED: 401,
  FORBIDDEN: 403,
  RATE_LIMITED: 429,
  INTERNAL: 500,
}

describe('error mapping', () => {
  it.each(ERROR_CODES)('maps the %s envelope onto a FlexitypeError', async (code) => {
    const status = statusForCode[code]
    const http = mockFetch({ status, body: errorBody(code, `${code} happened`, { attribute: 'sku' }) })
    const client = createClient({ baseUrl: 'https://example.test', retry: false, fetch: http.fetch })

    const error = await client.types.get('t1').catch((e: unknown) => e)

    expect(isFlexitypeError(error)).toBe(true)
    const flexitypeError = error as FlexitypeError
    expect(flexitypeError.code).toBe(code)
    expect(flexitypeError.status).toBe(status)
    expect(flexitypeError.message).toBe(`${code} happened`)
    expect(flexitypeError.details).toEqual({ attribute: 'sku' })
  })

  it('narrows with one helper per code', () => {
    const build = (code: ErrorCode) => new FlexitypeError({ code, message: '', status: 400 })
    expect(isValidation(build('VALIDATION'))).toBe(true)
    expect(isNotFound(build('NOT_FOUND'))).toBe(true)
    expect(isConflict(build('CONFLICT'))).toBe(true)
    expect(isArchived(build('ARCHIVED'))).toBe(true)
    expect(isDependencyViolation(build('DEPENDENCY_VIOLATION'))).toBe(true)
    expect(isFeatureDisabled(build('FEATURE_DISABLED'))).toBe(true)
    expect(isCursorConflict(build('CURSOR_CONFLICT'))).toBe(true)
    expect(isCursorExpired(build('CURSOR_EXPIRED'))).toBe(true)
    expect(isUnauthenticated(build('UNAUTHENTICATED'))).toBe(true)
    expect(isForbidden(build('FORBIDDEN'))).toBe(true)
    expect(isRateLimited(build('RATE_LIMITED'))).toBe(true)

    // A helper answers false for a different code and for a plain Error.
    expect(isNotFound(build('CONFLICT'))).toBe(false)
    expect(isNotFound(new Error('nope'))).toBe(false)
    expect(isNotFound(undefined)).toBe(false)
  })

  it('falls back to a status-derived code when the body is not the envelope', () => {
    // A proxy in front of the service answers with its own HTML page. The
    // status is still the useful fact, so it must survive.
    const error = errorFromResponse(429, '<html>too many requests</html>')
    expect(error.code).toBe('RATE_LIMITED')
    expect(error.status).toBe(429)
  })

  it('reports an unknown code as INTERNAL rather than trusting it', () => {
    const error = errorFromResponse(500, { error: { code: 'NOT_A_REAL_CODE', message: 'x' } })
    expect(error.code).toBe('INTERNAL')
  })

  it('carries a transport failure as INTERNAL with status 0', async () => {
    const http = mockFetch({ throws: new TypeError('connection refused') })
    const client = createClient({ baseUrl: 'https://example.test', retry: false, fetch: http.fetch })

    const error = (await client.types.get('t1').catch((e: unknown) => e)) as FlexitypeError
    expect(error.status).toBe(0)
    expect(error.code).toBe('INTERNAL')
    expect(error.message).toContain('connection refused')
  })

  it('reads both Retry-After forms and never returns a negative wait', () => {
    const now = Date.parse('2026-07-27T09:00:00Z')
    expect(parseRetryAfter('30', now)).toBe(30_000)
    expect(parseRetryAfter('Mon, 27 Jul 2026 09:00:10 GMT', now)).toBe(10_000)
    expect(parseRetryAfter('Mon, 27 Jul 2026 08:59:00 GMT', now)).toBe(0)
    expect(parseRetryAfter(null, now)).toBeUndefined()
    expect(parseRetryAfter('not a date', now)).toBeUndefined()
  })

  it('puts the server Retry-After on the error', async () => {
    const http = mockFetch({ status: 429, body: errorBody('RATE_LIMITED'), headers: { 'Retry-After': '7' } })
    const client = createClient({ baseUrl: 'https://example.test', retry: false, fetch: http.fetch })

    const error = (await client.types.get('t1').catch((e: unknown) => e)) as FlexitypeError
    expect(error.retryAfterMs).toBe(7000)
  })
})
