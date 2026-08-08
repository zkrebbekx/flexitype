/**
 * The query-key scheme.
 *
 * Every key starts with the literal `'flexitype'`, then names a resource, then
 * a shape, then the arguments — widest first, narrowest last:
 *
 * ```
 * ['flexitype', <resource>, <shape>, ...<arguments>]
 * ```
 *
 * The order is what makes an invalidation precise. TanStack Query matches a
 * key by PREFIX, so an argument that narrows the query must come after the one
 * that widens it. A derived query hangs off the key of the record it derives
 * from, which is why an entity's values live under the entity's own key rather
 * than beside it.
 *
 * | key | covers |
 * |---|---|
 * | `['flexitype']` | everything this package caches |
 * | `['flexitype','types']` | every type query |
 * | `['flexitype','types','list',opts]` | one page of types |
 * | `['flexitype','types','detail',id]` | one type AND everything derived from it |
 * | `['flexitype','types','detail',id,'effective-attributes']` | that type's effective attributes |
 * | `['flexitype','entities','list',typeId,opts]` | one page of a type's entities |
 * | `['flexitype','entities','detail',typeId,entityId]` | one entity and all its derived queries |
 * | `['flexitype','entities','detail',typeId,entityId,'values',opts]` | that entity's values |
 * | `['flexitype','query','run',type,q,opts]` | one FQL result set |
 *
 * So `invalidateQueries({ queryKey: flexitypeKeys.entities.detail(t, e) })`
 * refreshes that entity's values, completeness, relationships and revisions in
 * one call, and touches no other entity.
 *
 * THE SCHEME IS STABLE API. A key's prefix does not change in a minor release,
 * so invalidation code written against it keeps working.
 */

/** The first element of every key this package builds. */
export const FLEXITYPE_KEY_ROOT = 'flexitype' as const

const root = [FLEXITYPE_KEY_ROOT] as const

/** The query keys, grouped by resource. */
export const flexitypeKeys = {
  /** Everything this package caches. */
  all: root,

  types: {
    all: [...root, 'types'] as const,
    list: (options?: unknown) => [...root, 'types', 'list', options ?? {}] as const,
    /** One type and everything derived from it. */
    detail: (id: string) => [...root, 'types', 'detail', id] as const,
    attributes: (id: string, options?: unknown) =>
      [...root, 'types', 'detail', id, 'attributes', options ?? {}] as const,
    effectiveAttributes: (id: string) => [...root, 'types', 'detail', id, 'effective-attributes'] as const,
    children: (id: string) => [...root, 'types', 'detail', id, 'children'] as const,
    completeness: (id: string) => [...root, 'types', 'detail', id, 'completeness'] as const,
    matchRules: (id: string) => [...root, 'types', 'detail', id, 'match-rules'] as const,
  },

  attributes: {
    all: [...root, 'attributes'] as const,
    list: (options?: unknown) => [...root, 'attributes', 'list', options ?? {}] as const,
    detail: (id: string) => [...root, 'attributes', 'detail', id] as const,
  },

  values: {
    all: [...root, 'values'] as const,
    list: (options?: unknown) => [...root, 'values', 'list', options ?? {}] as const,
    detail: (id: string) => [...root, 'values', 'detail', id] as const,
  },

  entities: {
    all: [...root, 'entities'] as const,
    /** Every list of one type's entities. */
    lists: (typeId: string) => [...root, 'entities', 'list', typeId] as const,
    list: (typeId: string, options?: unknown) => [...root, 'entities', 'list', typeId, options ?? {}] as const,
    /** One entity and everything derived from it. */
    detail: (typeId: string, entityId: string) => [...root, 'entities', 'detail', typeId, entityId] as const,
    values: (typeId: string, entityId: string, options?: unknown) =>
      [...root, 'entities', 'detail', typeId, entityId, 'values', options ?? {}] as const,
    relationships: (typeId: string, entityId: string) =>
      [...root, 'entities', 'detail', typeId, entityId, 'relationships'] as const,
    completeness: (typeId: string, entityId: string) =>
      [...root, 'entities', 'detail', typeId, entityId, 'completeness'] as const,
    effectiveSchema: (typeId: string, entityId: string, attributeId: string) =>
      [...root, 'entities', 'detail', typeId, entityId, 'effective-schema', attributeId] as const,
    revisions: (typeId: string, entityId: string) =>
      [...root, 'entities', 'detail', typeId, entityId, 'revisions'] as const,
    grid: (typeId: string, options?: unknown) => [...root, 'entities', 'grid', typeId, options ?? {}] as const,
    facets: (typeId: string, options?: unknown) => [...root, 'entities', 'facets', typeId, options ?? {}] as const,
  },

  query: {
    all: [...root, 'query'] as const,
    /** Every FQL query over one root type. */
    ofType: (type: string) => [...root, 'query', 'run', type] as const,
    run: (type: string, q: string, options?: unknown) =>
      [...root, 'query', 'run', type, q, options ?? {}] as const,
    validate: (type: string, q: string) => [...root, 'query', 'validate', type, q] as const,
  },

  relationships: {
    all: [...root, 'relationships'] as const,
    list: (options?: unknown) => [...root, 'relationships', 'list', options ?? {}] as const,
    detail: (id: string) => [...root, 'relationships', 'detail', id] as const,
  },

  relationshipDefinitions: {
    all: [...root, 'relationship-definitions'] as const,
    list: (options?: unknown) => [...root, 'relationship-definitions', 'list', options ?? {}] as const,
    detail: (id: string) => [...root, 'relationship-definitions', 'detail', id] as const,
  },

  dependencies: {
    all: [...root, 'dependencies'] as const,
    list: (options?: unknown) => [...root, 'dependencies', 'list', options ?? {}] as const,
    detail: (id: string) => [...root, 'dependencies', 'detail', id] as const,
  },

  savedViews: {
    all: [...root, 'saved-views'] as const,
    list: () => [...root, 'saved-views', 'list'] as const,
    detail: (id: string) => [...root, 'saved-views', 'detail', id] as const,
  },

  unitFamilies: {
    all: [...root, 'unit-families'] as const,
    list: () => [...root, 'unit-families', 'list'] as const,
    detail: (id: string) => [...root, 'unit-families', 'detail', id] as const,
  },

  activity: {
    all: [...root, 'activity'] as const,
    list: (options?: unknown) => [...root, 'activity', 'list', options ?? {}] as const,
  },

  revisions: {
    all: [...root, 'revisions'] as const,
    detail: (id: string) => [...root, 'revisions', 'detail', id] as const,
    diff: (id: string, toId: string) => [...root, 'revisions', 'detail', id, 'diff', toId] as const,
  },

  schema: {
    all: [...root, 'schema'] as const,
    export: () => [...root, 'schema', 'export'] as const,
    templates: () => [...root, 'schema', 'templates'] as const,
  },

  ops: {
    all: [...root, 'ops'] as const,
    features: () => [...root, 'ops', 'features'] as const,
  },
} as const
