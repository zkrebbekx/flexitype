import { describe, expect, it } from 'vitest'
import { ancestorsOf, buildTypeTree } from './tree'
import type { TypeDefinition } from './api'

function td(id: string, name: string, extendsId?: string): TypeDefinition {
  return {
    id,
    tenant_id: 'default',
    internal_name: name,
    display_name: name,
    extends_id: extendsId,
    version: 1,
    created_at: '',
    updated_at: '',
  }
}

describe('buildTypeTree', () => {
  it('nests subtypes under parents depth-first with tree metadata', () => {
    const nodes = buildTypeTree([
      td('p', 'product'),
      td('mtb', 'mountain_bike', 'b'),
      td('b', 'bike', 'p'),
      td('s', 'supplier'),
      td('c', 'car', 'p'),
    ])

    expect(nodes.map((n) => [n.type.internal_name, n.depth])).toEqual([
      ['product', 0],
      ['bike', 1],
      ['mountain_bike', 2],
      ['car', 1],
      ['supplier', 0],
    ])
    expect(nodes[0]!.childCount).toBe(2)
    expect(nodes[3]!.isLast).toBe(true) // car is product's last child (sorted)
  })

  it('renders orphans (filtered-out parents) as roots', () => {
    const nodes = buildTypeTree([td('b', 'bike', 'missing-parent')])
    expect(nodes).toHaveLength(1)
    expect(nodes[0]!.depth).toBe(0)
  })

  it('survives a cycle without spinning', () => {
    const nodes = buildTypeTree([td('a', 'a', 'b'), td('b', 'b', 'a')])
    expect(nodes.length).toBeLessThanOrEqual(2)
  })
})

describe('ancestorsOf', () => {
  it('walks extends pointers nearest-first', () => {
    const all = [td('p', 'product'), td('b', 'bike', 'p'), td('mtb', 'mountain_bike', 'b')]
    expect(ancestorsOf(all, 'mtb').map((t) => t.internal_name)).toEqual(['bike', 'product'])
    expect(ancestorsOf(all, 'p')).toEqual([])
  })
})
