/**
 * The fourth reader of the error-code contract.
 *
 * `TestErrorCodeContract` in the repository root holds three lists equal: the
 * `Error.code` enum in `api/openapi.yaml`, the `ErrorCode` constants in
 * `client/errors.go`, and the paragraph in `docs/api-stability.md`. They had
 * drifted three ways at once because each list lived in a different file and
 * no test read more than one.
 *
 * This adds the TypeScript list to that equality, and it reads the same three
 * files rather than a copy of them, so a code added to the service without
 * being added here fails on the next run.
 */
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { ERROR_CODES } from '../src/errors.js'

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), '..', '..')

function readRepoFile(path: string): string {
  return readFileSync(join(repoRoot, path), 'utf8')
}

function sorted(values: readonly string[]): string[] {
  return [...new Set(values.map((v) => v.trim()).filter((v) => v !== ''))].sort()
}

/** The Error.code enum of the OpenAPI document. */
function openApiErrorCodes(): string[] {
  const spec = readRepoFile('api/openapi.yaml')
  const match = /^\s*enum: \[(VALIDATION[^\]]*)\]/m.exec(spec)
  if (match?.[1] === undefined) throw new Error('api/openapi.yaml: could not find the Error.code enum')
  return sorted(match[1].split(','))
}

/** The ErrorCode constants of the Go client. It is another module, so read the source. */
function goClientErrorCodes(): string[] {
  const source = readRepoFile('client/errors.go')
  const found = [...source.matchAll(/ErrorCode = "([A-Z_]+)"/g)].map((m) => m[1] as string)
  if (found.length === 0) throw new Error('client/errors.go: found no ErrorCode constants')
  return sorted(found)
}

/** The codes the stability document publishes. */
function stabilityDocErrorCodes(): string[] {
  const doc = readRepoFile('docs/api-stability.md')
  const start = doc.indexOf('Error responses carry stable machine codes')
  if (start < 0) throw new Error('docs/api-stability.md: could not find the code list')
  const end = doc.indexOf('New codes may be added', start)
  if (end < 0) throw new Error('docs/api-stability.md: could not find the end of the code list')
  return sorted([...doc.slice(start, end).matchAll(/`([A-Z_]+)`/g)].map((m) => m[1] as string))
}

describe('the error-code contract', () => {
  const spec = openApiErrorCodes()

  it('parses a non-trivial enum, so the comparison means something', () => {
    expect(spec.length).toBeGreaterThan(5)
  })

  it('declares exactly the codes the OpenAPI document enumerates', () => {
    expect(sorted(ERROR_CODES)).toEqual(spec)
  })

  it('declares exactly the codes the Go client declares', () => {
    expect(sorted(ERROR_CODES)).toEqual(goClientErrorCodes())
  })

  it('declares exactly the codes docs/api-stability.md lists', () => {
    expect(sorted(ERROR_CODES)).toEqual(stabilityDocErrorCodes())
  })
})
