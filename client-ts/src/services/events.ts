import type { RequestOptions } from '../http.js'
import { segment } from '../http.js'
import type { ListOptions, Page } from '../pagination.js'
import type {
  ChangeSet,
  CreateChangeSet,
  CreateSubscription,
  Delivery,
  DeliveryStatus,
  EventCursor,
  FeedEvent,
  Mutation,
  Subscription,
  UpdateSubscription,
} from '../models.js'
import { itemsOf, pageOf, pageQuery, requestPart, Service } from './base.js'

/** Webhook subscription and delivery operations. */
export class WebhooksService extends Service {
  /** Every subscription of the tenant. Secrets are never returned. */
  async list(options: RequestOptions = {}): Promise<Subscription[]> {
    return itemsOf(
      await this.http.request<{ items?: Subscription[] }>('GET', '/webhook-subscriptions', {}, options),
    )
  }

  /** One subscription by id. */
  get(id: string, options: RequestOptions = {}): Promise<Subscription> {
    return this.http.request<Subscription>('GET', `/webhook-subscriptions/${segment(id)}`, {}, options)
  }

  /** Subscribes an endpoint. An empty `event_types` means every event. */
  create(input: CreateSubscription, options: RequestOptions = {}): Promise<Subscription> {
    return this.http.request<Subscription>('POST', '/webhook-subscriptions', { body: input }, options)
  }

  /** Updates a subscription. `rotate_secret` replaces the signing secret. */
  update(id: string, input: UpdateSubscription, options: RequestOptions = {}): Promise<Subscription> {
    return this.http.request<Subscription>(
      'PATCH',
      `/webhook-subscriptions/${segment(id)}`,
      { body: input },
      options,
    )
  }

  /** Deletes a subscription. */
  remove(id: string, options: RequestOptions = {}): Promise<void> {
    return this.http.request<void>('DELETE', `/webhook-subscriptions/${segment(id)}`, {}, options)
  }

  /** One page of a subscription's delivery attempts. */
  async deliveries(
    id: string,
    options: ListOptions & { status?: DeliveryStatus | undefined } = {},
  ): Promise<Page<Delivery>> {
    return pageOf(
      await this.http.request<Page<Delivery>>(
        'GET',
        `/webhook-subscriptions/${segment(id)}/deliveries`,
        { query: { ...pageQuery(options), status: options.status } },
        requestPart(options),
      ),
    )
  }

  /** Requeues one dead or delivered attempt. */
  redeliver(deliveryId: string, options: RequestOptions = {}): Promise<Delivery> {
    return this.http.request<Delivery>(
      'POST',
      `/webhook-deliveries/${segment(deliveryId)}/redeliver`,
      {},
      options,
    )
  }

  /**
   * Requeues every dead delivery, optionally for one subscription, and reports
   * how many returned to pending. It needs the admin scope.
   */
  async redeliverDead(
    options: RequestOptions & { subscriptionId?: string | undefined } = {},
  ): Promise<number> {
    const body = await this.http.request<{ redelivered?: number }>(
      'POST',
      '/webhook-deliveries/redeliver-dead',
      { query: { subscription_id: options.subscriptionId } },
      requestPart(options),
    )
    return body.redelivered ?? 0
  }
}

/** The filters `GET /events` accepts. */
export interface ListEventsOptions extends RequestOptions {
  /** The feed position to read after. It is a `feed_seq`, not an opaque cursor. */
  after?: number | undefined
  /** Only these event types. */
  types?: string[] | undefined
  limit?: number | undefined
}

/**
 * Event-feed operations.
 *
 * The feed is ordered by `feed_seq` and is paged by that number, not by an
 * opaque cursor. A consumer commits its position through `commitCursor`, which
 * is a compare-and-swap: a position another worker moved first answers
 * CURSOR_CONFLICT, and a position older than the retention answers
 * CURSOR_EXPIRED.
 */
export class EventsService extends Service {
  /** One page of the feed. */
  async list(options: ListEventsOptions = {}): Promise<FeedEvent[]> {
    return itemsOf(
      await this.http.request<{ items?: FeedEvent[] }>(
        'GET',
        '/events',
        { query: { after: options.after, types: options.types, limit: options.limit } },
        requestPart(options),
      ),
    )
  }

  /** A named consumer's committed position. */
  getCursor(consumer: string, options: RequestOptions = {}): Promise<EventCursor> {
    return this.http.request<EventCursor>('GET', `/event-cursors/${segment(consumer)}`, {}, options)
  }

  /**
   * Commits a consumer's position. `expected` is the position the caller last
   * read; a mismatch answers CURSOR_CONFLICT rather than overwriting the other
   * worker's progress.
   */
  commitCursor(
    consumer: string,
    position: number,
    expected: number,
    options: RequestOptions = {},
  ): Promise<EventCursor> {
    return this.http.request<EventCursor>(
      'PUT',
      `/event-cursors/${segment(consumer)}`,
      { body: { position, expected } },
      options,
    )
  }

  /** The URL of the server-sent-events tail, for an `EventSource`. */
  streamUrl(): string {
    return this.http.url('/events/stream')
  }
}

/**
 * Change-set operations.
 *
 * A change-set stages value mutations and moves through review to publish:
 * draft, in_review, approved, publishing, published or rejected. Preview one
 * with `entities.values(type, entity, { changeset })`.
 */
export class ChangeSetsService extends Service {
  /** Every change-set of the tenant. */
  async list(options: RequestOptions = {}): Promise<ChangeSet[]> {
    return itemsOf(await this.http.request<{ items?: ChangeSet[] }>('GET', '/changesets', {}, options))
  }

  /** One change-set by id. */
  get(id: string, options: RequestOptions = {}): Promise<ChangeSet> {
    return this.http.request<ChangeSet>('GET', `/changesets/${segment(id)}`, {}, options)
  }

  /** Opens a change-set. */
  create(input: CreateChangeSet, options: RequestOptions = {}): Promise<ChangeSet> {
    return this.http.request<ChangeSet>('POST', '/changesets', { body: input }, options)
  }

  /** Stages one value mutation on a draft change-set. */
  addMutation(id: string, mutation: Mutation, options: RequestOptions = {}): Promise<ChangeSet> {
    return this.http.request<ChangeSet>('POST', `/changesets/${segment(id)}/mutations`, { body: mutation }, options)
  }

  /** Submits a draft for review. */
  submit(id: string, options: RequestOptions = {}): Promise<ChangeSet> {
    return this.http.request<ChangeSet>('POST', `/changesets/${segment(id)}/submit`, {}, options)
  }

  /** Approves a submitted change-set. The approver must not be the author. */
  approve(id: string, options: RequestOptions = {}): Promise<ChangeSet> {
    return this.http.request<ChangeSet>('POST', `/changesets/${segment(id)}/approve`, {}, options)
  }

  /** Rejects a submitted change-set. */
  reject(id: string, options: RequestOptions = {}): Promise<ChangeSet> {
    return this.http.request<ChangeSet>('POST', `/changesets/${segment(id)}/reject`, {}, options)
  }

  /** Applies an approved change-set's mutations. */
  publish(id: string, options: RequestOptions = {}): Promise<ChangeSet> {
    return this.http.request<ChangeSet>('POST', `/changesets/${segment(id)}/publish`, {}, options)
  }
}
