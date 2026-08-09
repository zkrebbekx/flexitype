import type { FormField } from '@flexitype/client'
import { formatValue } from '@flexitype/client'

import { TextInput } from './ui.js'

/**
 * One field of a soft-typed form.
 *
 * Everything it renders comes from the SDK's `FormField`, which the SDK builds
 * out of the type's EFFECTIVE attributes. The console knows no product field
 * by name: a merchant that adds `voltage` to its own subtype gets an input for
 * it with no console change at all.
 */
export interface DynamicFieldProps {
  field: FormField
  /** The editor's value: text for most kinds, boolean for a checkbox. */
  value: string | boolean
  onChange: (value: string | boolean) => void
  /** The message from the last failed save, if this field caused it. */
  error?: string | undefined
  disabled?: boolean
}

export function DynamicField({ field, value, onChange, error, disabled = false }: DynamicFieldProps) {
  const id = `field-${field.name}`
  const describedBy = [error !== undefined ? `${id}-error` : undefined, field.helpText !== undefined ? `${id}-help` : undefined]
    .filter((entry): entry is string => entry !== undefined)
    .join(' ')

  return (
    <div className="space-y-1">
      <label htmlFor={id} className="block text-sm font-medium text-slate-700">
        {field.label}
        {field.required && (
          <span aria-hidden className="ml-1 text-rose-600">
            *
          </span>
        )}
        {field.readOnly && <span className="ml-2 text-xs font-normal text-slate-400">computed</span>}
        {field.declaredIn !== undefined && (
          <span className="ml-2 text-xs font-normal text-slate-400">
            from {field.declaredIn.display_name ?? field.declaredIn.internal_name}
          </span>
        )}
      </label>

      <Control
        id={id}
        field={field}
        value={value}
        onChange={onChange}
        disabled={disabled || field.readOnly}
        describedBy={describedBy === '' ? undefined : describedBy}
      />

      {field.helpText !== undefined && (
        <p id={`${id}-help`} className="text-xs text-slate-500">
          {field.helpText}
        </p>
      )}
      {error !== undefined && (
        <p id={`${id}-error`} role="alert" className="text-xs text-rose-700">
          {error}
        </p>
      )}
    </div>
  )
}

interface ControlProps {
  id: string
  field: FormField
  value: string | boolean
  onChange: (value: string | boolean) => void
  disabled: boolean
  describedBy: string | undefined
}

/** Picks the control from the field's KIND, which the SDK derives from the data type. */
function Control({ id, field, value, onChange, disabled, describedBy }: ControlProps) {
  const text = typeof value === 'boolean' ? String(value) : value
  const common = {
    id,
    disabled,
    required: field.required,
    'aria-describedby': describedBy,
    'aria-invalid': describedBy?.includes('-error') === true ? true : undefined,
  }

  switch (field.kind) {
    case 'checkbox':
      return (
        <input
          {...common}
          type="checkbox"
          checked={value === true || value === 'true'}
          onChange={(event) => onChange(event.target.checked)}
          className="size-4 rounded border-slate-300"
        />
      )

    case 'select':
      return (
        <select
          {...common}
          value={text}
          onChange={(event) => onChange(event.target.value)}
          className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
        >
          <option value="">—</option>
          {(field.options ?? []).map((option) => (
            <option key={String(option.value)} value={String(option.value)}>
              {option.label}
            </option>
          ))}
        </select>
      )

    case 'textarea':
      // A `text` attribute declares that its value is long. That is the one
      // thing a renderer cannot infer from a `string` with a large
      // max_length, and it is why the data type exists.
      return (
        <textarea
          {...common}
          value={text}
          rows={6}
          maxLength={field.constraints.maxLength}
          onChange={(event) => onChange(event.target.value)}
          className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
        />
      )

    case 'json':
      return (
        <textarea
          {...common}
          value={text}
          rows={4}
          spellCheck={false}
          onChange={(event) => onChange(event.target.value)}
          className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 font-mono text-xs"
        />
      )

    case 'file':
      // A media value is written by the upload endpoint, not by this form: the
      // bytes go to the blob store and the VALUE is what comes back.
      return (
        <p className="text-sm text-slate-500">
          {text === '' ? 'No image yet.' : formatValue('media', safeParse(text), { emptyText: 'No image yet.' })}
        </p>
      )

    case 'quantity':
      return (
        <TextInput
          {...common}
          value={text}
          placeholder={field.displayUnit === undefined ? '12 kg' : `12 ${field.displayUnit}`}
          onChange={(event) => onChange(event.target.value)}
        />
      )

    default:
      return (
        <TextInput
          {...common}
          type={inputType(field)}
          value={text}
          // A decimal is arbitrary precision and travels as TEXT. A number
          // input would hand it to the browser's float parser, which drops the
          // trailing zero of "89.50" and, past 15 digits, drops real ones. So a
          // decimal gets a text input with a decimal keypad instead.
          inputMode={field.kind === 'decimal' ? 'decimal' : undefined}
          minLength={field.constraints.minLength}
          maxLength={field.constraints.maxLength}
          pattern={field.constraints.patternSubstring === true ? undefined : field.constraints.pattern}
          onChange={(event) => onChange(event.target.value)}
        />
      )
  }
}

/** The HTML input type for a field kind. */
export function inputType(field: FormField): string {
  switch (field.kind) {
    case 'number':
      return 'number'
    case 'date':
      return 'date'
    case 'time':
      return 'time'
    case 'datetime':
      return 'datetime-local'
    case 'url':
      return 'url'
    case 'email':
      return 'email'
    default:
      return 'text'
  }
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return undefined
  }
}
