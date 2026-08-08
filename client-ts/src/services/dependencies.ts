import type { RequestOptions } from '../http.js'
import { segment } from '../http.js'
import type { ListOptions, Page } from '../pagination.js'
import { paginate } from '../pagination.js'
import type { CreateDependency, Dependency, UpdateDependency } from '../models.js'
import { pageOf, pageQuery, requestPart, Service } from './base.js'

/** The filters `GET /dependencies` accepts. */
export interface ListDependenciesOptions extends ListOptions {
  /** Only rules whose source is this attribute. */
  sourceAttributeId?: string | undefined
  /** Only rules whose target is this attribute. */
  targetAttributeId?: string | undefined
}

/**
 * Attribute-dependency operations.
 *
 * A dependency makes one attribute's rules depend on another's value: it is
 * how a cascading picklist and a conditional requirement are declared. The
 * resolved outcome for one entity is read from
 * `entities.effectiveSchema(...)`.
 */
export class DependenciesService extends Service {
  /** One page of dependency rules. */
  async list(options: ListDependenciesOptions = {}): Promise<Page<Dependency>> {
    return pageOf(
      await this.http.request<Page<Dependency>>(
        'GET',
        '/dependencies',
        {
          query: {
            ...pageQuery(options),
            source_attribute_id: options.sourceAttributeId,
            target_attribute_id: options.targetAttributeId,
          },
        },
        requestPart(options),
      ),
    )
  }

  /** Every matching rule, following the cursor across pages. */
  listAll(options: ListDependenciesOptions = {}): AsyncGenerator<Dependency, void, undefined> {
    return paginate((cursor) => this.list({ ...options, cursor }))
  }

  /** One rule by id. */
  get(id: string, options: RequestOptions = {}): Promise<Dependency> {
    return this.http.request<Dependency>('GET', `/dependencies/${segment(id)}`, {}, options)
  }

  /** Declares a rule. */
  create(input: CreateDependency, options: RequestOptions = {}): Promise<Dependency> {
    return this.http.request<Dependency>('POST', '/dependencies', { body: input }, options)
  }

  /** Replaces a rule's conditions and effect. */
  update(id: string, input: UpdateDependency, options: RequestOptions = {}): Promise<Dependency> {
    return this.http.request<Dependency>('PATCH', `/dependencies/${segment(id)}`, { body: input }, options)
  }

  /** Archives a rule. It returns the archived snapshot. */
  archive(id: string, options: RequestOptions = {}): Promise<Dependency> {
    return this.http.request<Dependency>('DELETE', `/dependencies/${segment(id)}`, {}, options)
  }
}
