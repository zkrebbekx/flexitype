import type { RequestOptions } from '../http.js'
import { segment } from '../http.js'
import type { ListOptions, Page } from '../pagination.js'
import { paginate } from '../pagination.js'
import type {
  AttributeSetIDs,
  CreateRelationshipDefinition,
  Link,
  Relationship,
  RelationshipDefinition,
  UpdateDisplay,
} from '../models.js'
import { pageOf, pageQuery, requestPart, Service } from './base.js'

/** The filters `GET /relationship-definitions` accepts. */
export interface ListRelationshipDefinitionsOptions extends ListOptions {
  /** Only definitions with this type on either side. */
  typeDefinitionId?: string | undefined
  includeArchived?: boolean | undefined
}

/** Relationship-definition operations. */
export class RelationshipDefinitionsService extends Service {
  /** One page of relationship definitions. */
  async list(options: ListRelationshipDefinitionsOptions = {}): Promise<Page<RelationshipDefinition>> {
    return pageOf(
      await this.http.request<Page<RelationshipDefinition>>(
        'GET',
        '/relationship-definitions',
        {
          query: {
            ...pageQuery(options),
            type_definition_id: options.typeDefinitionId,
            include_archived: options.includeArchived,
          },
        },
        requestPart(options),
      ),
    )
  }

  /** Every relationship definition, following the cursor across pages. */
  listAll(
    options: ListRelationshipDefinitionsOptions = {},
  ): AsyncGenerator<RelationshipDefinition, void, undefined> {
    return paginate((cursor) => this.list({ ...options, cursor }))
  }

  /** One relationship definition by id. */
  get(id: string, options: RequestOptions = {}): Promise<RelationshipDefinition> {
    return this.http.request<RelationshipDefinition>('GET', `/relationship-definitions/${segment(id)}`, {}, options)
  }

  /** Declares a relationship between two types. */
  create(input: CreateRelationshipDefinition, options: RequestOptions = {}): Promise<RelationshipDefinition> {
    return this.http.request<RelationshipDefinition>(
      'POST',
      '/relationship-definitions',
      { body: input },
      options,
    )
  }

  /** Renames a relationship definition and adjusts its cardinality bounds. */
  update(
    id: string,
    input: UpdateDisplay & Partial<CreateRelationshipDefinition>,
    options: RequestOptions = {},
  ): Promise<RelationshipDefinition> {
    return this.http.request<RelationshipDefinition>(
      'PATCH',
      `/relationship-definitions/${segment(id)}`,
      { body: input },
      options,
    )
  }

  /** Archives a relationship definition. */
  archive(id: string, options: RequestOptions = {}): Promise<RelationshipDefinition> {
    return this.http.request<RelationshipDefinition>(
      'POST',
      `/relationship-definitions/${segment(id)}/archive`,
      {},
      options,
    )
  }

  /** Restores an archived relationship definition. */
  restore(id: string, options: RequestOptions = {}): Promise<RelationshipDefinition> {
    return this.http.request<RelationshipDefinition>(
      'POST',
      `/relationship-definitions/${segment(id)}/restore`,
      {},
      options,
    )
  }

  /** The attribute-set type ids a link of this definition may carry values on. */
  attributeSets(id: string, options: RequestOptions = {}): Promise<AttributeSetIDs> {
    return this.http.request<AttributeSetIDs>(
      'GET',
      `/relationship-definitions/${segment(id)}/attribute-sets`,
      {},
      options,
    )
  }
}

/** The filters `GET /relationships` accepts. */
export interface ListRelationshipsOptions extends ListOptions {
  relationshipDefinitionId?: string | undefined
  parentEntityId?: string | undefined
  childEntityId?: string | undefined
}

/** Relationship (link) operations. */
export class RelationshipsService extends Service {
  /** One page of links. */
  async list(options: ListRelationshipsOptions = {}): Promise<Page<Relationship>> {
    return pageOf(
      await this.http.request<Page<Relationship>>(
        'GET',
        '/relationships',
        {
          query: {
            ...pageQuery(options),
            relationship_definition_id: options.relationshipDefinitionId,
            parent_entity_id: options.parentEntityId,
            child_entity_id: options.childEntityId,
          },
        },
        requestPart(options),
      ),
    )
  }

  /** Every matching link, following the cursor across pages. */
  listAll(options: ListRelationshipsOptions = {}): AsyncGenerator<Relationship, void, undefined> {
    return paginate((cursor) => this.list({ ...options, cursor }))
  }

  /** One link by id. */
  get(id: string, options: RequestOptions = {}): Promise<Relationship> {
    return this.http.request<Relationship>('GET', `/relationships/${segment(id)}`, {}, options)
  }

  /** Links two entities under a relationship definition. */
  link(input: Link, options: RequestOptions = {}): Promise<Relationship> {
    return this.http.request<Relationship>('POST', '/relationships', { body: input }, options)
  }

  /** Archives a link. It returns the archived snapshot. */
  unlink(id: string, options: RequestOptions = {}): Promise<Relationship> {
    return this.http.request<Relationship>('DELETE', `/relationships/${segment(id)}`, {}, options)
  }
}
