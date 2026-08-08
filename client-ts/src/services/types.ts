import type { RequestOptions } from '../http.js'
import { segment } from '../http.js'
import type { ListOptions, Page } from '../pagination.js'
import { paginate } from '../pagination.js'
import type {
  Attribute,
  CloneResult,
  CloneType,
  CreateMatchRule,
  CreateTypeDefinition,
  EffectiveAttribute,
  MatchRule,
  TypeCompleteness,
  TypeDefinition,
  UpdateDisplay,
} from '../models.js'
import { itemsOf, pageOf, pageQuery, requestPart, Service } from './base.js'

/** The filters `GET /type-definitions` accepts. */
export interface ListTypesOptions extends ListOptions {
  /** Only these internal names. */
  internalName?: string[] | undefined
  /** Include archived types. They are hidden by default. */
  includeArchived?: boolean | undefined
}

/** Type-definition operations. */
export class TypesService extends Service {
  /** One page of type definitions. */
  async list(options: ListTypesOptions = {}): Promise<Page<TypeDefinition>> {
    return pageOf(
      await this.http.request<Page<TypeDefinition>>(
        'GET',
        '/type-definitions',
        {
          query: {
            ...pageQuery(options),
            internal_name: options.internalName,
            include_archived: options.includeArchived,
          },
        },
        requestPart(options),
      ),
    )
  }

  /** Every type definition, following the cursor across pages. */
  listAll(options: ListTypesOptions = {}): AsyncGenerator<TypeDefinition, void, undefined> {
    return paginate((cursor) => this.list({ ...options, cursor }))
  }

  /** One type definition by id. */
  get(id: string, options: RequestOptions = {}): Promise<TypeDefinition> {
    return this.http.request<TypeDefinition>('GET', `/type-definitions/${segment(id)}`, {}, options)
  }

  /** Creates a type. `extends_id` names the parent type and is immutable. */
  create(input: CreateTypeDefinition, options: RequestOptions = {}): Promise<TypeDefinition> {
    return this.http.request<TypeDefinition>('POST', '/type-definitions', { body: input }, options)
  }

  /** Renames a type. The internal name never changes. */
  update(id: string, input: UpdateDisplay, options: RequestOptions = {}): Promise<TypeDefinition> {
    return this.http.request<TypeDefinition>('PATCH', `/type-definitions/${segment(id)}`, { body: input }, options)
  }

  /** Archives a type. Its entities stay readable and become unwritable. */
  archive(id: string, options: RequestOptions = {}): Promise<TypeDefinition> {
    return this.http.request<TypeDefinition>('POST', `/type-definitions/${segment(id)}/archive`, {}, options)
  }

  /** Restores an archived type. */
  restore(id: string, options: RequestOptions = {}): Promise<TypeDefinition> {
    return this.http.request<TypeDefinition>('POST', `/type-definitions/${segment(id)}/restore`, {}, options)
  }

  /** Copies a type with its attributes and dependencies under a new name. */
  clone(id: string, input: CloneType, options: RequestOptions = {}): Promise<CloneResult> {
    return this.http.request<CloneResult>('POST', `/type-definitions/${segment(id)}/clone`, { body: input }, options)
  }

  /** One page of the attributes this type declares itself. */
  async attributes(id: string, options: ListOptions = {}): Promise<Page<Attribute>> {
    return pageOf(
      await this.http.request<Page<Attribute>>(
        'GET',
        `/type-definitions/${segment(id)}/attributes`,
        { query: pageQuery(options) },
        requestPart(options),
      ),
    )
  }

  /**
   * The attributes an entity of this type may hold: the type's own plus every
   * attribute it inherits through `extends_id`.
   *
   * This is what a dynamic form or a grid renders from. `toFormDescriptor` in
   * the soft-type helpers turns the answer into renderable fields.
   */
  async effectiveAttributes(id: string, options: RequestOptions = {}): Promise<EffectiveAttribute[]> {
    return itemsOf(
      await this.http.request<{ items?: EffectiveAttribute[] }>(
        'GET',
        `/type-definitions/${segment(id)}/effective-attributes`,
        {},
        options,
      ),
    )
  }

  /** The direct subtypes of this type. */
  async children(id: string, options: RequestOptions = {}): Promise<TypeDefinition[]> {
    return itemsOf(
      await this.http.request<{ items?: TypeDefinition[] }>(
        'GET',
        `/type-definitions/${segment(id)}/children`,
        {},
        options,
      ),
    )
  }

  /** How complete this type's entities are against their required schema. */
  completeness(id: string, options: RequestOptions = {}): Promise<TypeCompleteness> {
    return this.http.request<TypeCompleteness>('GET', `/type-definitions/${segment(id)}/completeness`, {}, options)
  }

  /** The duplicate-detection rules declared on this type. */
  async matchRules(id: string, options: RequestOptions = {}): Promise<MatchRule[]> {
    return itemsOf(
      await this.http.request<{ items?: MatchRule[] }>(
        'GET',
        `/type-definitions/${segment(id)}/match-rules`,
        {},
        options,
      ),
    )
  }

  /** Declares a duplicate-detection rule on this type. */
  createMatchRule(id: string, input: CreateMatchRule, options: RequestOptions = {}): Promise<MatchRule> {
    return this.http.request<MatchRule>(
      'POST',
      `/type-definitions/${segment(id)}/match-rules`,
      { body: input },
      options,
    )
  }
}
