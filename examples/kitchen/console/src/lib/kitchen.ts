/**
 * The kitchen API.
 *
 * Every derived number here — a cost per kilogram, a line cost, a dish's food
 * cost — is READ, never calculated. This console has no arithmetic in it
 * except the margin, and that one exception is explained in the README.
 */

/** A magnitude with the unit it was entered in. */
export interface Quantity {
  magnitude: string
  unit: string
}

/** What a supplier sells. */
export interface Ingredient {
  id: string
  name: string
  supplier?: string
  pack_size?: Quantity
  pack_price?: string
  /** Derived by the service: pack_price / pack_size, in the family's base unit. */
  cost_per_kg?: string
}

/** One ingredient in one dish. */
export interface Line {
  id: string
  ingredient_id: string
  ingredient?: string
  quantity?: Quantity
  /** Derived: the ingredient's cost per kilogram, reached through the link. */
  cost_per_kg?: string
  /** Derived: quantity times that cost. */
  line_cost?: string
}

/** What a guest orders. */
export interface Dish {
  id: string
  course?: string
  status?: string
  /** Keyed by locale. The base value is under "". */
  name: Record<string, string>
  description?: Record<string, string>
  /** Keyed by channel: one dish, three prices. */
  price: Record<string, string>
  allergens?: string[]
  contains_allergens?: boolean
  /** Derived: the total of this dish's lines. */
  food_cost?: string
  line_count: number
  /** Computed per channel by the API, because a formula cannot read a scoped value. */
  margin?: Record<string, string>
  lines?: Line[]
}

/** A staged set of price moves. */
export interface MenuChange {
  id: string
  name: string
  state: string
  publish_at?: string
}

export class KitchenError extends Error {
  readonly status: number
  /** The attributes a dish still needs, when publishing was refused. */
  readonly missing: string[]

  constructor(status: number, message: string, missing: string[] = []) {
    super(message)
    this.name = 'KitchenError'
    this.status = status
    this.missing = missing
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: { Accept: 'application/json', ...(init.headers ?? {}) },
  })
  const text = await response.text()
  const body: unknown = text === '' ? undefined : JSON.parse(text)
  if (!response.ok) {
    throw new KitchenError(response.status, messageOf(body) ?? `request failed with ${response.status}`, missingOf(body))
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

function missingOf(body: unknown): string[] {
  if (typeof body !== 'object' || body === null) return []
  const missing = (body as { missing?: unknown }).missing
  return Array.isArray(missing) ? missing.map(String) : []
}

function json(method: string, body: unknown): RequestInit {
  return { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }
}

export async function listIngredients(): Promise<Ingredient[]> {
  const body = await request<{ items?: Ingredient[] }>('/api/ingredients')
  return body.items ?? []
}

export async function putIngredient(id: string, input: Omit<Ingredient, 'id' | 'cost_per_kg'>): Promise<Ingredient> {
  return request<Ingredient>(`/api/ingredients/${encodeURIComponent(id)}`, json('PUT', input))
}

export async function listDishes(): Promise<Dish[]> {
  const body = await request<{ items?: Dish[] }>('/api/dishes')
  return body.items ?? []
}

export async function getDish(id: string): Promise<Dish> {
  return request<Dish>(`/api/dishes/${encodeURIComponent(id)}`)
}

/** Writes a dish. Derived fields are absent: the service owns them. */
export async function putDish(id: string, input: Partial<Dish>): Promise<Dish> {
  return request<Dish>(`/api/dishes/${encodeURIComponent(id)}`, json('PUT', input))
}

export async function putLine(
  dishID: string,
  lineID: string,
  input: { ingredient_id: string; quantity: Quantity },
): Promise<Dish> {
  return request<Dish>(
    `/api/dishes/${encodeURIComponent(dishID)}/lines/${encodeURIComponent(lineID)}`,
    json('PUT', input),
  )
}

export async function deleteLine(dishID: string, lineID: string): Promise<Dish> {
  return request<Dish>(
    `/api/dishes/${encodeURIComponent(dishID)}/lines/${encodeURIComponent(lineID)}`,
    { method: 'DELETE' },
  )
}

/** Puts a dish on the menu. Refused, with what is missing, when incomplete. */
export async function publishDish(id: string): Promise<Dish> {
  return request<Dish>(`/api/dishes/${encodeURIComponent(id)}/publish`, { method: 'POST' })
}

export async function scheduleMenuChange(input: {
  name: string
  publish_at?: string
  prices: Record<string, Record<string, string>>
}): Promise<MenuChange> {
  return request<MenuChange>('/api/menu-changes', json('POST', input))
}

export async function listMenuChanges(): Promise<MenuChange[]> {
  const body = await request<{ items?: MenuChange[] }>('/api/menu-changes')
  return body.items ?? []
}

/** The channels a dish is priced for, in the order a menu lists them. */
export const CHANNELS = ['dine_in', 'delivery', 'catering'] as const

/** The locales a dish is named in. "" is the base value. */
export const LOCALES = ['', 'fr'] as const

/** Renders a margin as a percentage. */
export function asPercent(ratio: string | undefined): string {
  if (ratio === undefined) return '—'
  const value = Number(ratio)
  if (!Number.isFinite(value)) return '—'
  return `${(value * 100).toFixed(1)}%`
}

/**
 * Renders money to two places, rounding half-up, without ever turning a
 * decimal string into a float.
 *
 * Rounding rather than truncating matters here: a cost derived by dividing by
 * a quantity carries the float tail of the unit conversion, so 5.39999…
 * truncates to 5.39 and reads as a penny less than it is.
 *
 * The arithmetic is done on the digits, because Number("12345678901234.99")
 * loses the last one.
 */
export function money(amount: string | undefined): string {
  if (amount === undefined || amount === '') return '—'
  const negative = amount.startsWith('-')
  const [whole = '0', fraction = ''] = amount.replace('-', '').split('.')
  const padded = (fraction + '000').slice(0, 3)
  const roundUp = Number(padded[2]) >= 5

  let digits = `${whole}${padded.slice(0, 2)}`.split('')
  if (roundUp) {
    let index = digits.length - 1
    for (;;) {
      const next = Number(digits[index]) + 1
      if (next < 10) {
        digits[index] = String(next)
        break
      }
      digits[index] = '0'
      index -= 1
      if (index < 0) {
        digits = ['1', ...digits]
        break
      }
    }
  }
  const text = digits.join('')
  const cents = text.slice(-2)
  const units = text.slice(0, -2).replace(/^0+(?=\d)/, '')
  return `${negative ? '-' : ''}${units === '' ? '0' : units}.${cents}`
}
