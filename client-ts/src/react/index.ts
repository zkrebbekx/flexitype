/**
 * `@flexitype/client/react` — React bindings over the core client.
 *
 * It depends on `react` and `@tanstack/react-query`, both peer dependencies.
 * The core entry point imports neither, so a Vue or a plain-TypeScript caller
 * pulls none of this.
 *
 * ```tsx
 * import { createClient } from '@flexitype/client'
 * import { FlexitypeProvider, useEffectiveAttributes } from '@flexitype/client/react'
 * ```
 */

export { FlexitypeProvider, useFlexitypeClient } from './provider.js'
export type { FlexitypeProviderProps } from './provider.js'

export { flexitypeKeysFor, FLEXITYPE_KEY_ROOT } from './keys.js'

export {
  flattenPages,
  invalidateAfterValueWrite,
  useAttributes,
  useCreateAttribute,
  useCreateType,
  useDependencies,
  useEffectiveAttributes,
  useEffectiveSchema,
  useEntities,
  useEntityCompleteness,
  useEntityGrid,
  useEntityRelationships,
  useEntityRevisions,
  useEntityValues,
  useFeatures,
  useFormDescriptor,
  useInfiniteQueryEntities,
  useQueryEntities,
  useRemoveEntity,
  useRemoveValue,
  useSavedViews,
  useSetValue,
  useSetValues,
  useType,
  useTypeChildren,
  useTypes,
  useUnitFamilies,
} from './hooks.js'
export type {
  InfiniteHookOptions,
  MutationHookOptions,
  QueryHookOptions,
  ResultState,
  WithState,
} from './hooks.js'
