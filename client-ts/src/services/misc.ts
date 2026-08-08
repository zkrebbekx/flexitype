import type { RequestOptions } from '../http.js'
import { segment } from '../http.js'
import type { ListOptions, Page } from '../pagination.js'
import { paginate } from '../pagination.js'
import type {
  ActivityEntry,
  CreateUnitFamily,
  Revision,
  RevisionDiff,
  SavedView,
  SavedViewInput,
  SavedViewPatch,
  ScanResult,
  UnitFamily,
} from '../models.js'
import { itemsOf, pageOf, pageQuery, requestPart, Service } from './base.js'

/** Saved-view operations. A saved view is a named query with its columns. */
export class SavedViewsService extends Service {
  /** Every saved view of the tenant. */
  async list(options: RequestOptions = {}): Promise<SavedView[]> {
    return itemsOf(await this.http.request<{ items?: SavedView[] }>('GET', '/saved-views', {}, options))
  }

  /** One saved view by id. */
  get(id: string, options: RequestOptions = {}): Promise<SavedView> {
    return this.http.request<SavedView>('GET', `/saved-views/${segment(id)}`, {}, options)
  }

  /** Saves a view. */
  create(input: SavedViewInput, options: RequestOptions = {}): Promise<SavedView> {
    return this.http.request<SavedView>('POST', '/saved-views', { body: input }, options)
  }

  /**
   * Updates a saved view. An omitted field keeps its stored value.
   *
   * Send the `version` you read to make the write a compare-and-swap: a view
   * someone else edited meanwhile answers 409 (CONFLICT) instead of discarding
   * their edit. Omit `version` for last-write-wins.
   */
  update(id: string, input: SavedViewPatch, options: RequestOptions = {}): Promise<SavedView> {
    return this.http.request<SavedView>('PATCH', `/saved-views/${segment(id)}`, { body: input }, options)
  }

  /** Deletes a saved view. */
  remove(id: string, options: RequestOptions = {}): Promise<void> {
    return this.http.request<void>('DELETE', `/saved-views/${segment(id)}`, {}, options)
  }
}

/** The filters `GET /activity` accepts. */
export interface ListActivityOptions extends ListOptions {
  /** The record kind, e.g. `attribute_value`. */
  entity?: string | undefined
  entityId?: string | undefined
  actor?: string | undefined
}

/** Audit-log operations. */
export class ActivityService extends Service {
  /** One page of the audit log, newest first. */
  async list(options: ListActivityOptions = {}): Promise<Page<ActivityEntry>> {
    return pageOf(
      await this.http.request<Page<ActivityEntry>>(
        'GET',
        '/activity',
        {
          query: {
            ...pageQuery(options),
            entity: options.entity,
            entity_id: options.entityId,
            actor: options.actor,
          },
        },
        requestPart(options),
      ),
    )
  }

  /** Every matching entry, following the cursor across pages. */
  listAll(options: ListActivityOptions = {}): AsyncGenerator<ActivityEntry, void, undefined> {
    return paginate((cursor) => this.list({ ...options, cursor }))
  }
}

/**
 * Unit-family operations.
 *
 * A unit family is a set of units that share a base unit, each with its factor
 * to that base. A quantity attribute names one, and every quantity value
 * normalizes to the base for comparison.
 */
export class UnitFamiliesService extends Service {
  /** Every unit family of the tenant. */
  async list(options: RequestOptions = {}): Promise<UnitFamily[]> {
    return itemsOf(await this.http.request<{ items?: UnitFamily[] }>('GET', '/unit-families', {}, options))
  }

  /** One unit family by id. */
  get(id: string, options: RequestOptions = {}): Promise<UnitFamily> {
    return this.http.request<UnitFamily>('GET', `/unit-families/${segment(id)}`, {}, options)
  }

  /** Creates a unit family. */
  create(input: CreateUnitFamily, options: RequestOptions = {}): Promise<UnitFamily> {
    return this.http.request<UnitFamily>('POST', '/unit-families', { body: input }, options)
  }

  /** Deletes a unit family. It is refused while an attribute still names it. */
  remove(id: string, options: RequestOptions = {}): Promise<void> {
    return this.http.request<void>('DELETE', `/unit-families/${segment(id)}`, {}, options)
  }
}

/** Duplicate-detection operations. Declare a rule through `types.createMatchRule`. */
export class MatchRulesService extends Service {
  /** Runs a rule and returns the candidate duplicate pairs it found. */
  scan(ruleId: string, options: RequestOptions = {}): Promise<ScanResult> {
    return this.http.request<ScanResult>('GET', `/match-rules/${segment(ruleId)}/scan`, {}, options)
  }

  /** Marks one candidate pair as not a duplicate, so a later scan omits it. */
  dismiss(ruleId: string, entityA: string, entityB: string, options: RequestOptions = {}): Promise<void> {
    return this.http.request<void>(
      'POST',
      `/match-rules/${segment(ruleId)}/dismiss`,
      { body: { entity_a: entityA, entity_b: entityB } },
      options,
    )
  }

  /** Deletes a rule. */
  remove(ruleId: string, options: RequestOptions = {}): Promise<void> {
    return this.http.request<void>('DELETE', `/match-rules/${segment(ruleId)}`, {}, options)
  }
}

/** Entity-revision operations. List and create them through `entities`. */
export class RevisionsService extends Service {
  /** One revision by id. */
  get(id: string, options: RequestOptions = {}): Promise<Revision> {
    return this.http.request<Revision>('GET', `/revisions/${segment(id)}`, {}, options)
  }

  /** The changes from this revision to another. */
  diff(id: string, toRevisionId: string, options: RequestOptions = {}): Promise<RevisionDiff> {
    return this.http.request<RevisionDiff>(
      'GET',
      `/revisions/${segment(id)}/diff`,
      { query: { to: toRevisionId } },
      options,
    )
  }

  /** Restores the entity to this revision. It writes a new revision. */
  restore(id: string, options: RequestOptions = {}): Promise<Revision> {
    return this.http.request<Revision>('POST', `/revisions/${segment(id)}/restore`, {}, options)
  }
}
