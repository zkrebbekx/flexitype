import { errorFromResponse, FlexitypeError, parseRetryAfter, transportError } from './errors.js'
import { retries, retriesTransport, sleep, waitBeforeMs, type RetryPolicy } from './retry.js'

/** A fetch implementation. The global one is used when none is supplied. */
export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

/** The options every request accepts. */
export interface RequestOptions {
  /** Cancels the request. The retry loop stops with it. */
  signal?: AbortSignal | undefined
  /** Extra headers for this request only. */
  headers?: Record<string, string> | undefined
}

/** A query string value. An array is joined with commas, as the API expects. */
export type QueryValue = string | number | boolean | string[] | undefined | null

/** The query parameters of one request. */
export type QueryParams = Record<string, QueryValue>

/** What Transport needs to send a request. */
export interface TransportConfig {
  /** The base URL including `/api/v1`, with no trailing slash. */
  baseUrl: string
  token: string | undefined
  userAgent: string | undefined
  headers: Record<string, string>
  retry: RetryPolicy
  fetch: FetchLike
}

interface RequestInitLike {
  query?: QueryParams | undefined
  body?: unknown
  /** A body already encoded — FormData for an upload. */
  rawBody?: BodyInit | undefined
  accept?: string
}

/**
 * Transport is the one place that talks to the network. Every service goes
 * through it, so authentication, error mapping and retrying are decided once.
 */
export class Transport {
  constructor(private readonly config: TransportConfig) {}

  /** The absolute URL of a path, with the query string applied. */
  url(path: string, query?: QueryParams): string {
    const search = encodeQuery(query)
    return `${this.config.baseUrl}${path}${search}`
  }

  /** Sends a request and decodes a JSON answer. A 204 resolves to undefined. */
  async request<T>(
    method: string,
    path: string,
    init: RequestInitLike = {},
    options: RequestOptions = {},
  ): Promise<T> {
    const response = await this.send(method, path, init, options)
    if (response.status === 204) return undefined as T
    const text = await response.text()
    if (text === '') return undefined as T
    try {
      return JSON.parse(text) as T
    } catch (cause) {
      throw new FlexitypeError({
        code: 'INTERNAL',
        message: `flexitype: ${method} ${path}: the response was not JSON`,
        status: response.status,
        cause,
      })
    }
  }

  /** Sends a request and returns the raw body — a CSV export, a media object. */
  async requestBlob(
    method: string,
    path: string,
    init: RequestInitLike = {},
    options: RequestOptions = {},
  ): Promise<{ blob: Blob; contentType: string }> {
    const response = await this.send(method, path, init, options)
    return {
      blob: await response.blob(),
      contentType: response.headers.get('Content-Type') ?? 'application/octet-stream',
    }
  }

  /**
   * Runs one request, retrying per the policy.
   *
   * The loop returns the last failure rather than a "gave up" error, so the
   * caller sees the real reason. An aborted signal ends it at once: a retry
   * must not outlive the caller's request.
   */
  private async send(
    method: string,
    path: string,
    init: RequestInitLike,
    options: RequestOptions,
  ): Promise<Response> {
    const url = this.url(path, init.query)
    const policy = this.config.retry
    let lastError: FlexitypeError | undefined

    for (let attempt = 1; ; attempt++) {
      let response: Response
      try {
        response = await this.config.fetch(url, this.buildInit(method, init, options))
      } catch (cause) {
        lastError = transportError(method, path, cause)
        if (
          attempt >= policy.maxAttempts ||
          !retriesTransport(policy, method) ||
          options.signal?.aborted === true ||
          !(await sleep(waitBeforeMs(policy, attempt + 1), options.signal))
        ) {
          throw lastError
        }
        continue
      }

      if (response.ok) return response

      const retryAfterMs = parseRetryAfter(response.headers.get('Retry-After'))
      lastError = errorFromResponse(response.status, await readErrorBody(response), retryAfterMs)
      if (
        attempt >= policy.maxAttempts ||
        !retries(policy, method, response.status) ||
        options.signal?.aborted === true ||
        !(await sleep(waitBeforeMs(policy, attempt + 1, retryAfterMs), options.signal))
      ) {
        throw lastError
      }
    }
  }

  private buildInit(method: string, init: RequestInitLike, options: RequestOptions): RequestInit {
    const headers: Record<string, string> = {
      Accept: init.accept ?? 'application/json',
      ...this.config.headers,
      ...options.headers,
    }
    if (this.config.token !== undefined && this.config.token !== '') {
      headers.Authorization = `Bearer ${this.config.token}`
    }
    // A browser refuses to set User-Agent, so it is opt-in rather than a
    // default: setting it unasked would log a console warning on every call.
    if (this.config.userAgent !== undefined && this.config.userAgent !== '') {
      headers['User-Agent'] = this.config.userAgent
    }

    let body: BodyInit | undefined
    if (init.rawBody !== undefined) {
      // FormData sets its own multipart boundary; naming a Content-Type here
      // would break the boundary and the upload with it.
      body = init.rawBody
    } else if (init.body !== undefined) {
      headers['Content-Type'] = 'application/json'
      body = JSON.stringify(init.body)
    }

    const requestInit: RequestInit = { method, headers }
    if (body !== undefined) requestInit.body = body
    if (options.signal !== undefined) requestInit.signal = options.signal
    return requestInit
  }
}

/** Reads an error body without letting a broken body mask the status. */
async function readErrorBody(response: Response): Promise<unknown> {
  try {
    const text = await response.text()
    return text === '' ? undefined : JSON.parse(text)
  } catch {
    return undefined
  }
}

/**
 * Encodes query parameters. An undefined, null or empty value is dropped, so a
 * caller can spread an options object without sending empty parameters. An
 * array joins with commas, which is the form every list parameter in the API
 * takes (`attributes`, `internal_name`, `types`).
 */
export function encodeQuery(query?: QueryParams): string {
  if (query === undefined) return ''
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '') continue
    if (Array.isArray(value)) {
      if (value.length === 0) continue
      search.set(key, value.join(','))
      continue
    }
    search.set(key, String(value))
  }
  const encoded = search.toString()
  return encoded === '' ? '' : `?${encoded}`
}

/** Escapes one path segment. An entity id may hold a slash or a space. */
export function segment(value: string): string {
  return encodeURIComponent(value)
}
