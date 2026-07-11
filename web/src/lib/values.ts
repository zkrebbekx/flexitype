// Conversions between form input strings and the API's typed JSON values.

import type { DataType, TypedValue } from './api'

// inputKind picks the native control for a data type.
export function inputKind(dt: DataType): 'text' | 'number' | 'bool' | 'date' | 'time' | 'datetime' | 'json' {
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
    default:
      return 'text'
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
