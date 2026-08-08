/**
 * The shopper API.
 *
 * It answers from the storefront's OWN catalog — a denormalized projection fed
 * by flexitype's webhooks — not from flexitype. That is what makes one search
 * across every merchant possible: flexitype takes the tenant from the token,
 * so "every merchant's active products, ranked by relevance" is not
 * expressible against it at all.
 *
 * Nothing here holds a credential. The shopper API is public, and the image
 * endpoint proxies the bytes with the merchant's token on the server side.
 */

/** One product row of the projection. */
export interface Product {
  tenant: string
  merchant: string
  entity_id: string
  subtype: string
  name: string
  description: string
  sku: string
  status: string
  price?: string
  currency: string
  in_stock?: boolean
  image?: unknown
  /**
   * Everything the merchant added to its own subtype, keyed by internal name.
   * The shop renders these without knowing any of them: that is the whole
   * point of one storefront over heterogeneous schemas.
   */
  attributes: Record<string, unknown>
  updated_at: string
}

/** One merchant a shopper can filter by. */
export interface Merchant {
  tenant: string
  display_name: string
}

/** The filters the product search takes. */
export interface SearchFilters {
  q?: string
  merchant?: string
  minPrice?: string
  maxPrice?: string
  limit?: number
  offset?: number
}

export class StorefrontError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'StorefrontError'
    this.status = status
  }
}

async function get<T>(path: string): Promise<T> {
  const response = await fetch(path, { headers: { Accept: 'application/json' } })
  const text = await response.text()
  const body: unknown = text === '' ? undefined : JSON.parse(text)
  if (!response.ok) {
    throw new StorefrontError(response.status, messageOf(body) ?? `request failed with ${response.status}`)
  }
  return body as T
}

function messageOf(body: unknown): string | undefined {
  if (typeof body !== 'object' || body === null) return undefined
  const error = (body as { error?: unknown }).error
  if (typeof error !== 'object' || error === null) return undefined
  const message = (error as { message?: unknown }).message
  return typeof message === 'string' ? message : undefined
}

/** Builds the query string, leaving an empty filter out entirely. */
export function searchPath(filters: SearchFilters): string {
  const params = new URLSearchParams()
  if (filters.q !== undefined && filters.q !== '') params.set('q', filters.q)
  if (filters.merchant !== undefined && filters.merchant !== '') params.set('merchant', filters.merchant)
  if (filters.minPrice !== undefined && filters.minPrice !== '') params.set('min_price', filters.minPrice)
  if (filters.maxPrice !== undefined && filters.maxPrice !== '') params.set('max_price', filters.maxPrice)
  if (filters.limit !== undefined) params.set('limit', String(filters.limit))
  if (filters.offset !== undefined && filters.offset > 0) params.set('offset', String(filters.offset))
  const query = params.toString()
  return query === '' ? '/api/products' : `/api/products?${query}`
}

/** Searches the catalog. Only active products are ever returned. */
export async function searchProducts(filters: SearchFilters): Promise<Product[]> {
  const body = await get<{ items?: Product[] }>(searchPath(filters))
  return body.items ?? []
}

/** One product. A draft or an archived one is a 404, not a hidden field. */
export async function getProduct(tenant: string, entityId: string): Promise<Product> {
  return get<Product>(`/api/products/${encodeURIComponent(tenant)}/${encodeURIComponent(entityId)}`)
}

/** Every merchant with something on offer. */
export async function listMerchants(): Promise<Merchant[]> {
  const body = await get<{ items?: Merchant[] }>('/api/merchants')
  return body.items ?? []
}

/** The URL of a product photo. The storefront proxies the bytes. */
export function imageUrl(product: Pick<Product, 'tenant' | 'entity_id'>): string {
  return `/api/products/${encodeURIComponent(product.tenant)}/${encodeURIComponent(product.entity_id)}/image`
}

/** Renders a price with its currency, or an empty string when it has none. */
export function formatPrice(product: Pick<Product, 'price' | 'currency'>): string {
  if (product.price === undefined || product.price === '') return ''
  // The price is a DECIMAL STRING. It is never parsed into a float: at four
  // significant figures that is harmless, and at fourteen it is a wrong price.
  return product.currency === '' ? product.price : `${product.price} ${product.currency}`
}
