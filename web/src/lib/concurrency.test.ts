import { describe, expect, it } from 'vitest'
import { ApiError } from './api'
import { baselineVersion, staleEditMessage } from './concurrency'

describe('baselineVersion', () => {
  it('reads the version an edit is based on', () => {
    expect(baselineVersion({ version: 7 })).toBe(7)
  })

  it('is undefined for a record that has none, keeping last-write-wins', () => {
    expect(baselineVersion(undefined)).toBeUndefined()
    expect(baselineVersion(null)).toBeUndefined()
    expect(baselineVersion({})).toBeUndefined()
  })
})

describe('staleEditMessage', () => {
  it('reports the other writer and names the recovery', () => {
    const msg = staleEditMessage(
      new ApiError(409, 'CONFLICT', 'the type definition was modified by someone else; reload it and retry'),
    )
    expect(msg).toContain('modified by someone else')
    // Without this the operator retries the same stale version for ever.
    expect(msg).toContain('reopen')
  })

  it('leaves every other failure to the ordinary path', () => {
    expect(staleEditMessage(new ApiError(422, 'VALIDATION', 'no'))).toBeNull()
    expect(staleEditMessage(new Error('boom'))).toBeNull()
  })
})
