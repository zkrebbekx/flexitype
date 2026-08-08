/**
 * The platform API — the merchant-facing backend of this example.
 *
 * The console reads through the flexitype SDK (see `merchantClient.ts`) and
 * WRITES here. That split is deliberate:
 *
 *   - A read is a plain flexitype read, so the SDK's services, hooks and
 *     soft-typing helpers work unchanged against the passthrough.
 *   - A write goes through the platform, which batches a whole product into
 *     ONE atomic call. Writing value by value would let the storefront project
 *     a half-written product.
 *
 * No credential is held here. The browser sends no token at all: nginx adds
 * the console credential to every proxied request, so it never reaches
 * JavaScript.
 */

/** One merchant, as the platform reports it. The token is never in this shape. */
export interface Merchant {
  id: string
  display_name: string
  tenant: string
  created_at: string
}

/** The failure shape every platform endpoint returns. */
export class PlatformError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'PlatformError'
    this.status = status
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: { Accept: 'application/json', ...(init.headers ?? {}) },
  })
  if (response.status === 204) return undefined as T
  const text = await response.text()
  const body: unknown = text === '' ? undefined : JSON.parse(text)
  if (!response.ok) {
    throw new PlatformError(response.status, messageOf(body) ?? `request failed with ${response.status}`)
  }
  return body as T
}

/** Reads the platform's `{"error":{"message":…}}` shape. */
function messageOf(body: unknown): string | undefined {
  if (typeof body !== 'object' || body === null) return undefined
  const error = (body as { error?: unknown }).error
  if (typeof error !== 'object' || error === null) return undefined
  const message = (error as { message?: unknown }).message
  return typeof message === 'string' ? message : undefined
}

function json(body: unknown): RequestInit {
  return {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }
}

/** Every onboarded merchant. */
export async function listMerchants(): Promise<Merchant[]> {
  const body = await request<{ items?: Merchant[] }>('/api/merchants')
  return body.items ?? []
}

/** The input onboarding takes. The tenant defaults to the id. */
export interface OnboardInput {
  id: string
  display_name: string
  tenant?: string
}

/**
 * Onboards a merchant: a flexitype tenant, a service account scoped to it, the
 * `ecommerce` starter schema, a webhook subscription and the first backfill.
 * It is idempotent, so a second click is safe.
 */
export async function onboardMerchant(input: OnboardInput): Promise<Merchant> {
  return request<Merchant>('/api/merchants', json(input))
}

/** One attribute to create alongside a subtype. */
export interface AttributeInput {
  internal_name: string
  display_name: string
  data_type: string
  required?: boolean
  multi_valued?: boolean
  unique?: boolean
  localizable?: boolean
  help_text?: string
}

/** The input a subtype takes. `extends` defaults to the root `product` type. */
export interface SubtypeInput {
  internal_name: string
  display_name: string
  extends?: string
  attributes?: AttributeInput[]
}

/** Creates a subtype of the merchant's product type, with its own fields. */
export async function createSubtype(merchantId: string, input: SubtypeInput): Promise<{ id: string }> {
  return request<{ id: string }>(`/api/merchants/${encodeURIComponent(merchantId)}/types`, json(input))
}

/** Adds one attribute to an existing type. */
export async function createAttribute(
  merchantId: string,
  typeId: string,
  input: AttributeInput,
): Promise<void> {
  await request<void>(
    `/api/merchants/${encodeURIComponent(merchantId)}/types/${encodeURIComponent(typeId)}/attributes`,
    json(input),
  )
}

/** The values of one product, keyed by attribute internal name. */
export interface ProductValues {
  entity_id: string
  type: string
  values: Record<string, unknown>
}

/**
 * Writes a whole product in one atomic batch. Either every value lands and its
 * events fire, or none does.
 */
export async function putProduct(
  merchantId: string,
  entityId: string,
  input: { type: string; values: Record<string, unknown> },
): Promise<{ written: number }> {
  return request<{ written: number }>(
    `/api/merchants/${encodeURIComponent(merchantId)}/products/${encodeURIComponent(entityId)}`,
    { ...json(input), method: 'PUT' },
  )
}

/** Archives a product. The storefront drops it on the next event. */
export async function deleteProduct(merchantId: string, entityId: string, type: string): Promise<void> {
  await request<void>(
    `/api/merchants/${encodeURIComponent(merchantId)}/products/${encodeURIComponent(entityId)}` +
      `?type=${encodeURIComponent(type)}`,
    { method: 'DELETE' },
  )
}

/** Uploads a product image. The bytes stream through the platform to flexitype. */
export async function uploadImage(
  merchantId: string,
  entityId: string,
  type: string,
  file: File,
  attribute = 'image',
): Promise<unknown> {
  const form = new FormData()
  form.append('file', file)
  return request<unknown>(
    `/api/merchants/${encodeURIComponent(merchantId)}/products/${encodeURIComponent(entityId)}/image` +
      `?type=${encodeURIComponent(type)}&attribute=${encodeURIComponent(attribute)}`,
    { method: 'POST', body: form },
  )
}
