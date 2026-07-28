import { describe, it, expect } from 'vitest'
import {
  accountsHolding,
  describeRole,
  fromPermissionRows,
  scopesFrom,
  toPermissionRows,
} from './roles'
import type { Role, ServiceAccount } from './api'

const account = (name: string, roles?: string[]): ServiceAccount => ({
  id: name,
  name,
  scopes: ['read'],
  roles,
  active: true,
})

describe('accountsHolding', () => {
  it('finds the accounts a delete would strand', () => {
    const accounts = [account('a', ['analyst']), account('b'), account('c', ['analyst', 'ops'])]
    expect(accountsHolding(accounts, 'analyst').map((a) => a.name)).toEqual(['a', 'c'])
  })

  it('is empty when nobody holds it, which is when a delete succeeds', () => {
    expect(accountsHolding([account('a', ['other'])], 'analyst')).toEqual([])
  })

  it('treats a missing roles field as holding nothing', () => {
    expect(accountsHolding([account('a')], 'analyst')).toEqual([])
  })
})

describe('permission rows', () => {
  it('round-trips a stored map', () => {
    const stored = { ssn: 'none', salary: 'read' } as const
    expect(fromPermissionRows(toPermissionRows(stored))).toEqual(stored)
  })

  it('orders rows by attribute so the editor does not reshuffle', () => {
    expect(toPermissionRows({ ssn: 'none', salary: 'read' }).map((r) => r.attribute)).toEqual([
      'salary',
      'ssn',
    ])
  })

  it('drops a half-typed row rather than sending an empty attribute', () => {
    expect(
      fromPermissionRows([
        { attribute: '  ', level: 'none' },
        { attribute: ' salary ', level: 'read' },
      ]),
    ).toEqual({ salary: 'read' })
  })

  it('keeps the last of a duplicated attribute, matching what the form shows', () => {
    expect(
      fromPermissionRows([
        { attribute: 'salary', level: 'none' },
        { attribute: 'salary', level: 'write' },
      ]),
    ).toEqual({ salary: 'write' })
  })

  it('has no entries for an empty editor', () => {
    expect(fromPermissionRows([])).toEqual({})
  })
})

describe('scopesFrom', () => {
  it('builds the scope list from the checkboxes', () => {
    expect(scopesFrom({ read: true, write: false })).toEqual(['read'])
    expect(scopesFrom({ read: true, write: true })).toEqual(['read', 'write'])
  })

  it('allows none: a role may carry only field permissions', () => {
    expect(scopesFrom({ read: false, write: false })).toEqual([])
  })
})

describe('describeRole', () => {
  const role = (r: Partial<Role>): Role => ({ id: '1', name: 'analyst', scopes: [], ...r })

  it('renders scopes and permissions together', () => {
    expect(
      describeRole(role({ scopes: ['read'], field_permissions: { ssn: 'none', salary: 'read' } })),
    ).toBe('read — salary:read ssn:none')
  })

  it('says "no scopes" rather than looking like missing data', () => {
    expect(describeRole(role({ field_permissions: { salary: 'none' } }))).toBe(
      'no scopes — salary:none',
    )
  })

  it('renders a scope-only role', () => {
    expect(describeRole(role({ scopes: ['read', 'write'] }))).toBe('read, write')
  })
})
