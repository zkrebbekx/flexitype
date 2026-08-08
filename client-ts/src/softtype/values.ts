/**
 * Value coercion and formatting for every soft data type.
 *
 * An application cannot know an entity's fields at compile time, so it holds
 * values as `unknown` and needs one place that knows how each data type is
 * written, read and displayed. That place is here.
 *
 * The wire forms follow `domain/valueobjects/value.go`:
 *
 * | data type | wire form |
 * |---|---|
 * | bool | a JSON boolean |
 * | string, enum, url, email | a JSON string |
 * | integer | a JSON number with no fraction |
 * | float | a JSON number |
 * | decimal | a JSON **string**, e.g. `"1234.56"` |
 * | date | `"YYYY-MM-DD"` |
 * | time | `"HH:MM:SS"` |
 * | datetime | RFC 3339, e.g. `"2026-07-27T09:22:45Z"` |
 * | json | any JSON document |
 * | media | `{ object_key, mime, size, checksum?, filename? }` |
 * | quantity | `{ magnitude, unit, base }` |
 */
import { FlexitypeError } from '../errors.js'
import type { Attribute, DataType, MediaValue, QuantityValue, UnitFamily } from '../models.js'

/** The canonical date format the API reads and writes. */
export const DATE_FORMAT = 'YYYY-MM-DD'
/** The canonical time-of-day format the API reads and writes. */
export const TIME_FORMAT = 'HH:MM:SS'

const DECIMAL_PATTERN = /^[+-]?\d+(\.\d+)?$/
const MAX_DECIMAL_LENGTH = 40
const INTEGER_PATTERN = /^[+-]?\d+$/
const DATE_PATTERN = /^\d{4}-\d{2}-\d{2}$/
const TIME_PATTERN = /^(\d{2}):(\d{2})(?::(\d{2}))?$/

function invalid(message: string, details?: Record<string, unknown>): FlexitypeError {
  return new FlexitypeError({ code: 'VALIDATION', message: `flexitype: ${message}`, status: 0, details })
}

/**
 * Encodes an application value into the JSON form the API expects for a data
 * type. It raises a VALIDATION FlexitypeError — the same code the service
 * would answer with — when the input cannot be that type.
 *
 * It accepts what an HTML form yields (strings) as well as the natural
 * JavaScript type, so a form field does not need its own parsing step.
 */
export function toWire(dataType: DataType, input: unknown): unknown {
  switch (dataType) {
    case 'bool':
      return toBool(input)
    case 'string':
    case 'enum':
    case 'url':
    case 'email':
      return toText(dataType, input)
    case 'integer':
      return toInteger(input)
    case 'float':
      return toFloat(input)
    case 'decimal':
      return toDecimal(input)
    case 'date':
      return toDate(input)
    case 'time':
      return toTime(input)
    case 'datetime':
      return toDateTime(input)
    case 'json':
      return toJson(input)
    case 'media':
      return toMedia(input)
    case 'quantity':
      return toQuantity(input)
    default:
      throw invalid(`unknown data type ${JSON.stringify(dataType)}`)
  }
}

function toBool(input: unknown): boolean {
  if (typeof input === 'boolean') return input
  if (input === 'true') return true
  if (input === 'false') return false
  throw invalid(`expected a boolean, got ${describe(input)}`)
}

function toText(dataType: DataType, input: unknown): string {
  if (typeof input === 'string') return input
  if (typeof input === 'number' || typeof input === 'boolean') return String(input)
  throw invalid(`expected a ${dataType} value as text, got ${describe(input)}`)
}

function toInteger(input: unknown): number {
  if (typeof input === 'bigint') {
    if (input > BigInt(Number.MAX_SAFE_INTEGER) || input < BigInt(Number.MIN_SAFE_INTEGER)) {
      throw invalid('the integer is outside the range JavaScript can represent exactly')
    }
    return Number(input)
  }
  if (typeof input === 'number') {
    if (!Number.isInteger(input)) throw invalid(`expected an integer, got ${input}`)
    if (!Number.isSafeInteger(input)) {
      throw invalid('the integer is outside the range JavaScript can represent exactly')
    }
    return input
  }
  if (typeof input === 'string') {
    const text = input.trim()
    if (!INTEGER_PATTERN.test(text)) throw invalid(`expected an integer, got ${JSON.stringify(input)}`)
    const parsed = Number(text)
    if (!Number.isSafeInteger(parsed)) {
      throw invalid('the integer is outside the range JavaScript can represent exactly')
    }
    return parsed
  }
  throw invalid(`expected an integer, got ${describe(input)}`)
}

function toFloat(input: unknown): number {
  const value =
    typeof input === 'number' ? input : typeof input === 'string' && input.trim() !== '' ? Number(input) : NaN
  if (!Number.isFinite(value)) throw invalid(`expected a finite number, got ${describe(input)}`)
  return value
}

/**
 * Encodes a decimal.
 *
 * A decimal stays a string from end to end. A JS number holds 15 to 17
 * significant digits, so a price or a measurement that passes through one can
 * come back changed — `0.1 + 0.2` is the familiar case, and a 20-digit
 * identifier is the silent one. This refuses a number rather than rounding it
 * quietly.
 */
function toDecimal(input: unknown): string {
  if (typeof input === 'bigint') return input.toString()
  if (typeof input === 'number') {
    throw invalid(
      'pass a decimal as a string, not a number: a JavaScript number cannot hold every decimal exactly ' +
        `(got ${input})`,
    )
  }
  if (typeof input !== 'string') throw invalid(`expected a decimal string, got ${describe(input)}`)
  const text = input.trim()
  if (text.length > MAX_DECIMAL_LENGTH) throw invalid(`the decimal exceeds ${MAX_DECIMAL_LENGTH} characters`)
  if (!DECIMAL_PATTERN.test(text)) throw invalid(`expected a decimal, got ${JSON.stringify(input)}`)
  return text
}

function toDate(input: unknown): string {
  if (input instanceof Date) {
    if (Number.isNaN(input.getTime())) throw invalid('expected a valid Date')
    return input.toISOString().slice(0, 10)
  }
  if (typeof input === 'string') {
    const text = input.trim()
    if (DATE_PATTERN.test(text)) return text
    throw invalid(`expected a date as ${DATE_FORMAT}, got ${JSON.stringify(input)}`)
  }
  throw invalid(`expected a date, got ${describe(input)}`)
}

function toTime(input: unknown): string {
  if (input instanceof Date) {
    if (Number.isNaN(input.getTime())) throw invalid('expected a valid Date')
    return input.toISOString().slice(11, 19)
  }
  if (typeof input === 'string') {
    const match = TIME_PATTERN.exec(input.trim())
    if (match) {
      // An <input type="time"> omits the seconds, which the API requires.
      return `${match[1]}:${match[2]}:${match[3] ?? '00'}`
    }
    throw invalid(`expected a time as ${TIME_FORMAT}, got ${JSON.stringify(input)}`)
  }
  throw invalid(`expected a time, got ${describe(input)}`)
}

function toDateTime(input: unknown): string {
  if (input instanceof Date) {
    if (Number.isNaN(input.getTime())) throw invalid('expected a valid Date')
    return input.toISOString()
  }
  if (typeof input === 'string') {
    const text = input.trim()
    const parsed = Date.parse(text)
    if (Number.isNaN(parsed)) throw invalid(`expected an RFC 3339 datetime, got ${JSON.stringify(input)}`)
    // An <input type="datetime-local"> yields "2026-07-27T09:22" with no zone.
    // Sending it unchanged makes the service read it as UTC by accident, so
    // this states the zone the browser meant.
    return /(?:Z|[+-]\d{2}:?\d{2})$/.test(text) ? text : new Date(parsed).toISOString()
  }
  throw invalid(`expected a datetime, got ${describe(input)}`)
}

function toJson(input: unknown): unknown {
  if (input === undefined) throw invalid('expected a JSON document, got undefined')
  try {
    JSON.stringify(input)
  } catch (cause) {
    throw new FlexitypeError({
      code: 'VALIDATION',
      message: 'flexitype: the value is not JSON-serializable',
      status: 0,
      cause,
    })
  }
  return input
}

function toMedia(input: unknown): MediaValue {
  if (typeof input !== 'object' || input === null) throw invalid(`expected media metadata, got ${describe(input)}`)
  const media = input as Partial<MediaValue>
  if (typeof media.object_key !== 'string' || media.object_key === '') {
    throw invalid('a media value requires an object_key; upload the file through entities.uploadMedia')
  }
  return {
    object_key: media.object_key,
    mime: media.mime ?? '',
    size: media.size ?? 0,
    ...(media.checksum === undefined ? {} : { checksum: media.checksum }),
    ...(media.filename === undefined ? {} : { filename: media.filename }),
  }
}

/**
 * Encodes a quantity.
 *
 * `base` is deliberately dropped: the service resolves the unit against the
 * attribute's unit family and computes the base magnitude itself, so a client
 * that sent its own would only be able to disagree with it.
 */
function toQuantity(input: unknown): { magnitude: string; unit: string } {
  if (typeof input === 'string') return toQuantity(parseQuantity(input))
  if (typeof input !== 'object' || input === null) throw invalid(`expected a quantity, got ${describe(input)}`)
  const quantity = input as Partial<QuantityValue>
  if (typeof quantity.unit !== 'string' || quantity.unit === '') throw invalid('a quantity requires a unit')
  return { magnitude: toDecimal(quantity.magnitude), unit: quantity.unit }
}

/** Reads a quantity written as text, e.g. `"10 kg"` or `"-2.5m"`. */
export function parseQuantity(text: string): QuantityValue {
  const match = /^\s*([+-]?\d+(?:\.\d+)?)\s*([^\s]+)\s*$/.exec(text)
  if (!match || match[1] === undefined || match[2] === undefined) {
    throw invalid(`expected a quantity as "<magnitude> <unit>", got ${JSON.stringify(text)}`)
  }
  return { magnitude: match[1], unit: match[2] }
}

/**
 * Decodes a wire value into the shape an application works with.
 *
 * Most types pass through: the wire form already is the natural one. The
 * temporal types gain a `Date` through `toDate*` helpers only when a caller
 * asks, because a `Date` cannot represent a date without a zone and a time
 * without a day, and converting eagerly is how a date becomes "the day
 * before" in a negative-offset zone.
 */
export function fromWire(dataType: DataType, wire: unknown): unknown {
  if (wire === null || wire === undefined) return undefined
  switch (dataType) {
    case 'quantity':
      return wire as QuantityValue
    case 'media':
      return wire as MediaValue
    default:
      return wire
  }
}

/** The options `formatValue` accepts. */
export interface FormatOptions {
  /** A BCP 47 locale. Without one the value is rendered in its wire form. */
  locale?: string | undefined
  /** The labels for a boolean. It defaults to `['false', 'true']`. */
  boolLabels?: readonly [string, string] | undefined
  /** What an absent value renders as. It defaults to an empty string. */
  emptyText?: string | undefined
}

/**
 * Renders a value for display.
 *
 * Without a `locale` it returns the wire form, which is stable and sortable.
 * With one it uses `Intl` for the numeric and temporal types. A decimal is
 * never converted to a number: it is grouped as text, so its digits survive.
 */
export function formatValue(
  attributeOrType: Attribute | DataType,
  wire: unknown,
  options: FormatOptions = {},
): string {
  const dataType = typeof attributeOrType === 'string' ? attributeOrType : attributeOrType.data_type
  if (wire === null || wire === undefined || dataType === undefined) return options.emptyText ?? ''

  switch (dataType) {
    case 'bool': {
      const labels = options.boolLabels ?? (['false', 'true'] as const)
      return wire === true ? labels[1] : labels[0]
    }
    case 'integer':
    case 'float':
      return options.locale === undefined ? String(wire) : new Intl.NumberFormat(options.locale).format(Number(wire))
    case 'decimal':
      return formatDecimal(String(wire), options.locale)
    case 'quantity': {
      const quantity = wire as QuantityValue
      const magnitude = formatDecimal(quantity.magnitude ?? '', options.locale)
      return `${magnitude} ${quantity.unit ?? ''}`.trim()
    }
    case 'date':
      return options.locale === undefined
        ? String(wire)
        : new Intl.DateTimeFormat(options.locale, { timeZone: 'UTC' }).format(new Date(`${String(wire)}T00:00:00Z`))
    case 'datetime':
      return options.locale === undefined
        ? String(wire)
        : new Intl.DateTimeFormat(options.locale, { dateStyle: 'medium', timeStyle: 'short' }).format(
            new Date(String(wire)),
          )
    case 'media': {
      const media = wire as MediaValue
      return media.filename ?? media.object_key ?? ''
    }
    case 'json':
      return JSON.stringify(wire)
    default:
      return String(wire)
  }
}

/**
 * Groups a decimal string for display without converting it to a number.
 *
 * `Intl.NumberFormat` is used only to discover the locale's group and decimal
 * separators; the digits are then regrouped as text, so a 30-digit decimal
 * keeps every digit.
 */
export function formatDecimal(value: string, locale?: string): string {
  if (value === '') return ''
  if (locale === undefined) return value
  const parts = new Intl.NumberFormat(locale).formatToParts(1234.5)
  const groupSeparator = parts.find((p) => p.type === 'group')?.value ?? ','
  const decimalSeparator = parts.find((p) => p.type === 'decimal')?.value ?? '.'

  const sign = value.startsWith('-') || value.startsWith('+') ? value[0] : ''
  const unsigned = sign === '' ? value : value.slice(1)
  const [whole = '', fraction] = unsigned.split('.')
  const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, groupSeparator)
  return `${sign === '-' ? '-' : ''}${grouped}${fraction === undefined ? '' : decimalSeparator + fraction}`
}

/**
 * Orders two wire values of one data type, for client-side sorting.
 *
 * A decimal is compared digit by digit rather than as a number, so two values
 * that a float cannot tell apart still order correctly. A quantity is compared
 * on `base`, the magnitude expressed in the family's base unit, so 1500 g
 * sorts above 1 kg. It returns a negative number, zero or a positive number,
 * as `Array.prototype.sort` expects.
 */
export function compareValues(dataType: DataType, a: unknown, b: unknown): number {
  if (a === undefined || a === null) return b === undefined || b === null ? 0 : -1
  if (b === undefined || b === null) return 1

  switch (dataType) {
    case 'integer':
    case 'float':
      return Number(a) - Number(b)
    case 'decimal':
      return compareDecimalStrings(String(a), String(b))
    case 'bool':
      return Number(Boolean(a)) - Number(Boolean(b))
    case 'quantity':
      return compareQuantities(a as QuantityValue, b as QuantityValue)
    case 'date':
    case 'time':
      // Both formats are fixed-width, so lexical order is chronological order.
      return String(a).localeCompare(String(b))
    case 'datetime':
      return Date.parse(String(a)) - Date.parse(String(b))
    default:
      return String(a).localeCompare(String(b))
  }
}

/** Orders two decimal strings exactly. */
export function compareDecimalStrings(a: string, b: string): number {
  const left = splitDecimal(a)
  const right = splitDecimal(b)
  if (left.negative !== right.negative) return left.negative ? -1 : 1
  const magnitude = compareUnsignedDecimal(left, right)
  return left.negative ? -magnitude : magnitude
}

interface SplitDecimal {
  negative: boolean
  whole: string
  fraction: string
}

function splitDecimal(value: string): SplitDecimal {
  const text = value.trim()
  const negative = text.startsWith('-')
  const unsigned = negative || text.startsWith('+') ? text.slice(1) : text
  const [whole = '0', fraction = ''] = unsigned.split('.')
  return { negative, whole: whole.replace(/^0+(?=\d)/, ''), fraction }
}

function compareUnsignedDecimal(a: SplitDecimal, b: SplitDecimal): number {
  if (a.whole.length !== b.whole.length) return a.whole.length < b.whole.length ? -1 : 1
  if (a.whole !== b.whole) return a.whole < b.whole ? -1 : 1
  const width = Math.max(a.fraction.length, b.fraction.length)
  const left = a.fraction.padEnd(width, '0')
  const right = b.fraction.padEnd(width, '0')
  if (left === right) return 0
  return left < right ? -1 : 1
}

/**
 * Orders two quantities on their base magnitude.
 *
 * A quantity read back from the API carries `base`. One built locally may not,
 * so a comparison falls back to the magnitudes when both carry the same unit,
 * and refuses to guess when they do not: 1 kg and 1 lb have no order without
 * the family's factors. Call `withBase` first to supply them.
 */
export function compareQuantities(a: QuantityValue, b: QuantityValue): number {
  if (typeof a.base === 'number' && typeof b.base === 'number') return a.base - b.base
  if (a.unit === b.unit) return compareDecimalStrings(a.magnitude ?? '0', b.magnitude ?? '0')
  throw invalid(
    `cannot order ${a.unit} against ${b.unit} without a base magnitude; ` +
      'read the values from the API, or apply withBase with the attribute\'s unit family',
  )
}

/**
 * Computes a quantity's base magnitude from a unit family, so a value built in
 * the browser can be compared before the service has seen it.
 */
export function withBase(quantity: QuantityValue, family: UnitFamily): QuantityValue {
  const factor = family.units?.[quantity.unit]
  if (typeof factor !== 'number') {
    throw invalid(`unit ${JSON.stringify(quantity.unit)} is not part of the ${family.name ?? 'unit'} family`, {
      unit: quantity.unit,
      family: family.name,
    })
  }
  return { ...quantity, base: Number(quantity.magnitude) * factor }
}

/**
 * Converts a quantity to another unit of the same family.
 *
 * The magnitude is returned as a string, so the result stays a decimal. It can
 * still lose digits: the conversion itself runs through a float factor, which
 * is what the service stores.
 */
export function convertQuantity(quantity: QuantityValue, toUnit: string, family: UnitFamily): QuantityValue {
  const from = family.units?.[quantity.unit]
  const to = family.units?.[toUnit]
  if (typeof from !== 'number') throw invalid(`unit ${JSON.stringify(quantity.unit)} is not part of the family`)
  if (typeof to !== 'number' || to === 0) throw invalid(`unit ${JSON.stringify(toUnit)} is not part of the family`)
  const base = Number(quantity.magnitude) * from
  return { magnitude: String(base / to), unit: toUnit, base }
}

function describe(input: unknown): string {
  if (input === null) return 'null'
  if (input === undefined) return 'undefined'
  if (Array.isArray(input)) return 'an array'
  return `a ${typeof input}`
}
