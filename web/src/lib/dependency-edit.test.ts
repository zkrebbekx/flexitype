import { describe, expect, it } from 'vitest'
import type { Condition, Constraint, Effect } from './api'
import {
  buildCondition,
  buildEffect,
  conditionFromApi,
  conditionSubjectType,
  editableConstraintKinds,
  effectFormFromApi,
} from './dependency-edit'

// Load an API object into the form and build it straight back, the way a
// description-only edit does. Every accepted field must survive.
function roundTripCondition(c: Condition, sourceType: Parameters<typeof buildCondition>[1] = 'string'): Condition {
  return buildCondition(conditionFromApi(c), sourceType)
}

describe('condition round-trip (#494)', () => {
  it('keeps context_key and context_type on an untouched equals condition', () => {
    const c: Condition = {
      kind: 'equals',
      context_key: 'tier',
      context_type: 'string',
      value: { type: 'string', value: 'gold' },
    }
    expect(roundTripCondition(c)).toEqual(c)
  })

  it('types the operands with context_type, not the source type', () => {
    const c: Condition = {
      kind: 'equals',
      context_key: 'age',
      context_type: 'integer',
      value: { type: 'integer', value: 18 },
    }
    // The source attribute is a string; the context fact is an integer.
    expect(roundTripCondition(c, 'string')).toEqual(c)
  })

  it('keeps context fields on in / range / pattern conditions', () => {
    const inCond: Condition = {
      kind: 'in',
      context_key: 'region',
      context_type: 'string',
      values: [
        { type: 'string', value: 'eu' },
        { type: 'string', value: 'us' },
      ],
    }
    expect(roundTripCondition(inCond)).toEqual(inCond)

    const range: Condition = {
      kind: 'range',
      context_key: 'age',
      context_type: 'integer',
      min: { type: 'integer', value: 18 },
      min_exclusive: true,
    }
    expect(roundTripCondition(range)).toEqual(range)

    const pattern: Condition = {
      kind: 'pattern',
      context_key: 'sku',
      context_type: 'string',
      pattern: '^[A-Z]{2}',
      pattern_substring: true,
    }
    expect(roundTripCondition(pattern)).toEqual(pattern)
  })

  it('rejects a context condition without a fact key', () => {
    const row = conditionFromApi({ kind: 'equals', value: { type: 'string', value: 'x' } })
    row.useContext = true
    expect(() => buildCondition(row, 'string')).toThrow(/fact key/)
  })

  it('resolves the subject type from the row', () => {
    const plain = conditionFromApi({ kind: 'equals', value: { type: 'integer', value: 1 } })
    expect(conditionSubjectType(plain, 'integer')).toBe('integer')
    const ctx = conditionFromApi({
      kind: 'equals',
      context_key: 'tier',
      context_type: 'enum',
      value: { type: 'enum', value: 'gold' },
    })
    expect(conditionSubjectType(ctx, 'integer')).toBe('enum')
  })

  it('passes an unrecognized condition field through unchanged', () => {
    const c = {
      kind: 'equals',
      value: { type: 'string', value: 'x' },
      some_future_field: { nested: true },
    } as unknown as Condition
    const rebuilt = roundTripCondition(c) as unknown as Record<string, unknown>
    expect(rebuilt.some_future_field).toEqual({ nested: true })
  })
})

describe('effect round-trip (#495)', () => {
  it('keeps a one_of constraint on an untouched effect', () => {
    const effect: Effect = {
      constraints: [
        {
          kind: 'one_of',
          values: [
            { type: 'string', value: 'a' },
            { type: 'string', value: 'b' },
          ],
        },
      ],
    }
    const { form, passthrough } = effectFormFromApi(effect, 'string')
    expect(form.oneOf).toEqual(['a', 'b'])
    expect(buildEffect(form, 'string', passthrough).constraints).toEqual(effect.constraints)
  })

  it('keeps substring on a pattern constraint instead of anchoring it', () => {
    const effect: Effect = {
      constraints: [{ kind: 'pattern', expr: 'beta', substring: true }],
    }
    const { form, passthrough } = effectFormFromApi(effect, 'string')
    expect(form.patternSubstring).toBe(true)
    expect(buildEffect(form, 'string', passthrough).constraints).toEqual([
      { kind: 'pattern', expr: 'beta', substring: true },
    ])
  })

  it('round-trips a full effect unchanged when nothing is edited', () => {
    const effect: Effect = {
      allowed_values: [{ type: 'string', value: 'a' }],
      required: true,
      constraints: [
        { kind: 'min_length', n: 2 },
        { kind: 'max_length', n: 8 },
        { kind: 'pattern', expr: '^[a-z]+$' },
        { kind: 'one_of', values: [{ type: 'string', value: 'abc' }] },
      ],
    }
    const { form, passthrough } = effectFormFromApi(effect, 'string')
    expect(buildEffect(form, 'string', passthrough)).toEqual(effect)
  })

  it('passes a constraint the editor cannot edit for the target through unchanged', () => {
    // A min_value constraint on a string target has no editor field; it must
    // survive the save untouched.
    const stray: Constraint = { kind: 'min_value', value: { type: 'integer', value: 3 } }
    const effect: Effect = { constraints: [stray] }
    const { form, passthrough } = effectFormFromApi(effect, 'string')
    expect(passthrough.constraints).toEqual([stray])
    expect(buildEffect(form, 'string', passthrough).constraints).toEqual([stray])
  })

  it('passes unknown fields inside a known constraint through unchanged', () => {
    const effect = {
      constraints: [{ kind: 'pattern', expr: 'x', future_flag: 7 }],
    } as unknown as Effect
    const { form, passthrough } = effectFormFromApi(effect, 'string')
    const [rebuilt] = buildEffect(form, 'string', passthrough).constraints as unknown as Record<string, unknown>[]
    expect(rebuilt.future_flag).toBe(7)
    expect(rebuilt.expr).toBe('x')
  })

  it('passes unknown effect-level fields through unchanged', () => {
    const effect = { required: false, future_effect_field: 'keep-me' } as unknown as Effect
    const { form, passthrough } = effectFormFromApi(effect, 'string')
    const rebuilt = buildEffect(form, 'string', passthrough) as unknown as Record<string, unknown>
    expect(rebuilt.future_effect_field).toBe('keep-me')
    expect(rebuilt.required).toBe(false)
  })

  it('still lets an edit remove a constraint the editor shows', () => {
    const effect: Effect = { constraints: [{ kind: 'min_length', n: 2 }] }
    const { form, passthrough } = effectFormFromApi(effect, 'string')
    form.minLength = ''
    expect(buildEffect(form, 'string', passthrough).constraints).toBeUndefined()
  })

  it('offers one_of for every target type and gates the rest by capability', () => {
    expect(editableConstraintKinds('bool').has('one_of')).toBe(true)
    expect(editableConstraintKinds('bool').has('pattern')).toBe(false)
    expect(editableConstraintKinds('string').has('pattern')).toBe(true)
    expect(editableConstraintKinds('integer').has('min_value')).toBe(true)
    expect(editableConstraintKinds('media').has('media')).toBe(true)
  })
})

describe('range bounds with a blank input (#508)', () => {
  it('sets only the bound that was filled in', () => {
    // 'between' is the default comparator, so an author who fills one bound
    // leaves the other blank.
    const row = conditionFromApi({ kind: 'range', min: { type: 'integer', value: 5 } })
    const c = buildCondition(row, 'integer')
    expect(c.min).toBeDefined()
    expect(c.max).toBeUndefined()
  })

  it('reports a range with neither bound against the row, not a bare value error', () => {
    const row = conditionFromApi({ kind: 'range' })
    expect(() => buildCondition(row, 'integer')).toThrow(/at least one bound/)
  })
})

describe('where a required override is enforced', () => {
  it('loads the mode a rule declares', () => {
    const effect: Effect = { required: true, enforce: 'on_write' }
    const { form } = effectFormFromApi(effect, 'string')
    expect(form.requiredOverride).toBe('true')
    expect(form.enforce).toBe('on_write')
  })

  it('reads a rule with no mode as on_read', () => {
    // Every rule written before the mode existed has none, and the default
    // is what the service already did.
    const { form } = effectFormFromApi({ required: true }, 'string')
    expect(form.enforce).toBe('on_read')
  })

  it('leaves on_read implicit, so opening and saving a rule does not rewrite it', () => {
    const effect: Effect = { required: true }
    const { form, passthrough } = effectFormFromApi(effect, 'string')
    expect(buildEffect(form, 'string', passthrough)).toEqual(effect)
  })

  it('sends the mode when the author asks for on_write', () => {
    const { form, passthrough } = effectFormFromApi({ required: true }, 'string')
    form.enforce = 'on_write'
    expect(buildEffect(form, 'string', passthrough)).toEqual({ required: true, enforce: 'on_write' })
  })

  it('drops the mode when the override is cleared', () => {
    // The API refuses enforce on an effect with no required override. Left in
    // the passthrough, it would turn "stop forcing this field" into a 422 the
    // author cannot see the cause of.
    const { form, passthrough } = effectFormFromApi({ required: true, enforce: 'on_write' }, 'string')
    form.requiredOverride = 'none'
    form.oneOf = ['a']
    const built = buildEffect(form, 'string', passthrough) as Record<string, unknown>
    expect(built.enforce).toBeUndefined()
    expect(built.required).toBeUndefined()
  })

  it('drops the mode when the override is flipped to optional', () => {
    const { form, passthrough } = effectFormFromApi({ required: true, enforce: 'on_write' }, 'string')
    form.requiredOverride = 'false'
    const built = buildEffect(form, 'string', passthrough) as Record<string, unknown>
    expect(built.enforce).toBeUndefined()
    expect(built.required).toBe(false)
  })
})
