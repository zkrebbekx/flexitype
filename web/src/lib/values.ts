// Conversions between form input strings and the API's typed JSON values.

import type { DataType, TypedValue } from './api'

// inputKind picks the native control for a data type.
export function inputKind(
  dt: DataType,
): 'text' | 'number' | 'bool' | 'date' | 'time' | 'datetime' | 'json' | 'quantity' {
  switch (dt) {
    case 'bool':
      return 'bool'
    case 'integer':
    case 'float':
      return 'number'
    case 'date':
      return 'date'
    case 'time':
      return 'time'
    case 'datetime':
      return 'datetime'
    case 'json':
      return 'json'
    case 'quantity':
      // A magnitude + unit pair — ValueInput renders a dedicated editor.
      return 'quantity'
    default:
      return 'text'
  }
}

// A quantity value is a magnitude paired with a unit from the attribute's
// unit family. It travels through the string-based ValueInput model as JSON.
export interface Quantity {
  magnitude: string
  unit: string
}

// isQuantityValue narrows a stored value to the {magnitude, unit} shape.
export function isQuantityValue(v: unknown): v is Quantity {
  if (typeof v !== 'object' || v === null) return false
  const q = v as Record<string, unknown>
  return typeof q.magnitude === 'string' && typeof q.unit === 'string'
}

// formatQuantity renders a quantity as its display form, "{magnitude} {unit}".
export function formatQuantity(q: Quantity): string {
  return `${q.magnitude} ${q.unit}`.trim()
}

// parseQuantity reads the JSON string ValueInput carries back into its parts,
// tolerating an empty or malformed model (a freshly opened editor).
export function parseQuantity(raw: string): Quantity {
  try {
    const q = JSON.parse(raw) as Partial<Quantity>
    return {
      magnitude: typeof q.magnitude === 'string' ? q.magnitude : '',
      unit: typeof q.unit === 'string' ? q.unit : '',
    }
  } catch {
    return { magnitude: '', unit: '' }
  }
}

// toApiValue converts a raw input string into the JSON scalar the API
// expects for the data type. Throws with a human message on bad input.
export function toApiValue(dt: DataType, raw: string | boolean): unknown {
  if (dt === 'bool') return typeof raw === 'boolean' ? raw : raw === 'true'
  const s = String(raw).trim()
  if (s === '') throw new Error('a value is required')

  switch (dt) {
    case 'integer': {
      if (!/^[+-]?\d+$/.test(s)) throw new Error('must be a whole number')
      return Number(s)
    }
    case 'float': {
      const n = Number(s)
      if (Number.isNaN(n)) throw new Error('must be a number')
      return n
    }
    case 'decimal':
      if (!/^[+-]?\d+(\.\d+)?$/.test(s)) throw new Error('must be a decimal like 12.50')
      return s
    case 'quantity': {
      // The server stores the base-unit magnitude itself; the client sends
      // only {magnitude, unit}. The magnitude is kept as a string so exact
      // decimals survive the round-trip.
      const q = parseQuantity(s)
      if (q.magnitude.trim() === '') throw new Error('a magnitude is required')
      if (!/^[+-]?\d+(\.\d+)?$/.test(q.magnitude.trim())) throw new Error('magnitude must be a number like 2.5')
      if (q.unit.trim() === '') throw new Error('a unit is required')
      return { magnitude: q.magnitude.trim(), unit: q.unit.trim() }
    }
    case 'datetime': {
      // datetime-local yields "2026-07-11T14:30" — normalise to RFC 3339.
      const d = new Date(s)
      if (Number.isNaN(d.getTime())) throw new Error('must be a valid date and time')
      return d.toISOString()
    }
    case 'time':
      return s.length === 5 ? `${s}:00` : s
    case 'json': {
      try {
        return JSON.parse(s)
      } catch {
        throw new Error('must be valid JSON')
      }
    }
    default:
      return s
  }
}

// fromApiValue renders a stored value back into an editable input string.
export function fromApiValue(dt: DataType, value: unknown): string {
  if (value === null || value === undefined) return ''
  switch (dt) {
    case 'json':
      return JSON.stringify(value, null, 2)
    case 'quantity':
      // Carry the magnitude/unit pair as JSON so ValueInput can split it back
      // into its two controls.
      return isQuantityValue(value)
        ? JSON.stringify({ magnitude: value.magnitude, unit: value.unit })
        : ''
    case 'datetime': {
      const d = new Date(String(value))
      if (Number.isNaN(d.getTime())) return String(value)
      // datetime-local wants local "YYYY-MM-DDTHH:MM".
      const pad = (n: number) => String(n).padStart(2, '0')
      return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
    }
    case 'time':
      return String(value).slice(0, 5)
    default:
      return String(value)
  }
}

// typedValue wraps a converted value in the self-describing form used by
// constraint operands and dependency conditions.
export function typedValue(dt: DataType, raw: string | boolean): TypedValue {
  return { type: dt, value: toApiValue(dt, raw) }
}

// renderTyped shows a TypedValue compactly.
export function renderTyped(tv: TypedValue): string {
  return typeof tv.value === 'object' ? JSON.stringify(tv.value) : String(tv.value)
}
