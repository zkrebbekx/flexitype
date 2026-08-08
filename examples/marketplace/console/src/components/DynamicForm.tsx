import { useMemo, useState } from 'react'
import { FlexitypeError, formatValue, toWire, type FormDescriptor, type FormField } from '@flexitype/client'

import { DynamicField } from './DynamicField.js'
import { Alert, Button } from './ui.js'

/** The editor state of one form: text for most kinds, boolean for a checkbox. */
export type FormState = Record<string, string | boolean>

export interface DynamicFormProps {
  descriptor: FormDescriptor
  state: FormState
  onChange: (state: FormState) => void
  /**
   * Receives the wire values, keyed by attribute internal name and ready for
   * the platform's atomic batch write. A localizable field is keyed
   * `name@fr` when a locale is chosen.
   */
  onSubmit: (values: Record<string, unknown>) => void
  /** The locale a localizable field writes into. Empty means the base value. */
  locale?: string
  submitting?: boolean
  submitLabel?: string
}

/**
 * A whole product form, rendered from the type's effective attributes.
 *
 * Nothing here names a product field. The descriptor comes from the SDK's
 * `toFormDescriptor`, so a merchant's own subtype fields render beside the
 * inherited ones, in the schema's sort order, with the constraints the schema
 * declares.
 *
 * Coercion is the SDK's `toWire`, so a decimal stays a decimal string, an
 * integer is rejected before the request rather than after it, and the failure
 * a bad value produces carries the SAME code — VALIDATION — the service would
 * have answered with.
 */
export function DynamicForm({
  descriptor,
  state,
  onChange,
  onSubmit,
  locale = '',
  submitting = false,
  submitLabel = 'Save product',
}: DynamicFormProps) {
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [formError, setFormError] = useState<string>()

  const editable = useMemo(() => descriptor.fields.filter((field) => !field.multiValued), [descriptor])
  const multiValued = useMemo(() => descriptor.fields.filter((field) => field.multiValued), [descriptor])

  function submit(event: React.FormEvent) {
    event.preventDefault()
    const values: Record<string, unknown> = {}
    const errors: Record<string, string> = {}

    for (const field of editable) {
      if (field.readOnly) continue // The service computes it; a write would be refused.
      const raw = state[field.name]
      if (raw === undefined || raw === '') continue
      try {
        values[keyFor(field, locale)] = toWire(field.dataType, raw)
      } catch (error) {
        errors[field.name] =
          error instanceof FlexitypeError ? error.message : String(error)
      }
    }

    setFieldErrors(errors)
    if (Object.keys(errors).length > 0) {
      setFormError('Some fields could not be encoded. Nothing was sent.')
      return
    }
    if (Object.keys(values).length === 0) {
      setFormError('Fill at least one field.')
      return
    }
    setFormError(undefined)
    onSubmit(values)
  }

  return (
    // noValidate: the schema is the authority, and it is enforced twice — by
    // the SDK's toWire before the request, and by the service after it. Native
    // validation would block the submit with a browser bubble and the field
    // would never show the message the schema actually produced.
    <form onSubmit={submit} noValidate className="space-y-6">
      {descriptor.groups.map((group, index) => (
        <fieldset key={group.name ?? `ungrouped-${index}`} className="space-y-4">
          {group.name !== undefined && (
            <legend className="text-sm font-semibold text-slate-700">{group.name}</legend>
          )}
          {group.fields
            .filter((field) => !field.multiValued)
            .map((field) => (
              <DynamicField
                key={field.attributeId}
                field={field}
                value={state[field.name] ?? ''}
                onChange={(value) => onChange({ ...state, [field.name]: value })}
                error={fieldErrors[field.name]}
                disabled={submitting}
              />
            ))}
        </fieldset>
      ))}

      {multiValued.length > 0 && (
        <p className="text-xs text-slate-500">
          {multiValued.map((field) => field.label).join(', ')}{' '}
          {multiValued.length === 1 ? 'is multi-valued' : 'are multi-valued'} and not editable here: a batch
          write sets one value per attribute, and a multi-valued write APPENDS. Editing them needs add and
          remove, which this form does not express.
        </p>
      )}

      {formError !== undefined && <Alert>{formError}</Alert>}

      <Button type="submit" disabled={submitting}>
        {submitting ? 'Saving…' : submitLabel}
      </Button>
    </form>
  )
}

/** The write key for a field: `name` normally, `name@fr` for a localized write. */
export function keyFor(field: FormField, locale: string): string {
  return field.localizable && locale !== '' ? `${field.name}@${locale}` : field.name
}

/**
 * Turns the values an entity already holds into editor state.
 *
 * A JSON or media value becomes its serialized text, a boolean stays a
 * boolean, and everything else is rendered in its wire form — which is stable
 * and round-trips through `toWire` unchanged.
 */
export function toFormState(
  descriptor: FormDescriptor,
  values: Record<string, unknown>,
): FormState {
  const state: FormState = {}
  for (const field of descriptor.fields) {
    const wire = values[field.name]
    if (wire === undefined || wire === null) continue
    if (field.kind === 'checkbox') {
      state[field.name] = wire === true || wire === 'true'
      continue
    }
    if (field.kind === 'json' || field.kind === 'file' || typeof wire === 'object') {
      state[field.name] = JSON.stringify(wire)
      continue
    }
    state[field.name] = formatValue(field.dataType, wire)
  }
  return state
}
