/**
 * Form model helpers for the attribute drawer.
 *
 * These live here rather than inline in the component for the reason the
 * dependency editor does: the update PATCH is a FULL REPLACE with no
 * field-presence tracking, so anything the form fails to send is deleted. That
 * rule is worth testing directly, and a component that builds its body inline
 * cannot be.
 *
 * The rule has now been broken five separate times — a data type missing from
 * a list, a default value never modelled, a rollup converted to a plain
 * attribute, constraints the form has no input for, and a compare-and-swap
 * version computed and then dropped on the floor. So the load and the save are
 * ONE pair of functions here, `loadPassthrough` and `buildCarriedUpdate`,
 * rather than a field-by-field copy inside the component: a field added to one
 * is added to the other, and forgetting to carry something is a test failure
 * rather than silent data loss.
 */

import type { ComputedSpec, Constraint, DataType, DefaultValue } from './api'

/**
 * The data types that carry length and pattern constraints.
 *
 * Mirrors `DataType.IsTextual` in `domain/valueobjects/datatype.go`. `text`
 * belongs here: the server accepts these constraints on it, and omitting it
 * made the drawer hide the inputs AND send an empty constraint list, which the
 * full replace then applied.
 */
export const TEXTUAL: DataType[] = ['string', 'text', 'enum', 'url', 'email']

/**
 * The data types that carry min/max value constraints.
 *
 * Mirrors `DataType.IsOrdered`, which includes `quantity`. Omitting it hid the
 * bounds on a quantity attribute and then deleted them on save — and the
 * dependency editor's list already had it, so the two disagreed.
 */
export const ORDERED: DataType[] = [
  'integer',
  'float',
  'decimal',
  'date',
  'time',
  'datetime',
  'quantity',
]

/** isTextual reports whether a type takes length and pattern constraints. */
export function isTextual(dataType: DataType): boolean {
  return TEXTUAL.includes(dataType)
}

/** isOrdered reports whether a type takes min/max value constraints. */
export function isOrdered(dataType: DataType): boolean {
  return ORDERED.includes(dataType)
}

/**
 * modelledKinds are the constraint kinds the drawer can EDIT for a data type.
 *
 * Everything else an attribute carries — a `media` MIME allow-list and size
 * cap, a `one_of` on a plain string — is preserved untouched. The drawer used
 * to emit only what it could edit, so a constraint it had no input for was
 * deleted by the full replace, silently widening what the attribute accepts.
 */
export function modelledKinds(dataType: DataType): Constraint['kind'][] {
  const kinds: Constraint['kind'][] = []
  if (isTextual(dataType)) kinds.push('min_length', 'max_length', 'pattern')
  if (isOrdered(dataType)) kinds.push('min_value', 'max_value')
  if (dataType === 'enum') kinds.push('one_of')
  return kinds
}

/** The state an edit must hand back untouched. */
export interface AttributePassthrough {
  computed?: ComputedSpec
  defaultValue?: DefaultValue
  /**
   * The version of the record the edit was based on.
   *
   * Sent back as a compare-and-swap, so an edit made by another operator
   * between this read and this save is reported rather than erased. It is
   * captured with the rest of the load, NOT read at save time: the record the
   * drawer holds is refetched in the background, so reading it late would send
   * the version of a change the author never saw and swap against nothing.
   */
  version?: number
  /** Constraints the drawer cannot edit for this data type, kept verbatim. */
  constraints: Constraint[]
  /**
   * Whether the loaded pattern matched a substring rather than the whole
   * value. The drawer edits the expression only, and re-emitting without this
   * re-anchored the pattern — silently rejecting values that used to pass.
   */
  patternSubstring?: boolean
  /**
   * The formula as it was loaded, so a CLEARED box can be told apart from an
   * untouched one. Falling back to the stored formula whenever the box was
   * empty meant an operator could never remove a computed spec: the formula
   * they had just deleted was sent straight back.
   */
  loadedFormula: string
}

/** An empty passthrough, for a drawer that is creating rather than editing. */
export function emptyPassthrough(): AttributePassthrough {
  return { constraints: [], loadedFormula: '' }
}

/**
 * loadPassthrough reads everything an edit must preserve.
 *
 * One function, so the component makes one assignment and cannot carry three
 * fields out of six.
 */
export function loadPassthrough(
  attribute:
    | {
        computed?: ComputedSpec
        default_value?: DefaultValue
        version?: number
        constraints?: Constraint[]
      }
    | null
    | undefined,
  dataType: DataType,
): AttributePassthrough {
  if (!attribute) return emptyPassthrough()
  const modelled = modelledKinds(dataType)
  const pattern = attribute.constraints?.find((c) => c.kind === 'pattern')
  return {
    computed: attribute.computed,
    defaultValue: attribute.default_value,
    version: attribute.version,
    constraints: (attribute.constraints ?? []).filter((c) => !modelled.includes(c.kind)),
    patternSubstring: pattern?.substring,
    loadedFormula: attribute.computed?.formula ?? '',
  }
}

/**
 * computedForUpdate decides what `computed` an update sends.
 *
 * A formula the author typed wins. An untouched empty box keeps whatever the
 * attribute already had, so a rollup keeps deriving. An EMPTIED box removes the
 * computed spec, which is the only way to turn a computed attribute back into a
 * plain writable one.
 */
export function computedForUpdate(
  formula: string,
  carried: AttributePassthrough,
): ComputedSpec | undefined {
  const typed = formula.trim()
  if (typed !== '') return { kind: 'formula', formula: typed }
  if (carried.loadedFormula.trim() !== '') return undefined // cleared on purpose
  return carried.computed
}

/** The subset of an update body these helpers own. */
export interface CarriedUpdate {
  computed?: ComputedSpec
  default_value?: DefaultValue
  constraints: Constraint[]
  version?: number
}

/**
 * buildCarriedUpdate assembles the replace-sensitive part of an update: what
 * the form edited, plus everything it could not.
 */
export function buildCarriedUpdate(
  formula: string,
  edited: Constraint[],
  carried: AttributePassthrough,
): CarriedUpdate {
  return {
    computed: computedForUpdate(formula, carried),
    default_value: carried.defaultValue,
    constraints: [...edited, ...carried.constraints],
    version: carried.version,
  }
}
