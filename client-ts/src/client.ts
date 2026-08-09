import { FlexitypeError } from './errors.js'
import type { FetchLike, RequestOptions } from './http.js'
import { Transport } from './http.js'
import type { Features, GraphQLResult } from './models.js'
import { defaultRetryPolicy, noRetryPolicy, type RetryPolicy } from './retry.js'
import { AdminService } from './services/admin.js'
import { AttributesService } from './services/attributes.js'
import { DependenciesService } from './services/dependencies.js'
import { EntitiesService } from './services/entities.js'
import { ChangeSetsService, EventsService, WebhooksService } from './services/events.js'
import {
  ActivityService,
  MatchRulesService,
  RevisionsService,
  SavedViewsService,
  UnitFamiliesService,
} from './services/misc.js'
import { QueryService } from './services/query.js'
import { RelationshipDefinitionsService, RelationshipsService } from './services/relationships.js'
import { SchemaService } from './services/schema.js'
import { TypesService } from './services/types.js'
import { ValuesService } from './services/values.js'

/** The options `createClient` accepts. */
export interface ClientOptions {
  /**
   * The service's base URL, e.g. `https://flexitype.internal:8080`. The
   * `/api/v1` prefix is added when it is absent.
   *
   * A path-only value such as `/` or `/api/v1` is allowed, for a browser app
   * served from the same origin as the API.
   */
  baseUrl: string

  /**
   * The service-account bearer token, `ft_<account>_<secret>`.
   *
   * THE TOKEN CARRIES THE TENANT. The service reads the tenant from the
   * authenticated account, so one client instance talks to exactly one
   * tenant. An application that serves several tenants builds one client per
   * tenant.
   *
   * The one exception is a credential holding the `read_any_tenant` scope,
   * which reads every tenant and writes none: see `tenant` below.
   *
   * Omit the token only against a development service that runs with
   * authentication disabled.
   */
  token?: string | undefined

  /**
   * The tenant to READ, sent as `X-Flexitype-Tenant`.
   *
   * It is only meaningful for a credential holding the `read_any_tenant`
   * scope — the one a cross-tenant read model uses. For every other
   * credential the service ignores it and reads the token's own tenant, so it
   * can never widen an ordinary credential.
   *
   * ```ts
   * const reader = createClient({ baseUrl, token: readerToken, tenant: 'merchant-a' })
   * ```
   */
  tenant?: string | undefined

  /**
   * The fetch implementation. It defaults to the global one, which Node 20 and
   * every supported browser provide. Supply your own to add tracing, a proxy
   * or a per-request timeout.
   */
  fetch?: FetchLike | undefined

  /**
   * The User-Agent header. A browser refuses to set it, so leave it unset in
   * browser code; it is meant for a server-side caller that wants its traffic
   * identifiable in the service's logs.
   */
  userAgent?: string | undefined

  /**
   * The retry policy. Pass `false` to disable retrying.
   *
   * The default retries an idempotent read up to three times on 429 and on the
   * transient 5xx statuses, honouring a `Retry-After` header. A write is never
   * retried, whatever the policy says.
   */
  retry?: RetryPolicy | false | undefined

  /** Extra headers sent with every request. */
  headers?: Record<string, string> | undefined
}

/**
 * A client for one flexitype tenant.
 *
 * It is safe to share between concurrent callers and it holds no per-request
 * state, so build it once and keep it.
 */
export interface FlexitypeClient {
  /** Type definitions and their inheritance. */
  readonly types: TypesService
  /** Attribute definitions. */
  readonly attributes: AttributesService
  /** Attribute values, including scoped ones. */
  readonly values: ValuesService
  /** Entities: browse, project, import, export, media, revisions. */
  readonly entities: EntitiesService
  /** FQL queries. */
  readonly query: QueryService
  /** Relationship definitions. */
  readonly relationshipDefinitions: RelationshipDefinitionsService
  /** Links between entities. */
  readonly relationships: RelationshipsService
  /** Attribute dependencies. */
  readonly dependencies: DependenciesService
  /** Schema export, import and templates. */
  readonly schema: SchemaService
  /** Saved views. */
  readonly savedViews: SavedViewsService
  /** Entity revisions. */
  readonly revisions: RevisionsService
  /** The audit log. */
  readonly activity: ActivityService
  /** Quantity unit families. */
  readonly unitFamilies: UnitFamiliesService
  /** Duplicate-detection rules. */
  readonly matchRules: MatchRulesService
  /** Staged change-sets. */
  readonly changeSets: ChangeSetsService
  /** Webhook subscriptions and deliveries. */
  readonly webhooks: WebhooksService
  /** The event feed and its consumer cursors. */
  readonly events: EventsService
  /** Tenant and service-account provisioning. */
  readonly admin: AdminService

  /** The deployment's enabled capabilities. */
  features(options?: RequestOptions): Promise<Features>

  /** Rebuilds the search projection. It returns how many entities it reindexed. */
  reindex(options?: RequestOptions): Promise<number>

  /** Rebuilds every computed attribute. It returns how many entities it touched. */
  recompute(options?: RequestOptions): Promise<number>

  /**
   * Runs a read-only GraphQL query. A query-level error arrives in the body
   * with a 200 status, so this raises a VALIDATION FlexitypeError rather than
   * handing back a result that looks successful.
   */
  graphql<T = unknown>(
    query: string,
    variables?: Record<string, unknown>,
    options?: RequestOptions,
  ): Promise<T>

  /**
   * The transport, for an endpoint this package does not wrap. Prefer a
   * service method: this bypasses the typed models.
   */
  readonly transport: Transport
}

/**
 * Builds a client for one tenant.
 *
 * ```ts
 * const client = createClient({
 *   baseUrl: 'https://flexitype.internal',
 *   token: process.env.FLEXITYPE_TOKEN,
 * })
 * const page = await client.types.list({ limit: 50 })
 * ```
 */
export function createClient(options: ClientOptions): FlexitypeClient {
  const transport = new Transport({
    baseUrl: normalizeBaseUrl(options.baseUrl),
    token: options.token,
    userAgent: options.userAgent,
    // The tenant a cross-tenant reader wants. It rides as a default header,
    // so every service on the client carries it without threading it through
    // each call.
    headers:
      options.tenant === undefined || options.tenant === ''
        ? (options.headers ?? {})
        : { 'X-Flexitype-Tenant': options.tenant, ...(options.headers ?? {}) },
    retry: options.retry === false ? noRetryPolicy() : (options.retry ?? defaultRetryPolicy()),
    fetch: options.fetch ?? resolveFetch(),
  })

  return {
    types: new TypesService(transport),
    attributes: new AttributesService(transport),
    values: new ValuesService(transport),
    entities: new EntitiesService(transport),
    query: new QueryService(transport),
    relationshipDefinitions: new RelationshipDefinitionsService(transport),
    relationships: new RelationshipsService(transport),
    dependencies: new DependenciesService(transport),
    schema: new SchemaService(transport),
    savedViews: new SavedViewsService(transport),
    revisions: new RevisionsService(transport),
    activity: new ActivityService(transport),
    unitFamilies: new UnitFamiliesService(transport),
    matchRules: new MatchRulesService(transport),
    changeSets: new ChangeSetsService(transport),
    webhooks: new WebhooksService(transport),
    events: new EventsService(transport),
    admin: new AdminService(transport),
    transport,

    features(requestOptions: RequestOptions = {}): Promise<Features> {
      return transport.request<Features>('GET', '/features', {}, requestOptions)
    },

    async reindex(requestOptions: RequestOptions = {}): Promise<number> {
      const body = await transport.request<{ reindexed?: number }>('POST', '/search/reindex', {}, requestOptions)
      return body.reindexed ?? 0
    },

    async recompute(requestOptions: RequestOptions = {}): Promise<number> {
      const body = await transport.request<{ recomputed?: number }>(
        'POST',
        '/computed/recompute',
        {},
        requestOptions,
      )
      return body.recomputed ?? 0
    },

    async graphql<T = unknown>(
      query: string,
      variables?: Record<string, unknown>,
      requestOptions: RequestOptions = {},
    ): Promise<T> {
      const body: Record<string, unknown> = { query }
      if (variables !== undefined) body.variables = variables
      const result = await transport.request<GraphQLResult>('POST', '/graphql', { body }, requestOptions)
      const errors = result.errors ?? []
      if (errors.length > 0) {
        const messages = errors.map((e) => String((e as { message?: unknown }).message ?? 'unknown error'))
        throw new FlexitypeError({
          code: 'VALIDATION',
          message: `flexitype: graphql: ${messages.join('; ')}`,
          status: 200,
          details: { errors },
        })
      }
      return result.data as T
    },
  }
}

/**
 * Normalizes a base URL and refuses one that cannot work.
 *
 * `url.parse` in Go and `new URL` here both accept "localhost:8080" as a URL
 * with the scheme "localhost", so the most natural thing to type for a local
 * service used to build a client that failed every request. Refusing it here
 * points the reader at the missing "http://" rather than at their network.
 */
export function normalizeBaseUrl(baseUrl: string): string {
  if (baseUrl === '') {
    throw new FlexitypeError({
      code: 'VALIDATION',
      message: 'flexitype: baseUrl is required',
      status: 0,
    })
  }

  const trimmed = baseUrl.replace(/\/+$/, '')

  // A path-only base is same-origin browser usage, which the admin console
  // relies on: it is served by the service itself and calls "/api/v1".
  if (trimmed.startsWith('/') || trimmed === '') {
    return withApiPrefix(trimmed)
  }

  let parsed: URL
  try {
    parsed = new URL(trimmed)
  } catch {
    throw new FlexitypeError({
      code: 'VALIDATION',
      message: `flexitype: invalid baseUrl ${JSON.stringify(baseUrl)}`,
      status: 0,
    })
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new FlexitypeError({
      code: 'VALIDATION',
      message: `flexitype: baseUrl must include a scheme, e.g. http://${baseUrl}`,
      status: 0,
    })
  }
  if (parsed.host === '') {
    throw new FlexitypeError({
      code: 'VALIDATION',
      message: `flexitype: baseUrl must include a host (got ${JSON.stringify(baseUrl)})`,
      status: 0,
    })
  }
  return withApiPrefix(trimmed)
}

function withApiPrefix(base: string): string {
  return base.endsWith('/api/v1') ? base : `${base}/api/v1`
}

function resolveFetch(): FetchLike {
  if (typeof globalThis.fetch !== 'function') {
    throw new FlexitypeError({
      code: 'INTERNAL',
      message: 'flexitype: no global fetch; supply one through the fetch option',
      status: 0,
    })
  }
  return globalThis.fetch.bind(globalThis) as FetchLike
}
