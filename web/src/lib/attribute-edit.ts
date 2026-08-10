/**
 * Form model helpers for the attribute drawer.
 *
 * These live here rather than inline in the component for the reason the
 * dependency editor does: the update PATCH is a FULL REPLACE with no
 * field-presence tracking, so anything the form fails to send is deleted. That
 * rule is worth testing directly, and a component that builds its body inline
 * cannot be.
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

/** The data types that carry min/max value constraints. */
export const ORDERED: DataType[] = ['integer', 'float', 'decimal', 'date', 'time', 'datetime']

/** isTextual reports whether a type takes length and pattern constraints. */
export function isTextual(dataType: DataType): boolean {
  return TEXTUAL.includes(dataType)
}

/** The fields the drawer shows but does not edit. */
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
}

/**
 * carriedFields reads what an edit must hand back untouched.
 *
 * The drawer models only a formula, so saving a ROLLUP attribute turned it
 * into a plain writable one; and it never modelled `default_value` at all, so
 * a rename deleted the stored default. Renaming an attribute is not a request
 * to change what it derives from.
 */
export function carriedFields(attribute?: {
  computed?: ComputedSpec
  default_value?: DefaultValue
  version?: number
}): AttributePassthrough {
  return {
    computed: attribute?.computed,
    defaultValue: attribute?.default_value,
    version: attribute?.version,
  }
}

/**
 * computedForUpdate decides what `computed` an update sends.
 *
 * A formula the author typed wins. Otherwise whatever the attribute already
 * had survives, so a rollup keeps deriving and an untouched formula keeps its
 * shape.
 */
export function computedForUpdate(
  formula: string,
  carried: AttributePassthrough,
): ComputedSpec | undefined {
  const typed = formula.trim()
  if (typed !== '') return { kind: 'formula', formula: typed }
  return carried.computed
}

/** The subset of an update body these helpers own. */
export interface CarriedUpdate {
  computed?: ComputedSpec
  default_value?: DefaultValue
  constraints: Constraint[]
  version?: number
}

/** buildCarriedUpdate assembles the replace-sensitive part of an update. */
export function buildCarriedUpdate(
  formula: string,
  constraints: Constraint[],
  carried: AttributePassthrough,
): CarriedUpdate {
  return {
    computed: computedForUpdate(formula, carried),
    default_value: carried.defaultValue,
    constraints,
    version: carried.version,
  }
}
