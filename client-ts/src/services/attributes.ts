import type { RequestOptions } from '../http.js'
import { segment } from '../http.js'
import type { ListOptions, Page } from '../pagination.js'
import { paginate } from '../pagination.js'
import type { Attribute, CreateAttribute, DataType, UpdateAttribute, ValidateValueResult } from '../models.js'
import { pageOf, pageQuery, requestPart, Service } from './base.js'

/** The filters `GET /attributes` accepts. */
export interface ListAttributesOptions extends ListOptions {
  /** Only attributes declared on this type. It does not follow inheritance. */
  typeDefinitionId?: string | undefined
  /** Only these internal names. */
  internalName?: string[] | undefined
  /** Only these data types. */
  dataType?: DataType[] | undefined
  /** Include archived attributes. */
  includeArchived?: boolean | undefined
}

/** Attribute-definition operations. */
export class AttributesService extends Service {
  /** One page of attribute definitions. */
  async list(options: ListAttributesOptions = {}): Promise<Page<Attribute>> {
    return pageOf(
      await this.http.request<Page<Attribute>>(
        'GET',
        '/attributes',
        {
          query: {
            ...pageQuery(options),
            type_definition_id: options.typeDefinitionId,
            internal_name: options.internalName,
            data_type: options.dataType,
            include_archived: options.includeArchived,
          },
        },
        requestPart(options),
      ),
    )
  }

  /** Every matching attribute, following the cursor across pages. */
  listAll(options: ListAttributesOptions = {}): AsyncGenerator<Attribute, void, undefined> {
    return paginate((cursor) => this.list({ ...options, cursor }))
  }

  /** One attribute definition by id. */
  get(id: string, options: RequestOptions = {}): Promise<Attribute> {
    return this.http.request<Attribute>('GET', `/attributes/${segment(id)}`, {}, options)
  }

  /** Declares an attribute on a type. */
  create(input: CreateAttribute, options: RequestOptions = {}): Promise<Attribute> {
    return this.http.request<Attribute>('POST', '/attributes', { body: input }, options)
  }

  /**
   * Updates an attribute. The body replaces the whole editable record, so send
   * the fields you want to keep as well as the ones you change.
   */
  update(id: string, input: UpdateAttribute, options: RequestOptions = {}): Promise<Attribute> {
    return this.http.request<Attribute>('PATCH', `/attributes/${segment(id)}`, { body: input }, options)
  }

  /** Archives an attribute. Its stored values stay readable. */
  archive(id: string, options: RequestOptions = {}): Promise<Attribute> {
    return this.http.request<Attribute>('POST', `/attributes/${segment(id)}/archive`, {}, options)
  }

  /** Restores an archived attribute. */
  restore(id: string, options: RequestOptions = {}): Promise<Attribute> {
    return this.http.request<Attribute>('POST', `/attributes/${segment(id)}/restore`, {}, options)
  }

  /**
   * Checks a candidate value against the attribute's constraints without
   * writing it. A valid value answers `{ valid: true }`; an invalid one
   * answers 422, which this client raises as a VALIDATION FlexitypeError.
   */
  validateValue(id: string, value: unknown, options: RequestOptions = {}): Promise<ValidateValueResult> {
    return this.http.request<ValidateValueResult>(
      'POST',
      `/attributes/${segment(id)}/validate-value`,
      { body: { value } },
      options,
    )
  }
}
