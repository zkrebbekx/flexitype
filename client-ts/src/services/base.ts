import type { QueryParams, RequestOptions, Transport } from '../http.js'
import type { ListOptions, Page } from '../pagination.js'

/** The shared state every service holds. */
export abstract class Service {
  constructor(protected readonly http: Transport) {}
}

/** Splits a list options object into its query part and its request part. */
export function pageQuery(options: ListOptions): QueryParams {
  return { limit: options.limit, cursor: options.cursor, total: options.total }
}

/** Keeps only the transport-level fields, so an options object can be passed on. */
export function requestPart(options: RequestOptions): RequestOptions {
  return { signal: options.signal, headers: options.headers }
}

/** The wire shape of a list endpoint that does not paginate. */
export interface ItemsResponse<T> {
  items?: T[]
}

/** Reads an `{"items":[...]}` body, tolerating an omitted array. */
export function itemsOf<T>(body: ItemsResponse<T>): T[] {
  return body.items ?? []
}

/** Normalizes a paged body, so `items` and `page_info` are always present. */
export function pageOf<T>(body: { items?: T[]; page_info?: Page<T>['page_info'] }): Page<T> {
  return { items: body.items ?? [], page_info: body.page_info ?? {} }
}
