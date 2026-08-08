/**
 * The stable, machine-readable failure codes the API returns.
 *
 * This list is held equal to three others by tests: the `Error.code` enum in
 * `api/openapi.yaml`, the `ErrorCode` constants in `client/errors.go`, and the
 * list in `docs/api-stability.md`. See `test/error-codes.test.ts` here and
 * `TestErrorCodeContract` in the repository root.
 *
 * The service may add a code in a minor release. An existing code keeps its
 * meaning, so a `switch` on a code stays correct; treat an unknown code the
 * way you treat `INTERNAL`.
 */
export const ERROR_CODES = [
  'VALIDATION',
  'NOT_FOUND',
  'CONFLICT',
  'ARCHIVED',
  'DEPENDENCY_VIOLATION',
  'FEATURE_DISABLED',
  'CURSOR_CONFLICT',
  'CURSOR_EXPIRED',
  'UNAUTHENTICATED',
  'FORBIDDEN',
  'RATE_LIMITED',
  'INTERNAL',
] as const

/** One of the stable failure codes. */
export type ErrorCode = (typeof ERROR_CODES)[number]

const KNOWN_CODES: ReadonlySet<string> = new Set(ERROR_CODES)

/** The fields a FlexitypeError carries. */
export interface FlexitypeErrorInit {
  /** The stable failure code. */
  code: ErrorCode
  /** The server's human-readable message. */
  message: string
  /** The HTTP status. It is 0 when the request got no answer at all. */
  status: number
  /** The machine-readable context the server attached. */
  details?: Record<string, unknown> | undefined
  /** How long the server asked the caller to wait, in milliseconds. */
  retryAfterMs?: number | undefined
  /** The underlying failure, for a transport error. */
  cause?: unknown
}

/**
 * Every failure this client reports is a FlexitypeError.
 *
 * A transport failure — a refused connection, a DNS failure, an aborted
 * request — carries `status: 0` and the code `INTERNAL`, with the original
 * error in `cause`. So a caller can always read `.code` without a type guard
 * for "did we even reach the server".
 */
export class FlexitypeError extends Error {
  readonly code: ErrorCode
  readonly status: number
  readonly details: Record<string, unknown> | undefined
  readonly retryAfterMs: number | undefined

  constructor(init: FlexitypeErrorInit) {
    super(init.message, init.cause === undefined ? undefined : { cause: init.cause })
    this.name = 'FlexitypeError'
    this.code = init.code
    this.status = init.status
    this.details = init.details
    this.retryAfterMs = init.retryAfterMs
    // Restores the prototype chain when the package is compiled down to ES5
    // by a consumer's bundler, so `instanceof` keeps working.
    Object.setPrototypeOf(this, FlexitypeError.prototype)
  }
}

/** Reports whether the value is a FlexitypeError. */
export function isFlexitypeError(err: unknown): err is FlexitypeError {
  return err instanceof FlexitypeError
}

/** Reports whether the value is a FlexitypeError carrying this code. */
export function hasErrorCode(err: unknown, code: ErrorCode): boolean {
  return isFlexitypeError(err) && err.code === code
}

/** The request did not satisfy the API's rules. A bad cursor lands here too. */
export const isValidation = (err: unknown): boolean => hasErrorCode(err, 'VALIDATION')
/** The addressed object does not exist for this tenant. */
export const isNotFound = (err: unknown): boolean => hasErrorCode(err, 'NOT_FOUND')
/** The write lost a race, or it collides with a stored record. */
export const isConflict = (err: unknown): boolean => hasErrorCode(err, 'CONFLICT')
/** The object is archived. Restore it before editing it. */
export const isArchived = (err: unknown): boolean => hasErrorCode(err, 'ARCHIVED')
/** An attribute dependency refuses the value. */
export const isDependencyViolation = (err: unknown): boolean => hasErrorCode(err, 'DEPENDENCY_VIOLATION')
/** The deployment does not run this optional capability. Retrying cannot help. */
export const isFeatureDisabled = (err: unknown): boolean => hasErrorCode(err, 'FEATURE_DISABLED')
/** Another consumer committed the feed cursor first. Re-read it and retry. */
export const isCursorConflict = (err: unknown): boolean => hasErrorCode(err, 'CURSOR_CONFLICT')
/** The feed position is older than the retention. Re-baseline. */
export const isCursorExpired = (err: unknown): boolean => hasErrorCode(err, 'CURSOR_EXPIRED')
/** The token is missing or invalid. */
export const isUnauthenticated = (err: unknown): boolean => hasErrorCode(err, 'UNAUTHENTICATED')
/** The service account lacks the scope, or the field ACL denies the attribute. */
export const isForbidden = (err: unknown): boolean => hasErrorCode(err, 'FORBIDDEN')
/** The account's token bucket is empty. `retryAfterMs` says how long to wait. */
export const isRateLimited = (err: unknown): boolean => hasErrorCode(err, 'RATE_LIMITED')
/** The service failed, or the request never reached it (`status === 0`). */
export const isInternal = (err: unknown): boolean => hasErrorCode(err, 'INTERNAL')

/** The wire shape of an error response body. */
interface ErrorEnvelope {
  error?: {
    code?: string
    message?: string
    details?: Record<string, unknown>
  }
}

/**
 * Builds a FlexitypeError from a non-2xx response and its already-read body.
 *
 * A body that is not the documented envelope — an HTML error page from a proxy,
 * an empty 502 — becomes an INTERNAL error that still reports the real status,
 * because "the gateway answered 502" is more useful than "JSON parse failed".
 */
export function errorFromResponse(status: number, body: unknown, retryAfterMs?: number): FlexitypeError {
  const envelope = (body ?? {}) as ErrorEnvelope
  const wire = envelope.error
  const code = wire?.code
  if (typeof code === 'string' && KNOWN_CODES.has(code)) {
    return new FlexitypeError({
      code: code as ErrorCode,
      message: wire?.message ?? code,
      status,
      details: wire?.details,
      retryAfterMs,
    })
  }
  return new FlexitypeError({
    code: statusFallbackCode(status),
    message: wire?.message ?? `unexpected ${status} response`,
    status,
    details: wire?.details,
    retryAfterMs,
  })
}

/**
 * Maps a status to a code when the body carries no usable one. It covers the
 * hops in front of the service — a proxy 401, a load balancer 429 — which
 * answer with their own body and would otherwise all read as INTERNAL.
 */
function statusFallbackCode(status: number): ErrorCode {
  switch (status) {
    case 401:
      return 'UNAUTHENTICATED'
    case 403:
      return 'FORBIDDEN'
    case 404:
      return 'NOT_FOUND'
    case 409:
      return 'CONFLICT'
    case 422:
      return 'VALIDATION'
    case 429:
      return 'RATE_LIMITED'
    default:
      return 'INTERNAL'
  }
}

/** Wraps a transport failure, which has no status and no server message. */
export function transportError(method: string, path: string, cause: unknown): FlexitypeError {
  const reason = cause instanceof Error ? cause.message : String(cause)
  return new FlexitypeError({
    code: 'INTERNAL',
    message: `flexitype: ${method} ${path}: ${reason}`,
    status: 0,
    cause,
  })
}

/**
 * Reads both forms RFC 9110 allows for Retry-After: whole seconds, and an HTTP
 * date. A date in the past gives 0, not a negative wait.
 */
export function parseRetryAfter(header: string | null, now = Date.now()): number | undefined {
  if (!header) return undefined
  const trimmed = header.trim()
  if (trimmed === '') return undefined
  if (/^\d+$/.test(trimmed)) {
    const seconds = Number(trimmed)
    return seconds > 0 ? seconds * 1000 : 0
  }
  const when = Date.parse(trimmed)
  if (Number.isNaN(when)) return undefined
  const wait = when - now
  return wait > 0 ? wait : 0
}
