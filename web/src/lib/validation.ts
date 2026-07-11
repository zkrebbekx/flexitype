// Lightweight client-side field validation for the console forms. These
// mirror the server's rules closely enough to catch mistakes before a round
// trip; the server stays the source of truth.

const SLUG = /^[a-z][a-z0-9_]*$/

/** required returns an error message when the value is blank. */
export function required(value: string, label = 'This field'): string {
  return value.trim() ? '' : `${label} is required`
}

/**
 * slug validates an internal (machine) name: lowercase, starting with a
 * letter, then letters, digits or underscores. Empty is reported as
 * required.
 */
export function slug(value: string, label = 'Internal name'): string {
  if (!value.trim()) return `${label} is required`
  return SLUG.test(value) ? '' : `${label} must be lowercase snake_case (letters, digits, underscore)`
}

/** hasErrors reports whether any field-error map value is non-empty. */
export function hasErrors(errors: Record<string, string>): boolean {
  return Object.values(errors).some(Boolean)
}
