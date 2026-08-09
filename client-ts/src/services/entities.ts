import type { RequestOptions } from '../http.js'
import { segment } from '../http.js'
import type { ListOptions, Page, PageInfo } from '../pagination.js'
import { paginate } from '../pagination.js'
import type {
  AppliedDefaults,
  AttributeValue,
  Completeness,
  EffectiveSchema,
  EntityLink,
  EntitySummary,
  Facets,
  GridResult,
  ImportReport,
  PurgeReport,
  RelationshipRequirement,
  Revision,
} from '../models.js'
import { itemsOf, pageOf, pageQuery, requestPart, Service } from './base.js'

/** The filters `GET /entities/{type}` accepts. */
export interface ListEntitiesOptions extends ListOptions {
  /** Also list entities of the subtypes of this type. */
  includeDescendants?: boolean | undefined
}

/** The arguments of a grid projection. */
export interface GridOptions extends ListOptions {
  /** The attribute internal names to project as columns. */
  attributes: string[]
  /** An FQL filter over the rows. */
  query?: string | undefined
  includeDescendants?: boolean | undefined
}

/** The arguments of a facet count. */
export interface FacetOptions extends RequestOptions {
  attributes: string[]
  query?: string | undefined
}

/** The arguments of a CSV export. */
export interface ExportOptions extends RequestOptions {
  /** The attribute internal names to write as columns. */
  attributes?: string[] | undefined
  /** An FQL filter over the rows. */
  query?: string | undefined
  /** Explicit entity ids, instead of a filter. */
  entityIds?: string[] | undefined
}

/** The JSON `mapping` part of a CSV import. */
export interface ImportMapping {
  /** The CSV column holding the entity id. */
  key_column: string
  /** CSV column name to attribute internal name. */
  mapping: Record<string, string>
  /**
   * `transactional` applies every row or none. `best_effort` writes the valid
   * rows and reports the rest.
   */
  mode?: 'best_effort' | 'transactional'
  /** Validates without writing. */
  dry_run?: boolean
  /** Opts in to the pre-1.5 in-band multi-value cell forms. It is off by default. */
  allow_legacy_multi_value_cells?: boolean
}

/** A grid page: the projected columns plus the cursor for the next page. */
export interface GridPage extends GridResult {
  page_info?: PageInfo
}

/** The receipt a cascading delete returns. */
export interface RemoveEntityResult {
  entity_id?: string
  values_removed?: number
  relationships_gone?: number
}

/** A signed, expiring link to one stored media object. */
export interface SignedMediaUrl {
  /** The path to fetch, relative to the service root. */
  url: string
  /** When the link stops working, as an RFC 3339 timestamp. */
  expires_at: string
}

/** Entity-level operations: browse, project, import, export, media, revisions. */
export class EntitiesService extends Service {
  /** One page of a type's entities. */
  async list(typeId: string, options: ListEntitiesOptions = {}): Promise<Page<EntitySummary>> {
    return pageOf(
      await this.http.request<Page<EntitySummary>>(
        'GET',
        `/entities/${segment(typeId)}`,
        { query: { ...pageQuery(options), include_descendants: options.includeDescendants } },
        requestPart(options),
      ),
    )
  }

  /** Every entity of a type, following the cursor across pages. */
  listAll(typeId: string, options: ListEntitiesOptions = {}): AsyncGenerator<EntitySummary, void, undefined> {
    return paginate((cursor) => this.list(typeId, { ...options, cursor }))
  }

  /**
   * An entity's live values, one row per (attribute, locale, channel).
   *
   * Pass `changeset` to preview a draft change-set's staged mutations overlaid
   * on the stored values. Group the answer with `groupScopedValues` to address
   * a value by its scope.
   */
  async values(
    typeId: string,
    entityId: string,
    options: RequestOptions & { changeset?: string | undefined } = {},
  ): Promise<AttributeValue[]> {
    return itemsOf(
      await this.http.request<{ items?: AttributeValue[] }>(
        'GET',
        `/entities/${segment(typeId)}/${segment(entityId)}/values`,
        { query: { changeset: options.changeset } },
        requestPart(options),
      ),
    )
  }

  /** Every relationship the entity takes part in, with its role resolved. */
  async relationships(typeId: string, entityId: string, options: RequestOptions = {}): Promise<EntityLink[]> {
    return itemsOf(
      await this.http.request<{ items?: EntityLink[] }>(
        'GET',
        `/entities/${segment(typeId)}/${segment(entityId)}/relationships`,
        {},
        options,
      ),
    )
  }

  /**
   * One attribute's dependency-resolved state for one entity: whether the
   * dependencies make it required, and which values they still allow. A
   * cascading picklist reads its options from here.
   */
  effectiveSchema(
    typeId: string,
    entityId: string,
    attributeId: string,
    options: RequestOptions = {},
  ): Promise<EffectiveSchema> {
    return this.http.request<EffectiveSchema>(
      'GET',
      `/entities/${segment(typeId)}/${segment(entityId)}/attributes/${segment(attributeId)}/effective-schema`,
      {},
      options,
    )
  }

  /** How much of the entity's required schema is filled. */
  completeness(typeId: string, entityId: string, options: RequestOptions = {}): Promise<Completeness> {
    return this.http.request<Completeness>(
      'GET',
      `/entities/${segment(typeId)}/${segment(entityId)}/completeness`,
      {},
      options,
    )
  }

  /** The relationship definitions whose minimum cardinality the entity fails. */
  async relationshipRequirements(
    typeId: string,
    entityId: string,
    options: RequestOptions = {},
  ): Promise<RelationshipRequirement[]> {
    return itemsOf(
      await this.http.request<{ items?: RelationshipRequirement[] }>(
        'GET',
        `/entities/${segment(typeId)}/${segment(entityId)}/relationship-requirements`,
        {},
        options,
      ),
    )
  }

  /** Writes every declared default the entity has no base-scope value for. */
  applyDefaults(typeId: string, entityId: string, options: RequestOptions = {}): Promise<AppliedDefaults> {
    return this.http.request<AppliedDefaults>(
      'POST',
      `/entities/${segment(typeId)}/${segment(entityId)}/apply-defaults`,
      {},
      options,
    )
  }

  /** The entity's state as of an instant, rebuilt from its revisions. */
  asOf(typeId: string, entityId: string, at: string, options: RequestOptions = {}): Promise<Revision> {
    return this.http.request<Revision>(
      'GET',
      `/entities/${segment(typeId)}/${segment(entityId)}/as-of`,
      { query: { at } },
      options,
    )
  }

  /** The entity's saved revisions, newest first. */
  async revisions(typeId: string, entityId: string, options: RequestOptions = {}): Promise<Revision[]> {
    return itemsOf(
      await this.http.request<{ items?: Revision[] }>(
        'GET',
        `/entities/${segment(typeId)}/${segment(entityId)}/revisions`,
        {},
        options,
      ),
    )
  }

  /** Saves a labelled revision of the entity's current values. */
  createRevision(typeId: string, entityId: string, label: string, options: RequestOptions = {}): Promise<Revision> {
    return this.http.request<Revision>(
      'POST',
      `/entities/${segment(typeId)}/${segment(entityId)}/revisions`,
      { body: { label } },
      options,
    )
  }

  /**
   * Soft-deletes an entity: it archives every live value and unlinks every
   * live relationship in one unit of work. Use `purge` for a hard delete.
   */
  remove(typeId: string, entityId: string, options: RequestOptions = {}): Promise<RemoveEntityResult> {
    return this.http.request<RemoveEntityResult>(
      'DELETE',
      `/entities/${segment(typeId)}/${segment(entityId)}`,
      {},
      options,
    )
  }

  /**
   * Hard-deletes every trace of the entity, including archived values,
   * revisions and media blobs. This is the right-to-erasure primitive. It is
   * irreversible and it needs the admin scope.
   *
   * Read `media_blobs_failed` in the receipt before you record the request as
   * satisfied: a non-zero count means data remains in object storage.
   */
  purge(typeId: string, entityId: string, options: RequestOptions = {}): Promise<PurgeReport> {
    return this.http.request<PurgeReport>(
      'POST',
      `/entities/${segment(typeId)}/${segment(entityId)}/purge`,
      {},
      options,
    )
  }

  /**
   * Projects a page of entities onto chosen attribute columns. It is one
   * round-trip plus one batched value load, so a table does not issue a
   * request per row.
   */
  grid(typeId: string, options: GridOptions): Promise<GridPage> {
    return this.http.request<GridPage>(
      'GET',
      `/entities/${segment(typeId)}/grid`,
      {
        query: {
          ...pageQuery(options),
          attributes: options.attributes,
          query: options.query,
          include_descendants: options.includeDescendants,
        },
      },
      requestPart(options),
    )
  }

  /** Counts the distinct values of chosen attributes across a result set. */
  facets(typeId: string, options: FacetOptions): Promise<Facets> {
    return this.http.request<Facets>(
      'GET',
      `/entities/${segment(typeId)}/facets`,
      { query: { attributes: options.attributes, query: options.query } },
      requestPart(options),
    )
  }

  /** The URL of a CSV export, for a download link the browser follows itself. */
  exportUrl(typeId: string, options: Omit<ExportOptions, keyof RequestOptions> = {}): string {
    return this.http.url(`/entities/${segment(typeId)}/export`, {
      attributes: options.attributes,
      query: options.query,
      entity_ids: options.entityIds,
    })
  }

  /** Downloads a CSV export as a blob. */
  async export(typeId: string, options: ExportOptions = {}): Promise<Blob> {
    const { blob } = await this.http.requestBlob(
      'GET',
      `/entities/${segment(typeId)}/export`,
      {
        query: { attributes: options.attributes, query: options.query, entity_ids: options.entityIds },
        accept: 'text/csv',
      },
      requestPart(options),
    )
    return blob
  }

  /** Imports a CSV of entity values. Run it with `dry_run` first. */
  import(
    typeId: string,
    file: Blob,
    mapping: ImportMapping,
    options: RequestOptions & { filename?: string } = {},
  ): Promise<ImportReport> {
    const form = new FormData()
    form.append('file', file, options.filename ?? 'import.csv')
    form.append('mapping', JSON.stringify(mapping))
    return this.http.request<ImportReport>(
      'POST',
      `/entities/${segment(typeId)}/import`,
      { rawBody: form },
      requestPart(options),
    )
  }

  /**
   * Uploads a file and writes it as the entity's media value.
   *
   * A media value is a reference to stored bytes. The upload endpoint is the
   * only way to create one: `values.set` with fresh media metadata is refused,
   * because a caller could otherwise point at another tenant's object key.
   */
  uploadMedia(
    typeId: string,
    entityId: string,
    attributeId: string,
    file: Blob,
    options: RequestOptions & { filename?: string } = {},
  ): Promise<AttributeValue> {
    const form = new FormData()
    form.append('file', file, options.filename ?? 'upload')
    return this.http.request<AttributeValue>(
      'POST',
      `/entities/${segment(typeId)}/${segment(entityId)}/attributes/${segment(attributeId)}/media`,
      { rawBody: form },
      requestPart(options),
    )
  }

  /**
   * Mints a signed, expiring link to a stored object.
   *
   * Media bytes sit behind the same authentication as everything else, and the
   * token carries the tenant, so a PUBLIC surface — a storefront, a catalogue
   * page, an email — cannot use `mediaUrl`: it would have to proxy every
   * request through a service holding a tenant credential. The returned link
   * is fetched with no credential at all, because the signature is the
   * credential.
   *
   * Minting a link changes nothing, so this is a GET: a read-only credential
   * — including a cross-tenant reader — can mint one.
   *
   * `ttlSeconds` is how long it lasts. Absent or zero takes the service's short
   * default; anything above its maximum is capped rather than refused. The
   * caller must be able to read the object, and a deployment that sets no
   * signing secret answers FEATURE_DISABLED.
   */
  signMediaUrl(
    objectKey: string,
    options: RequestOptions & { ttlSeconds?: number } = {},
  ): Promise<SignedMediaUrl> {
    return this.http.request<SignedMediaUrl>(
      'GET',
      `/media/${segment(objectKey)}/signed-url`,
      { query: { ttl_seconds: options.ttlSeconds } },
      requestPart(options),
    )
  }

  /** The URL of a stored media object, for an `<img src>` or a download link. */
  mediaUrl(objectKey: string): string {
    return this.http.url(`/media/${segment(objectKey)}`)
  }

  /** Downloads a stored media object. */
  async downloadMedia(
    objectKey: string,
    options: RequestOptions = {},
  ): Promise<{ blob: Blob; contentType: string }> {
    return this.http.requestBlob(
      'GET',
      `/media/${segment(objectKey)}`,
      { accept: 'application/octet-stream' },
      options,
    )
  }
}
