import type { RequestOptions } from '../http.js'
import { segment } from '../http.js'
import type { ListOptions, Page } from '../pagination.js'
import { paginate } from '../pagination.js'
import type { AttributeValue, SetValue } from '../models.js'
import { itemsOf, pageOf, pageQuery, requestPart, Service } from './base.js'

/** The filters `GET /values` accepts. */
export interface ListValuesOptions extends ListOptions {
  typeDefinitionId?: string | undefined
  /** One attribute across entities — the "who holds a value for this" query. */
  attributeDefinitionId?: string | undefined
  entityId?: string | undefined
  includeArchived?: boolean | undefined
  /** Overlays a draft change-set's staged mutations on the stored values. */
  changeset?: string | undefined
}

/** Attribute-value operations. */
export class ValuesService extends Service {
  /** One page of stored values. */
  async list(options: ListValuesOptions = {}): Promise<Page<AttributeValue>> {
    return pageOf(
      await this.http.request<Page<AttributeValue>>(
        'GET',
        '/values',
        {
          query: {
            ...pageQuery(options),
            type_definition_id: options.typeDefinitionId,
            attribute_definition_id: options.attributeDefinitionId,
            entity_id: options.entityId,
            include_archived: options.includeArchived,
            changeset: options.changeset,
          },
        },
        requestPart(options),
      ),
    )
  }

  /** Every matching value, following the cursor across pages. */
  listAll(options: ListValuesOptions = {}): AsyncGenerator<AttributeValue, void, undefined> {
    return paginate((cursor) => this.list({ ...options, cursor }))
  }

  /** One stored value by id. */
  get(id: string, options: RequestOptions = {}): Promise<AttributeValue> {
    return this.http.request<AttributeValue>('GET', `/values/${segment(id)}`, {}, options)
  }

  /**
   * Writes one attribute value. It is an upsert: writing an attribute that
   * already holds a value replaces it.
   *
   * `locale` and `channel` address a scoped value. A localizable attribute
   * accepts a locale, a scopable one accepts a channel, and an attribute that
   * is neither refuses both with a VALIDATION error. Build the body with
   * `scopedValueInput` to keep the scope and the value together.
   */
  set(input: SetValue, options: RequestOptions = {}): Promise<AttributeValue> {
    return this.http.request<AttributeValue>('POST', '/values', { body: input }, options)
  }

  /**
   * Writes many values in one transaction: either every item is applied and
   * its events fire, or the whole batch rolls back. The maximum is 1000 items.
   */
  async setBatch(items: SetValue[], options: RequestOptions = {}): Promise<AttributeValue[]> {
    return itemsOf(
      await this.http.request<{ items?: AttributeValue[] }>('POST', '/values/batch', { body: { items } }, options),
    )
  }

  /** Archives one stored value. It returns the archived snapshot. */
  remove(id: string, options: RequestOptions = {}): Promise<AttributeValue> {
    return this.http.request<AttributeValue>('DELETE', `/values/${segment(id)}`, {}, options)
  }
}
