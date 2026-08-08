/**
 * Retry policy. It mirrors `client/retry.go`, so a team running both clients
 * meets one behaviour.
 *
 * Only an idempotent method is ever retried — GET, HEAD, PUT and DELETE. A
 * POST is left alone whatever the policy says, because a POST that creates a
 * value may have been applied before the connection broke, and replaying it
 * would write it twice.
 */
export interface RetryPolicy {
  /** Counts the first try. 1 disables retrying. */
  maxAttempts: number
  /** The wait before the second attempt. Each further attempt doubles it. */
  baseDelayMs: number
  /** The cap on the computed backoff. */
  maxDelayMs: number
  /**
   * The random part of each wait, as a fraction of the computed delay. 0.2
   * means plus or minus 20%. Without it, many clients throttled at once retry
   * in lockstep and throttle each other again.
   */
  jitter: number
  /** The response statuses worth another attempt. */
  retryStatuses: number[]
}

/**
 * The default policy: three attempts on 429 and on the transient 5xx statuses
 * a proxy or a restarting server emits, backing off from 200ms to 5s with plus
 * or minus 20% jitter.
 *
 * 422 is deliberately absent. The API answers 422 for a cursor of the wrong
 * arity or an unparseable value, and that is a permanent fault in the request:
 * retrying it only repeats the same answer.
 */
export function defaultRetryPolicy(): RetryPolicy {
  return {
    maxAttempts: 3,
    baseDelayMs: 200,
    maxDelayMs: 5000,
    jitter: 0.2,
    retryStatuses: [429, 502, 503, 504],
  }
}

/** A policy that never retries. */
export function noRetryPolicy(): RetryPolicy {
  return { maxAttempts: 1, baseDelayMs: 0, maxDelayMs: 0, jitter: 0, retryStatuses: [] }
}

/** Reports whether the method may be replayed safely. */
export function isIdempotent(method: string): boolean {
  switch (method.toUpperCase()) {
    case 'GET':
    case 'HEAD':
    case 'PUT':
    case 'DELETE':
      return true
    default:
      return false
  }
}

/** Reports whether the policy retries this method and status. */
export function retries(policy: RetryPolicy, method: string, status: number): boolean {
  if (policy.maxAttempts < 2 || !isIdempotent(method)) return false
  return policy.retryStatuses.includes(status)
}

/**
 * Reports whether a transport failure is worth another attempt. The request
 * may not have reached the service at all, so this is still limited to an
 * idempotent method.
 */
export function retriesTransport(policy: RetryPolicy, method: string): boolean {
  return policy.maxAttempts >= 2 && isIdempotent(method)
}

/**
 * The delay before attempt number `n`, where n is 2 for the first retry.
 *
 * A server hint replaces the computed backoff, even past maxDelayMs: the
 * service knows when its token bucket refills and the client does not, and
 * waiting less than it asks only earns another 429.
 */
export function waitBeforeMs(
  policy: RetryPolicy,
  n: number,
  serverHintMs?: number,
  random: () => number = Math.random,
): number {
  if (serverHintMs !== undefined && serverHintMs > 0) return serverHintMs
  let delay = policy.baseDelayMs
  for (let i = 2; i < n; i++) {
    delay *= 2
    if (policy.maxDelayMs > 0 && delay > policy.maxDelayMs) {
      delay = policy.maxDelayMs
      break
    }
  }
  if (policy.jitter > 0) {
    const spread = delay * policy.jitter
    delay = delay - spread + random() * 2 * spread
  }
  return delay > 0 ? delay : 0
}

/**
 * Waits for `ms`, or resolves early when the signal aborts. It reports whether
 * the wait completed: an aborted wait must end the retry loop rather than
 * outlive the caller's request.
 */
export function sleep(ms: number, signal?: AbortSignal): Promise<boolean> {
  if (signal?.aborted) return Promise.resolve(false)
  if (ms <= 0) return Promise.resolve(true)
  return new Promise<boolean>((resolve) => {
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort)
      resolve(true)
    }, ms)
    const onAbort = () => {
      clearTimeout(timer)
      resolve(false)
    }
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}
