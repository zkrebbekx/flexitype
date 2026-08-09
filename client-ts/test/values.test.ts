import { describe, expect, it } from 'vitest'
import { DATA_TYPES, type DataType, type UnitFamily } from '../src/models.js'
import {
  compareDecimalStrings,
  compareQuantities,
  compareValues,
  convertQuantity,
  formatDecimal,
  formatValue,
  parseQuantity,
  toWire,
  withBase,
} from '../src/softtype/values.js'

const weight: UnitFamily = { id: 'u1', name: 'weight', base_unit: 'g', units: { g: 1, kg: 1000, mg: 0.001 } }

describe('coercion, per data type', () => {
  it('covers every data type the service supports', () => {
    // The list is the DataType enum of api/openapi.yaml, which
    // domain/valueobjects/datatype.go defines. A new type added there without
    // an arm here fails this test.
    const covered: DataType[] = [
      'bool',
      'string',
      'enum',
      'url',
      'email',
      'integer',
      'float',
      'decimal',
      'date',
      'time',
      'datetime',
      'json',
      'media',
      'quantity',
    ]
    expect([...DATA_TYPES].sort()).toEqual([...covered].sort())
  })

  it('encodes a bool from a checkbox or from a form string', () => {
    expect(toWire('bool', true)).toBe(true)
    expect(toWire('bool', 'false')).toBe(false)
    expect(() => toWire('bool', 'yes')).toThrow(/expected a boolean/)
  })

  it('encodes the textual types as strings', () => {
    expect(toWire('string', 'ABC-1')).toBe('ABC-1')
    // Long-form text travels exactly as a string does: only the declared
    // type differs, and that difference is for the renderer.
    expect(toWire('text', 'A long description.\nWith a second line.')).toBe(
      'A long description.\nWith a second line.',
    )
    expect(toWire('enum', 'active')).toBe('active')
    expect(toWire('url', 'https://example.test/x')).toBe('https://example.test/x')
    expect(toWire('email', 'a@example.test')).toBe('a@example.test')
    expect(() => toWire('string', {})).toThrow(/as text/)
  })

  it('encodes an integer from a number or a form string, and refuses a fraction', () => {
    expect(toWire('integer', 42)).toBe(42)
    expect(toWire('integer', '42')).toBe(42)
    expect(toWire('integer', -7)).toBe(-7)
    expect(() => toWire('integer', 4.2)).toThrow(/expected an integer/)
    expect(() => toWire('integer', '4.2')).toThrow(/expected an integer/)
    // The service refuses a quoted value for an integer attribute, so the
    // client sends a number, never a string.
    expect(typeof toWire('integer', '42')).toBe('number')
  })

  it('refuses an integer JavaScript cannot hold exactly', () => {
    expect(() => toWire('integer', '9007199254740993')).toThrow(/outside the range/)
  })

  it('encodes a float and refuses a value JSON cannot carry', () => {
    expect(toWire('float', 1.5)).toBe(1.5)
    expect(toWire('float', '1.5')).toBe(1.5)
    expect(() => toWire('float', Number.NaN)).toThrow(/finite/)
    expect(() => toWire('float', Number.POSITIVE_INFINITY)).toThrow(/finite/)
  })

  it('keeps a decimal as a string end to end', () => {
    expect(toWire('decimal', '1234.56')).toBe('1234.56')
    expect(toWire('decimal', '-0.000000000000000001')).toBe('-0.000000000000000001')
    // 21 significant digits: a JS number would round this, so the string form
    // is the only one that survives.
    expect(toWire('decimal', '123456789012345678901')).toBe('123456789012345678901')
    expect(toWire('decimal', 10n)).toBe('10')
  })

  it('refuses a decimal passed as a number rather than rounding it quietly', () => {
    // 0.1 + 0.2 is 0.30000000000000004. Accepting a number here would write
    // that, and the caller would never see where the digits came from.
    expect(() => toWire('decimal', 0.1 + 0.2)).toThrow(/pass a decimal as a string/)
    expect(() => toWire('decimal', 19.99)).toThrow(/pass a decimal as a string/)
  })

  it('refuses a malformed or over-long decimal', () => {
    expect(() => toWire('decimal', '1,5')).toThrow(/expected a decimal/)
    expect(() => toWire('decimal', '1e10')).toThrow(/expected a decimal/)
    expect(() => toWire('decimal', '1'.repeat(41))).toThrow(/exceeds 40/)
  })

  it('encodes the temporal types in the formats the service parses', () => {
    expect(toWire('date', '2026-07-27')).toBe('2026-07-27')
    expect(toWire('date', new Date('2026-07-27T13:45:00Z'))).toBe('2026-07-27')
    expect(toWire('time', '09:22:45')).toBe('09:22:45')
    // <input type="time"> omits the seconds, which the service requires.
    expect(toWire('time', '09:22')).toBe('09:22:00')
    expect(toWire('datetime', '2026-07-27T09:22:45Z')).toBe('2026-07-27T09:22:45Z')
    // <input type="datetime-local"> carries no zone, so one is stated.
    expect(String(toWire('datetime', '2026-07-27T09:22'))).toMatch(/Z$/)
    expect(() => toWire('date', '27/07/2026')).toThrow(/expected a date/)
  })

  it('passes a JSON document through and refuses one it cannot serialize', () => {
    expect(toWire('json', { a: [1, 2] })).toEqual({ a: [1, 2] })
    const cyclic: Record<string, unknown> = {}
    cyclic.self = cyclic
    expect(() => toWire('json', cyclic)).toThrow(/not JSON-serializable/)
  })

  it('requires an object key on a media value', () => {
    expect(toWire('media', { object_key: 'k1', mime: 'image/png', size: 12 })).toEqual({
      object_key: 'k1',
      mime: 'image/png',
      size: 12,
    })
    expect(() => toWire('media', { mime: 'image/png' })).toThrow(/requires an object_key/)
  })

  it('encodes a quantity as magnitude and unit, and drops the base', () => {
    // The service resolves the unit against the attribute's family and
    // computes the base itself, so a client-supplied base could only disagree.
    expect(toWire('quantity', { magnitude: '2.5', unit: 'kg', base: 2500 })).toEqual({
      magnitude: '2.5',
      unit: 'kg',
    })
    expect(toWire('quantity', '10 kg')).toEqual({ magnitude: '10', unit: 'kg' })
    expect(() => toWire('quantity', { magnitude: 2.5, unit: 'kg' })).toThrow(/pass a decimal as a string/)
    expect(() => toWire('quantity', { magnitude: '2.5' })).toThrow(/requires a unit/)
  })

  it('reads a quantity written as text', () => {
    expect(parseQuantity('10 kg')).toEqual({ magnitude: '10', unit: 'kg' })
    expect(parseQuantity('-2.5mm')).toEqual({ magnitude: '-2.5', unit: 'mm' })
    expect(() => parseQuantity('kg')).toThrow(/expected a quantity/)
  })

  it('raises VALIDATION, the same code the service would answer with', () => {
    const error = (() => {
      try {
        toWire('integer', 'abc')
        return undefined
      } catch (e) {
        return e as { code?: string; status?: number }
      }
    })()
    expect(error?.code).toBe('VALIDATION')
    expect(error?.status).toBe(0)
  })
})

describe('quantities', () => {
  it('orders on the base magnitude, so grams and kilograms compare correctly', () => {
    const oneKilo = { magnitude: '1', unit: 'kg', base: 1000 }
    const fifteenHundredGrams = { magnitude: '1500', unit: 'g', base: 1500 }
    expect(compareQuantities(oneKilo, fifteenHundredGrams)).toBeLessThan(0)
    expect(compareValues('quantity', fifteenHundredGrams, oneKilo)).toBeGreaterThan(0)
  })

  it('falls back to the magnitudes when both carry the same unit', () => {
    expect(compareQuantities({ magnitude: '2', unit: 'kg' }, { magnitude: '10', unit: 'kg' })).toBeLessThan(0)
  })

  it('refuses to guess an order across units with no base', () => {
    expect(() => compareQuantities({ magnitude: '1', unit: 'kg' }, { magnitude: '1', unit: 'g' })).toThrow(
      /without a base magnitude/,
    )
  })

  it('computes a base from a unit family so a fresh value can be ordered', () => {
    expect(withBase({ magnitude: '2', unit: 'kg' }, weight).base).toBe(2000)
    expect(() => withBase({ magnitude: '2', unit: 'lb' }, weight)).toThrow(/not part of the weight family/)
  })

  it('converts between units of one family', () => {
    expect(convertQuantity({ magnitude: '2', unit: 'kg' }, 'g', weight)).toEqual({
      magnitude: '2000',
      unit: 'g',
      base: 2000,
    })
    expect(() => convertQuantity({ magnitude: '2', unit: 'kg' }, 'furlong', weight)).toThrow(/not part of the family/)
  })
})

describe('ordering', () => {
  it('orders decimals exactly, including two a float cannot tell apart', () => {
    expect(compareDecimalStrings('1.10', '1.9')).toBeLessThan(0)
    expect(compareDecimalStrings('10', '9')).toBeGreaterThan(0)
    expect(compareDecimalStrings('1.50', '1.5')).toBe(0)
    expect(compareDecimalStrings('-2', '-10')).toBeGreaterThan(0)
    expect(compareDecimalStrings('0.1', '-0.1')).toBeGreaterThan(0)
    expect(
      compareDecimalStrings('9007199254740993.00000001', '9007199254740993.00000002'),
    ).toBeLessThan(0)
  })

  it('sorts a column of decimals in numeric order, not in string order', () => {
    const column = ['9', '10', '1.5', '-2']
    column.sort((a, b) => compareValues('decimal', a, b))
    expect(column).toEqual(['-2', '1.5', '9', '10'])
  })

  it('orders the other types sensibly and puts an absent value first', () => {
    expect(compareValues('integer', 2, 10)).toBeLessThan(0)
    expect(compareValues('date', '2026-01-02', '2026-01-10')).toBeLessThan(0)
    expect(compareValues('datetime', '2026-01-02T00:00:00Z', '2026-01-01T00:00:00Z')).toBeGreaterThan(0)
    expect(compareValues('bool', false, true)).toBeLessThan(0)
    expect(compareValues('string', undefined, 'a')).toBeLessThan(0)
  })
})

describe('formatting', () => {
  it('returns the wire form when no locale is given', () => {
    expect(formatValue('decimal', '1234.56')).toBe('1234.56')
    expect(formatValue('date', '2026-07-27')).toBe('2026-07-27')
    expect(formatValue('bool', true)).toBe('true')
    expect(formatValue('integer', 1234)).toBe('1234')
  })

  it('groups a decimal as text, so its digits survive', () => {
    // Converting to a number for display is how a 21-digit identifier becomes
    // 1.2345678901234568e+20 on screen.
    expect(formatDecimal('123456789012345678901', 'en-US')).toBe('123,456,789,012,345,678,901')
    expect(formatDecimal('-1234.5678', 'en-US')).toBe('-1,234.5678')
    expect(formatDecimal('1234.5', 'de-DE')).toBe('1.234,5')
  })

  it('renders a quantity as its magnitude and unit', () => {
    expect(formatValue('quantity', { magnitude: '2.5', unit: 'kg', base: 2500 })).toBe('2.5 kg')
  })

  it('renders media by filename, falling back to the object key', () => {
    expect(formatValue('media', { object_key: 'k1', mime: 'image/png', size: 1, filename: 'logo.png' })).toBe(
      'logo.png',
    )
    expect(formatValue('media', { object_key: 'k1', mime: 'image/png', size: 1 })).toBe('k1')
  })

  it('renders an absent value as the caller asked', () => {
    expect(formatValue('string', undefined)).toBe('')
    expect(formatValue('string', null, { emptyText: '—' })).toBe('—')
  })

  it('uses the labels a caller supplies for a boolean', () => {
    expect(formatValue('bool', true, { boolLabels: ['No', 'Yes'] })).toBe('Yes')
    expect(formatValue('bool', false, { boolLabels: ['No', 'Yes'] })).toBe('No')
  })
})
