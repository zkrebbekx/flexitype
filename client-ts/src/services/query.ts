import type { RequestOptions } from '../http.js'
import type { ListOptions, Page } from '../pagination.js'
import { paginate, paginatePages } from '../pagination.js'
import type { EntitySummary } from '../models.js'
import { pageOf, pageQuery, requestPart, Service } from './base.js'

/** The arguments of an FQL query. */
export interface QueryOptions extends ListOptions {
  /** Narrows a localizable attribute to one locale. */
  locale?: string | undefined
  /** Narrows a scopable attribute to one channel. */
  channel?: string | undefined
}

/** The result of validating a query without running it. */
export interface ValidateQueryResult {
  valid?: boolean
}

/**
 * FQL query operations.
 *
 * FQL filters entities of one root type: `price > 10 and status = "active"`.
 * A malformed expression answers 422, which this client raises as a VALIDATION
 * FlexitypeError with the position in `details`.
 */
export class QueryService extends Service {
  /** One page of the entities the expression matches. */
  async run(type: string, q: string, options: QueryOptions = {}): Promise<Page<EntitySummary>> {
    return pageOf(
      await this.http.request<Page<EntitySummary>>(
        'GET',
        '/query',
        {
          query: {
            type,
            q,
            ...pageQuery(options),
            locale: options.locale,
            channel: options.channel,
          },
        },
        requestPart(options),
      ),
    )
  }

  /** Every matching entity, following the cursor across pages. */
  runAll(type: string, q: string, options: QueryOptions = {}): AsyncGenerator<EntitySummary, void, undefined> {
    return paginate((cursor) => this.run(type, q, { ...options, cursor }))
  }

  /** Every matching page, for a caller that needs the page boundaries. */
  runPages(
    type: string,
    q: string,
    options: QueryOptions = {},
  ): AsyncGenerator<Page<EntitySummary>, void, undefined> {
    return paginatePages((cursor) => this.run(type, q, { ...options, cursor }))
  }

  /** Parses the expression without running it. */
  validate(type: string, q: string, options: RequestOptions = {}): Promise<ValidateQueryResult> {
    return this.http.request<ValidateQueryResult>('POST', '/query/validate', { body: { type, q } }, options)
  }
}
