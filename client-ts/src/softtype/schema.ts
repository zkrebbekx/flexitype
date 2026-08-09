/**
 * The soft schema an application renders from.
 *
 * flexitype types are defined at runtime, so no generated interface can
 * describe an entity's fields. A form or a grid has to be built from the
 * type's EFFECTIVE attributes — its own plus everything it inherits through
 * `extends_id` — and from the constraints each one carries. These helpers turn
 * that data into a descriptor a renderer can walk without knowing the schema.
 */
import type {
  Attribute,
  Constraint,
  DataType,
  DefaultValue,
  EffectiveAttribute,
  EffectiveSchema,
  TypedValue,
  TypeDefinition,
} from '../models.js'

/** One link of a type's inheritance chain, with the attributes it declares. */
export interface TypeChainEntry {
  type: TypeDefinition
  attributes: Attribute[]
}

/** The options `resolveEffectiveAttributes` accepts. */
export interface ResolveOptions {
  /** Keeps archived attributes. They are dropped by default. */
  includeArchived?: boolean
}

/**
 * Resolves a chain of types into the attribute set an entity of the leaf type
 * may hold.
 *
 * Pass the chain LEAF FIRST, as the service builds it: the leaf type, then its
 * parent, then its parent, with the root last. The result is ordered the way
 * the API orders it — by group, then by `sort_order`, stably — so a form built
 * from this and a form built from `client.types.effectiveAttributes` lay out
 * the same.
 *
 * The service refuses an attribute whose internal name is already declared
 * anywhere in the hierarchy, so a name appears once. This still resolves a
 * duplicate in favour of the declaration nearest the leaf, because a chain
 * assembled in a client from several reads can hold a stale one.
 */
export function resolveEffectiveAttributes(
  chain: readonly TypeChainEntry[],
  options: ResolveOptions = {},
): EffectiveAttribute[] {
  const seen = new Set<string>()
  const resolved: EffectiveAttribute[] = []
  for (const link of chain) {
    for (const attribute of link.attributes) {
      if (options.includeArchived !== true && attribute.archived_at !== undefined) continue
      const name = attribute.internal_name
      if (name !== undefined) {
        if (seen.has(name)) continue
        seen.add(name)
      }
      resolved.push({ attribute, declared_in: link.type })
    }
  }
  return sortEffectiveAttributes(resolved)
}

/**
 * Orders attributes by group, then by `sort_order`. The sort is stable, so
 * attributes that share a group and an order keep the order they arrived in —
 * which is declaration order, leaf first.
 */
export function sortEffectiveAttributes(attributes: readonly EffectiveAttribute[]): EffectiveAttribute[] {
  return [...attributes].sort((x, y) => {
    const a = x.attribute
    const b = y.attribute
    const groupA = a?.group ?? ''
    const groupB = b?.group ?? ''
    if (groupA !== groupB) return groupA < groupB ? -1 : 1
    return (a?.sort_order ?? 0) - (b?.sort_order ?? 0)
  })
}

/** The kind of control a field wants. */
export type FieldKind =
  | 'text'
  | 'textarea'
  | 'number'
  | 'decimal'
  | 'checkbox'
  | 'select'
  | 'date'
  | 'time'
  | 'datetime'
  | 'json'
  | 'file'
  | 'quantity'
  | 'url'
  | 'email'

/** One choice of a select field. */
export interface FieldOption {
  /** The value to write, in its wire form. */
  value: unknown
  /** A label to show. It is the value rendered as text unless one is supplied. */
  label: string
}

/** The bounds and patterns a field enforces, flattened out of the constraints. */
export interface FieldConstraints {
  minLength?: number
  maxLength?: number
  /** The lower bound, in the attribute's wire form. */
  min?: unknown
  /** The upper bound, in the attribute's wire form. */
  max?: unknown
  /** A regular expression the whole value must match, unless `patternSubstring`. */
  pattern?: string
  /** The pattern matches anywhere in the value instead of the whole value. */
  patternSubstring?: boolean
  /** The MIME types a media field accepts. */
  mime?: string[]
  /** The largest media size in bytes. */
  maxSize?: number
}

/** Everything a renderer needs to draw one field. */
export interface FormField {
  /** The attribute definition id — what a write addresses. */
  attributeId: string
  /** The attribute's internal name — what FQL and CSV columns address. */
  name: string
  /** The label to show. */
  label: string
  description?: string
  helpText?: string
  group?: string
  sortOrder: number
  dataType: DataType
  kind: FieldKind
  /** The field must hold a value. A dependency can turn this on. */
  required: boolean
  /** The attribute holds several values at once. */
  multiValued: boolean
  /** The value must be unique across the type's entities. */
  unique: boolean
  /** The value is addressed by locale, so the form needs a locale selector. */
  localizable: boolean
  /** The value is addressed by channel. */
  scopable: boolean
  /** The service computes the value. A form must show it and refuse to edit it. */
  readOnly: boolean
  /** The choices, for an enum or an attribute a `one_of` constraint restricts. */
  options?: FieldOption[]
  /**
   * A dependency has narrowed the choices for this entity. A renderer should
   * disable everything outside `options` rather than hide the fact.
   */
  restricted: boolean
  constraints: FieldConstraints
  /** The unit family a quantity field draws its units from. */
  unitFamilyId?: string
  /** The unit a quantity field should offer first. */
  displayUnit?: string
  defaultValue?: DefaultValue
  /** The type that declares the attribute — "Declared here" or "Inherited from X". */
  declaredIn?: TypeDefinition
}

/** A named set of fields. The unnamed group holds everything ungrouped. */
export interface FormGroup {
  /** The group's name, or undefined for the ungrouped fields. */
  name?: string
  fields: FormField[]
}

/** A whole form, ready to render. */
export interface FormDescriptor {
  fields: FormField[]
  groups: FormGroup[]
  /** Fields by internal name, for a renderer that addresses them by name. */
  byName: Record<string, FormField>
  /** Fields by attribute id, for a renderer that addresses them by id. */
  byId: Record<string, FormField>
}

/** The options `toFormDescriptor` accepts. */
export interface FormDescriptorOptions {
  /**
   * The dependency-resolved state of one or more attributes for ONE entity,
   * keyed by attribute definition id and read from
   * `client.entities.effectiveSchema(...)`.
   *
   * A dependency can make an attribute required that the definition does not,
   * and can narrow the values it allows. Without this the form shows the
   * static schema, which is right for a new entity and wrong for an existing
   * one.
   */
  overrides?: Record<string, EffectiveSchema>
  /** Renders an option's label. It defaults to the value as text. */
  optionLabel?: (value: unknown, attribute: Attribute) => string
}

/**
 * Turns a type's effective attributes into a form descriptor.
 *
 * ```ts
 * const attributes = await client.types.effectiveAttributes(typeId)
 * const form = toFormDescriptor(attributes)
 * for (const group of form.groups) {
 *   for (const field of group.fields) render(field)
 * }
 * ```
 */
export function toFormDescriptor(
  attributes: readonly EffectiveAttribute[],
  options: FormDescriptorOptions = {},
): FormDescriptor {
  const fields: FormField[] = []
  for (const entry of sortEffectiveAttributes(attributes)) {
    const field = toFormField(entry, options)
    if (field !== undefined) fields.push(field)
  }

  const groups: FormGroup[] = []
  const byName: Record<string, FormField> = {}
  const byId: Record<string, FormField> = {}
  for (const field of fields) {
    byName[field.name] = field
    byId[field.attributeId] = field
    const last = groups[groups.length - 1]
    if (last !== undefined && last.name === field.group) last.fields.push(field)
    else groups.push(field.group === undefined ? { fields: [field] } : { name: field.group, fields: [field] })
  }

  return { fields, groups, byName, byId }
}

/** Turns one effective attribute into a field, or undefined when it is unusable. */
export function toFormField(
  entry: EffectiveAttribute,
  options: FormDescriptorOptions = {},
): FormField | undefined {
  const attribute = entry.attribute
  if (attribute === undefined) return undefined
  const attributeId = attribute.id
  const name = attribute.internal_name
  const dataType = attribute.data_type
  if (attributeId === undefined || name === undefined || dataType === undefined) return undefined

  const override = options.overrides?.[attributeId]
  const constraints = flattenConstraints(attribute.constraints ?? [])
  const allowed = override?.allowed_values
  const options_ =
    allowed !== undefined && allowed.length > 0
      ? allowed.map((value) => toOption(unwrapTyped(value), attribute, options))
      : optionsOf(attribute, options)

  const field: FormField = {
    attributeId,
    name,
    label: attribute.display_name ?? name,
    sortOrder: attribute.sort_order ?? 0,
    dataType,
    kind: fieldKind(dataType, options_ !== undefined),
    required: override?.required ?? attribute.required ?? false,
    multiValued: attribute.multi_valued ?? false,
    unique: attribute.unique ?? false,
    localizable: attribute.localizable ?? false,
    scopable: attribute.scopable ?? false,
    readOnly: attribute.computed !== undefined,
    restricted: override?.restricted ?? false,
    constraints,
  }
  if (attribute.description !== undefined) field.description = attribute.description
  if (attribute.help_text !== undefined) field.helpText = attribute.help_text
  if (attribute.group !== undefined) field.group = attribute.group
  if (options_ !== undefined) field.options = options_
  if (attribute.unit_family_id !== undefined) field.unitFamilyId = attribute.unit_family_id
  if (attribute.display_unit !== undefined) field.displayUnit = attribute.display_unit
  if (attribute.default_value !== undefined) field.defaultValue = attribute.default_value
  if (entry.declared_in !== undefined) field.declaredIn = entry.declared_in
  return field
}

/** The control a data type wants. A restricted attribute becomes a select. */
export function fieldKind(dataType: DataType, hasOptions = false): FieldKind {
  if (hasOptions) return 'select'
  switch (dataType) {
    case 'bool':
      return 'checkbox'
    case 'integer':
    case 'float':
      return 'number'
    case 'decimal':
      return 'decimal'
    case 'enum':
      return 'select'
    case 'date':
      return 'date'
    case 'time':
      return 'time'
    case 'datetime':
      return 'datetime'
    case 'json':
      return 'json'
    case 'media':
      return 'file'
    case 'quantity':
      return 'quantity'
    case 'text':
      // A `text` attribute declares that its value is long, which is the one
      // thing a renderer cannot infer from a `string` with a large
      // max_length. It draws a text area.
      return 'textarea'
    case 'url':
      return 'url'
    case 'email':
      return 'email'
    default:
      return 'text'
  }
}

/** Reads the `one_of` members of an attribute, if it declares any. */
function optionsOf(attribute: Attribute, options: FormDescriptorOptions): FieldOption[] | undefined {
  for (const constraint of attribute.constraints ?? []) {
    if (constraint.kind !== 'one_of') continue
    const values = constraint.values ?? []
    if (values.length === 0) continue
    return values.map((value) => toOption(unwrapTyped(value), attribute, options))
  }
  return undefined
}

function toOption(value: unknown, attribute: Attribute, options: FormDescriptorOptions): FieldOption {
  return { value, label: options.optionLabel?.(value, attribute) ?? String(value) }
}

/**
 * Reads the payload of a self-describing value.
 *
 * Constraint operands and allowed values travel as `{ type, value }` so they
 * decode without the attribute definition. A form only needs the payload, and
 * this tolerates a bare value for a caller that already unwrapped one.
 */
export function unwrapTyped(value: TypedValue | unknown): unknown {
  if (typeof value === 'object' && value !== null && 'type' in value && 'value' in value) {
    return (value as TypedValue).value
  }
  return value
}

/** Flattens an attribute's constraints into the bounds a control enforces. */
export function flattenConstraints(constraints: readonly Constraint[]): FieldConstraints {
  const out: FieldConstraints = {}
  for (const constraint of constraints) {
    switch (constraint.kind) {
      case 'min_length':
        if (constraint.n !== undefined) out.minLength = constraint.n
        break
      case 'max_length':
        if (constraint.n !== undefined) out.maxLength = constraint.n
        break
      case 'min_value':
        out.min = unwrapTyped(constraint.value)
        break
      case 'max_value':
        out.max = unwrapTyped(constraint.value)
        break
      case 'pattern':
        if (constraint.expr !== undefined) out.pattern = constraint.expr
        if (constraint.substring === true) out.patternSubstring = true
        break
      case 'media':
        if (constraint.mime !== undefined) out.mime = constraint.mime
        if (constraint.max_size !== undefined) out.maxSize = constraint.max_size
        break
      default:
        break
    }
  }
  return out
}

/** Reads the attributes of a type from the API and builds a form descriptor. */
export interface EffectiveAttributeSource {
  types: { effectiveAttributes(id: string): Promise<EffectiveAttribute[]> }
}

/**
 * Loads a type's effective attributes and turns them into a form descriptor in
 * one call. It is the shortest path from a type id to a renderable form.
 */
export async function loadFormDescriptor(
  client: EffectiveAttributeSource,
  typeId: string,
  options: FormDescriptorOptions = {},
): Promise<FormDescriptor> {
  return toFormDescriptor(await client.types.effectiveAttributes(typeId), options)
}
