import { describe, expect, it } from 'vitest'
import { fromApiValue, inputKind, toApiValue } from './values'

describe('toApiValue', () => {
  it('converts each data type to its API scalar', () => {
    expect(toApiValue('bool', true)).toBe(true)
    expect(toApiValue('bool', 'true')).toBe(true)
    expect(toApiValue('integer', '42')).toBe(42)
    expect(toApiValue('float', '3.14')).toBe(3.14)
    expect(toApiValue('decimal', '12.50')).toBe('12.50')
    expect(toApiValue('string', 'hello')).toBe('hello')
    expect(toApiValue('time', '14:30')).toBe('14:30:00')
    expect(toApiValue('json', '{"a": 1}')).toEqual({ a: 1 })
  })

  it('rejects malformed input with a human message', () => {
    expect(() => toApiValue('integer', '3.5')).toThrow(/whole number/)
    expect(() => toApiValue('decimal', '12.3.4')).toThrow(/decimal/)
    expect(() => toApiValue('json', 'not json')).toThrow(/JSON/)
    expect(() => toApiValue('string', '  ')).toThrow(/required/)
  })

  it('normalises datetime-local input to RFC 3339', () => {
    const iso = toApiValue('datetime', '2026-07-11T14:30') as string
    expect(new Date(iso).toISOString()).toBe(iso)
  })
})

describe('fromApiValue', () => {
  it('round-trips values back into editable strings', () => {
    expect(fromApiValue('string', 'x')).toBe('x')
    expect(fromApiValue('time', '14:30:00')).toBe('14:30')
    expect(fromApiValue('json', { a: 1 })).toBe('{\n  "a": 1\n}')
    expect(fromApiValue('integer', null)).toBe('')
  })
})

describe('inputKind', () => {
  it('picks the right control per data type', () => {
    expect(inputKind('bool')).toBe('bool')
    expect(inputKind('integer')).toBe('number')
    expect(inputKind('datetime')).toBe('datetime')
    expect(inputKind('json')).toBe('json')
    expect(inputKind('enum')).toBe('text')
  })
})
