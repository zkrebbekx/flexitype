/**
 * The generated types cannot drift from the OpenAPI document.
 *
 * `api/openapi.yaml` is the contract, and a route-coverage test in the
 * repository root already holds it equal to the handler. This test closes the
 * last gap: it regenerates the types and compares the result with the file
 * that is checked in. A change to the document that nobody regenerated fails
 * here rather than reaching an application as a wrong type.
 *
 * It runs the same command `npm run generate` runs, so there is one generator
 * and one set of flags.
 */
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterAll, describe, expect, it } from 'vitest'

const packageRoot = join(dirname(fileURLToPath(import.meta.url)), '..')
const scratch = mkdtempSync(join(tmpdir(), 'flexitype-openapi-'))

afterAll(() => {
  rmSync(scratch, { recursive: true, force: true })
})

/** The flags of the `generate` script, read from package.json rather than copied. */
function generateArgs(): string[] {
  const pkg = JSON.parse(readFileSync(join(packageRoot, 'package.json'), 'utf8')) as {
    scripts: Record<string, string>
  }
  const script = pkg.scripts.generate
  if (script === undefined) throw new Error('package.json: no "generate" script')
  return script.split(/\s+/).slice(1)
}

describe('the generated types', () => {
  it('match a fresh run of npm run generate', () => {
    const args = generateArgs()
    const outputIndex = args.findIndex((a) => a === '--output' || a === '-o')
    expect(outputIndex).toBeGreaterThan(-1)
    const checkedInPath = join(packageRoot, args[outputIndex + 1] as string)
    const freshPath = join(scratch, 'openapi.ts')

    const freshArgs = [...args]
    freshArgs[outputIndex + 1] = freshPath
    execFileSync('npx', ['openapi-typescript', ...freshArgs], { cwd: packageRoot, stdio: 'pipe' })

    const checkedIn = readFileSync(checkedInPath, 'utf8')
    const fresh = readFileSync(freshPath, 'utf8')

    if (checkedIn !== fresh) {
      throw new Error(
        'src/generated/openapi.ts is stale: api/openapi.yaml has changed since it was generated. ' +
          'Run "npm run generate" in client-ts and commit the result.',
      )
    }
    expect(checkedIn).toBe(fresh)
  }, 60_000)
})
