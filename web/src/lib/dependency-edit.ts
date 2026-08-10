// Form model and (de)serialization for the dependency drawer.
//
// The API can carry more fields than the editor renders. The functions here
// keep every unrecognized field across an open -> save cycle: each loaded
// object keeps its raw form, and a rebuilt object starts from the raw fields
// the editor does not model. The editor's own fields overwrite on top. The
// editor can add or change what it shows; it cannot silently subtract what
// it does not show.

import type { Condition, Constraint, DataType, Effect } from './api'
import { renderTyped, typedValue } from './values'

// The backend's own capability gates, mirrored so the builder only offers
// what the subject's data type supports (condition.Validate).
export const TEXTUAL: DataType[] = ['string', 'text', 'enum', 'url', 'email']
export const ORDERED: DataType[] = ['integer', 'float', 'decimal', 'date', 'time', 'datetime', 'quantity']
export const TEMPORAL: DataType[] = ['date', 'time', 'datetime']

// A range condition is edited as a comparator: 'between' carries both bounds,
// the single-bound comparators encode strictness (> = exclusive min).
export type Comparator = 'between' | 'gt' | 'gte' | 'lt' | 'lte'

export interface ConditionRow {
  kind: Condition['kind']
  value: string
  values: string[]
  newValue: string
  cmp: Comparator
  min: string
  max: string
  minExclusive: boolean
  maxExclusive: boolean
  pattern: string
  patternSubstring: boolean
  op: NonNullable<Condition['op']>
  dynamicKind: 'now' | 'today' | 'relative_time'
  period: string
  amount: string
  // A context condition tests a caller-supplied fact instead of the source
  // value. context_type declares the fact's data type; the API requires it
  // with context_key, and operands validate against it.
  useContext: boolean
  contextKey: string
  contextType: DataType
  // The condition as the API sent it. buildCondition copies its unmodeled
  // fields into the rebuilt condition.
  raw?: Condition
}

export function emptyCondition(kind: Condition['kind'] = 'equals'): ConditionRow {
  return {
    kind,
    value: '',
    values: [],
    newValue: '',
    cmp: 'between',
    min: '',
    max: '',
    minExclusive: false,
    maxExclusive: false,
    pattern: '',
    patternSubstring: false,
    op: 'before',
    dynamicKind: 'today',
    period: 'days',
    amount: '0',
    useContext: false,
    contextKey: '',
    contextType: 'string',
  }
}

// The condition fields the editor models and rebuilds on save. Any other
// field on a loaded condition passes through unchanged.
const CONDITION_FIELDS = new Set([
  'kind',
  'value',
  'values',
  'min',
  'max',
  'min_exclusive',
  'max_exclusive',
  'pattern',
  'pattern_substring',
  'dynamic',
  'op',
  'context_key',
  'context_type',
])

// unknownFields copies the entries of raw whose keys are not in known.
function unknownFields(raw: object | undefined, known: Set<string>): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  if (!raw) return out
  for (const [k, v] of Object.entries(raw)) if (!known.has(k)) out[k] = v
  return out
}

export function conditionFromApi(c: Condition): ConditionRow {
  const row = emptyCondition()
  row.kind = c.kind
  if (c.value) row.value = renderTyped(c.value)
  if (c.values) row.values = c.values.map(renderTyped)
  if (c.min) row.min = renderTyped(c.min)
  if (c.max) row.max = renderTyped(c.max)
  row.minExclusive = c.min_exclusive ?? false
  row.maxExclusive = c.max_exclusive ?? false
  if (c.min && c.max) row.cmp = 'between'
  else if (c.min) row.cmp = row.minExclusive ? 'gt' : 'gte'
  else if (c.max) row.cmp = row.maxExclusive ? 'lt' : 'lte'
  row.pattern = c.pattern ?? ''
  row.patternSubstring = c.pattern_substring ?? false
  if (c.op) row.op = c.op
  if (c.dynamic) {
    row.dynamicKind = c.dynamic.kind
    row.period = c.dynamic.period ?? 'days'
    row.amount = String(c.dynamic.amount ?? 0)
  }
  row.useContext = !!c.context_key
  row.contextKey = c.context_key ?? ''
  if (c.context_type) row.contextType = c.context_type
  row.raw = c
  return row
}

// conditionSubjectType returns the data type the condition's operands
// validate against: the declared fact type for a context condition, else
// the source attribute's type.
export function conditionSubjectType(row: ConditionRow, sourceType: DataType): DataType {
  return row.useContext ? row.contextType : sourceType
}

export function buildCondition(row: ConditionRow, sourceType: DataType): Condition {
  const dt = conditionSubjectType(row, sourceType)
  let c: Condition
  switch (row.kind) {
    case 'equals':
      c = { kind: 'equals', value: typedValue(dt, row.value) }
      break
    case 'in':
      c = { kind: 'in', values: row.values.map((v) => typedValue(dt, v)) }
      break
    case 'range': {
      // Each bound is set only when its own input carries something.
      // 'between' is the default comparator, so an untouched row called
      // typedValue on an empty string and threw a bare "a value is
      // required" that pointed at no field. A range with neither bound is
      // reported against the row instead, which is the thing the author can
      // actually fix.
      c = { kind: 'range' }
      const wantsMin = row.cmp === 'between' || row.cmp === 'gt' || row.cmp === 'gte'
      const wantsMax = row.cmp === 'between' || row.cmp === 'lt' || row.cmp === 'lte'
      if (wantsMin && row.min !== '') {
        c.min = typedValue(dt, row.min)
        if (row.cmp === 'gt' || (row.cmp === 'between' && row.minExclusive)) c.min_exclusive = true
      }
      if (wantsMax && row.max !== '') {
        c.max = typedValue(dt, row.max)
        if (row.cmp === 'lt' || (row.cmp === 'between' && row.maxExclusive)) c.max_exclusive = true
      }
      if (c.min === undefined && c.max === undefined) {
        throw new Error('a range condition needs at least one bound')
      }
      break
    }
    case 'pattern':
      c = { kind: 'pattern', pattern: row.pattern, pattern_substring: row.patternSubstring || undefined }
      break
    case 'dynamic':
      c = {
        kind: 'dynamic',
        op: row.op,
        dynamic:
          row.dynamicKind === 'relative_time'
            ? { kind: 'relative_time', period: row.period, amount: Number(row.amount) }
            : { kind: row.dynamicKind },
      }
      break
  }
  if (row.useContext) {
    if (!row.contextKey.trim()) throw new Error('a context condition needs a fact key')
    c.context_key = row.contextKey.trim()
    c.context_type = row.contextType
  }
  return { ...unknownFields(row.raw, CONDITION_FIELDS), ...c }
}

// The effect-editor form fields. The drawer's reactive form satisfies this
// shape, so load and build work on the drawer state directly.
export interface EffectForm {
  allowedValues: string[]
  requiredOverride: 'none' | 'true' | 'false'
  // Where a "force required" override is enforced. Only meaningful with
  // requiredOverride === 'true': the API refuses enforce on an effect that
  // does not demand a value.
  enforce: 'on_read' | 'on_write'
  oneOf: string[]
  minLength: string
  maxLength: string
  minValue: string
  maxValue: string
  pattern: string
  patternSubstring: boolean
  mime: string
  maxSize: string
}

export function emptyEffectForm(): EffectForm {
  return {
    allowedValues: [],
    requiredOverride: 'none',
    enforce: 'on_read',
    oneOf: [],
    minLength: '',
    maxLength: '',
    minValue: '',
    maxValue: '',
    pattern: '',
    patternSubstring: false,
    mime: '',
    maxSize: '',
  }
}

// EffectPassthrough holds what the editor loaded but does not model:
// - effect: the raw effect, for effect-level fields beyond the known three.
// - byKind: the raw form of each editable constraint, for fields inside a
//   known constraint kind that the editor does not model.
// - constraints: whole constraints the editor cannot edit for this target.
export interface EffectPassthrough {
  effect?: Effect
  byKind: Partial<Record<Constraint['kind'], Constraint>>
  constraints: Constraint[]
}

export function emptyEffectPassthrough(): EffectPassthrough {
  return { byKind: {}, constraints: [] }
}

// editableConstraintKinds returns the constraint kinds the editor renders
// for a target data type. one_of applies to every type; the rest follow the
// type's capabilities. A loaded constraint outside this set passes through
// unchanged on save.
export function editableConstraintKinds(dt: DataType | undefined): Set<Constraint['kind']> {
  const kinds = new Set<Constraint['kind']>(['one_of'])
  if (dt && TEXTUAL.includes(dt)) {
    kinds.add('min_length')
    kinds.add('max_length')
    kinds.add('pattern')
  }
  if (dt && ORDERED.includes(dt)) {
    kinds.add('min_value')
    kinds.add('max_value')
  }
  if (dt === 'media') kinds.add('media')
  return kinds
}

export function effectFormFromApi(
  effect: Effect | undefined,
  targetType: DataType | undefined,
): { form: EffectForm; passthrough: EffectPassthrough } {
  const form = emptyEffectForm()
  const passthrough = emptyEffectPassthrough()
  if (!effect) return { form, passthrough }
  passthrough.effect = effect
  form.allowedValues = (effect.allowed_values ?? []).map(renderTyped)
  form.requiredOverride = effect.required === undefined ? 'none' : (String(effect.required) as 'true' | 'false')
  form.enforce = effect.enforce ?? 'on_read'
  const editable = editableConstraintKinds(targetType)
  for (const c of effect.constraints ?? []) {
    if (!editable.has(c.kind)) {
      passthrough.constraints.push(c)
      continue
    }
    passthrough.byKind[c.kind] = c
    switch (c.kind) {
      case 'min_length':
        form.minLength = c.n === undefined ? '' : String(c.n)
        break
      case 'max_length':
        form.maxLength = c.n === undefined ? '' : String(c.n)
        break
      case 'min_value':
        if (c.value) form.minValue = renderTyped(c.value)
        break
      case 'max_value':
        if (c.value) form.maxValue = renderTyped(c.value)
        break
      case 'pattern':
        form.pattern = c.expr ?? ''
        form.patternSubstring = c.substring ?? false
        break
      case 'one_of':
        form.oneOf = (c.values ?? []).map(renderTyped)
        break
      case 'media':
        form.mime = (c.mime ?? []).join(', ')
        form.maxSize = c.max_size ? String(c.max_size) : ''
        break
    }
  }
  return { form, passthrough }
}

// The constraint fields the client's Constraint type models. Any other
// field on a loaded constraint passes through unchanged.
const CONSTRAINT_FIELDS = new Set(['kind', 'n', 'value', 'expr', 'substring', 'values', 'mime', 'max_size'])

// The effect fields the editor models. Any other field on a loaded effect
// passes through unchanged.
const EFFECT_FIELDS = new Set(['allowed_values', 'constraints', 'required', 'enforce'])

export function buildEffect(form: EffectForm, targetType: DataType, passthrough: EffectPassthrough): Effect {
  const editable = editableConstraintKinds(targetType)
  const cs: Constraint[] = []
  const add = (c: Constraint) => cs.push({ ...unknownFields(passthrough.byKind[c.kind], CONSTRAINT_FIELDS), ...c })
  if (editable.has('min_length') && form.minLength) add({ kind: 'min_length', n: Number(form.minLength) })
  if (editable.has('max_length') && form.maxLength) add({ kind: 'max_length', n: Number(form.maxLength) })
  if (editable.has('min_value') && form.minValue) add({ kind: 'min_value', value: typedValue(targetType, form.minValue) })
  if (editable.has('max_value') && form.maxValue) add({ kind: 'max_value', value: typedValue(targetType, form.maxValue) })
  if (editable.has('pattern') && form.pattern)
    add({ kind: 'pattern', expr: form.pattern, substring: form.patternSubstring || undefined })
  if (form.oneOf.length) add({ kind: 'one_of', values: form.oneOf.map((v) => typedValue(targetType, v)) })
  if (editable.has('media') && (form.mime.trim() || form.maxSize)) {
    const mime = form.mime.split(',').map((m) => m.trim()).filter(Boolean)
    add({ kind: 'media', mime: mime.length ? mime : undefined, max_size: form.maxSize ? Number(form.maxSize) : undefined })
  }
  cs.push(...passthrough.constraints)
  const allowed = form.allowedValues.map((v) => typedValue(targetType, v))
  const required = form.requiredOverride === 'none' ? undefined : form.requiredOverride === 'true'
  const built: Effect = {
    allowed_values: allowed.length ? allowed : undefined,
    constraints: cs.length ? cs : undefined,
    required,
    // Only a rule that DEMANDS a value carries a mode. The API refuses
    // enforce without a required override, so a rule whose override is
    // cleared — or set to "force optional" — must drop it rather than leave
    // it behind in the passthrough and fail the save.
    //
    // on_read is the default and is left implicit, so opening and saving an
    // untouched rule does not rewrite it.
    enforce: required === true && form.enforce === 'on_write' ? 'on_write' : undefined,
  }
  return { ...unknownFields(passthrough.effect, EFFECT_FIELDS), ...built }
}
