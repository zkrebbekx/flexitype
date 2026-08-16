/**
 * TanStack Query hooks over the services.
 *
 * Two decisions apply to every hook here.
 *
 * 1. QUERY-LEVEL RETRYING IS OFF. The transport already retries an idempotent
 *    read with exponential backoff and honours the service's `Retry-After`.
 *    Leaving TanStack's own retry on would multiply the two — up to nine
 *    requests for one render — so it is off by default. Pass `retry` in a
 *    hook's options to turn it back on.
 *
 * 2. AN ERROR IS NEVER AN EMPTY RESULT. Each list hook exposes `state`, which
 *    is one of `pending`, `error`, `empty` or `ready`. The console shipped a
 *    bug (#496) where a failed request rendered as "no results", and a screen
 *    that switches on `state` cannot repeat it.
 */
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
  type UseInfiniteQueryOptions,
  type UseInfiniteQueryResult,
  type UseMutationOptions,
  type UseMutationResult,
  type UseQueryOptions,
  type UseQueryResult,
} from '@tanstack/react-query'
import { useMemo } from 'react'
import type { FlexitypeClient } from '../client.js'
import type { FlexitypeError } from '../errors.js'
import type { Page } from '../pagination.js'
import type {
  Attribute,
  AttributeValue,
  Completeness,
  Dependency,
  EffectiveAttribute,
  EffectiveSchema,
  EntityLink,
  EntitySummary,
  Features,
  Revision,
  SavedView,
  TypeDefinition,
  UnitFamily,
} from '../models.js'
import type { ListAttributesOptions } from '../services/attributes.js'
import type { GridPage, GridOptions, ListEntitiesOptions } from '../services/entities.js'
import type { QueryOptions } from '../services/query.js'
import type { ListTypesOptions } from '../services/types.js'
import type { SetValue } from '../models.js'
import { ScopedValues } from '../softtype/scoped.js'
import { toFormDescriptor, type FormDescriptor, type FormDescriptorOptions } from '../softtype/schema.js'
import { flexitypeKeysFor } from './keys.js'
import { useFlexitypeClient } from './provider.js'

/**
 * The four states a data-bearing screen can be in.
 *
 * `empty` means the request succeeded and matched nothing. It is a different
 * fact from `error`, and a screen must say so.
 */
export type ResultState = 'pending' | 'error' | 'empty' | 'ready'

/** The extra fields every query hook in this package adds. */
export interface WithState {
  state: ResultState
  /** True only when the request succeeded and returned nothing. */
  isEmpty: boolean
}

/** The options a query hook accepts, minus the parts it sets itself. */
export type QueryHookOptions<TData> = Omit<UseQueryOptions<TData, FlexitypeError>, 'queryKey' | 'queryFn'>

function stateOf(result: { status: string; data: unknown }, isEmpty: (data: unknown) => boolean): ResultState {
  if (result.status === 'error') return 'error'
  if (result.status === 'pending') return 'pending'
  return isEmpty(result.data) ? 'empty' : 'ready'
}

function withState<TData>(
  result: UseQueryResult<TData, FlexitypeError>,
  isEmpty: (data: TData | undefined) => boolean,
): UseQueryResult<TData, FlexitypeError> & WithState {
  const state = stateOf(result, (data) => isEmpty(data as TData | undefined))
  return Object.assign(result, { state, isEmpty: state === 'empty' })
}

const emptyPage = (page: Page<unknown> | undefined): boolean => (page?.items.length ?? 0) === 0
const emptyList = (items: unknown[] | undefined): boolean => (items?.length ?? 0) === 0

/** Merges the caller's options over this package's defaults. */
function queryDefaults<TData>(options?: QueryHookOptions<TData>): QueryHookOptions<TData> {
  return { retry: false, ...options }
}

// --- types ------------------------------------------------------------------

/** One page of type definitions. */
export function useTypes(
  options: ListTypesOptions = {},
  queryOptions?: QueryHookOptions<Page<TypeDefinition>>,
): UseQueryResult<Page<TypeDefinition>, FlexitypeError> & WithState {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return withState(
    useQuery<Page<TypeDefinition>, FlexitypeError>({
      queryKey: flexitypeKeys.types.list(options),
      queryFn: ({ signal }) => client.types.list({ ...options, signal }),
      ...queryDefaults(queryOptions),
    }),
    emptyPage,
  )
}

/** One type definition. */
export function useType(
  id: string | undefined,
  queryOptions?: QueryHookOptions<TypeDefinition>,
): UseQueryResult<TypeDefinition, FlexitypeError> {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return useQuery<TypeDefinition, FlexitypeError>({
    queryKey: flexitypeKeys.types.detail(id ?? ''),
    queryFn: ({ signal }) => client.types.get(id as string, { signal }),
    enabled: id !== undefined && id !== '',
    ...queryDefaults(queryOptions),
  })
}

/**
 * A type's effective attributes: its own plus everything it inherits.
 *
 * This is the schema a dynamic form or grid renders from. `useFormDescriptor`
 * wraps it when the caller wants renderable fields rather than raw
 * definitions.
 */
export function useEffectiveAttributes(
  typeId: string | undefined,
  queryOptions?: QueryHookOptions<EffectiveAttribute[]>,
): UseQueryResult<EffectiveAttribute[], FlexitypeError> & WithState {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return withState(
    useQuery<EffectiveAttribute[], FlexitypeError>({
      queryKey: flexitypeKeys.types.effectiveAttributes(typeId ?? ''),
      queryFn: ({ signal }) => client.types.effectiveAttributes(typeId as string, { signal }),
      enabled: typeId !== undefined && typeId !== '',
      ...queryDefaults(queryOptions),
    }),
    emptyList,
  )
}

/**
 * A type's effective attributes, already turned into a form descriptor.
 *
 * The descriptor is memoized on the attributes, so a re-render does not
 * rebuild it.
 */
export function useFormDescriptor(
  typeId: string | undefined,
  descriptorOptions?: FormDescriptorOptions,
  queryOptions?: QueryHookOptions<EffectiveAttribute[]>,
): UseQueryResult<EffectiveAttribute[], FlexitypeError> & WithState & { descriptor: FormDescriptor | undefined } {
  const result = useEffectiveAttributes(typeId, queryOptions)
  const attributes = result.data
  const overrides = descriptorOptions?.overrides
  const optionLabel = descriptorOptions?.optionLabel
  const descriptor = useMemo(
    () => (attributes === undefined ? undefined : toFormDescriptor(attributes, { overrides, optionLabel })),
    [attributes, overrides, optionLabel],
  )
  return Object.assign(result, { descriptor })
}

/** A type's direct subtypes. */
export function useTypeChildren(
  typeId: string | undefined,
  queryOptions?: QueryHookOptions<TypeDefinition[]>,
): UseQueryResult<TypeDefinition[], FlexitypeError> & WithState {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return withState(
    useQuery<TypeDefinition[], FlexitypeError>({
      queryKey: flexitypeKeys.types.children(typeId ?? ''),
      queryFn: ({ signal }) => client.types.children(typeId as string, { signal }),
      enabled: typeId !== undefined && typeId !== '',
      ...queryDefaults(queryOptions),
    }),
    emptyList,
  )
}

// --- attributes -------------------------------------------------------------

/** One page of attribute definitions. */
export function useAttributes(
  options: ListAttributesOptions = {},
  queryOptions?: QueryHookOptions<Page<Attribute>>,
): UseQueryResult<Page<Attribute>, FlexitypeError> & WithState {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return withState(
    useQuery<Page<Attribute>, FlexitypeError>({
      queryKey: flexitypeKeys.attributes.list(options),
      queryFn: ({ signal }) => client.attributes.list({ ...options, signal }),
      ...queryDefaults(queryOptions),
    }),
    emptyPage,
  )
}

// --- entities ---------------------------------------------------------------

/** One page of a type's entities. */
export function useEntities(
  typeId: string | undefined,
  options: ListEntitiesOptions = {},
  queryOptions?: QueryHookOptions<Page<EntitySummary>>,
): UseQueryResult<Page<EntitySummary>, FlexitypeError> & WithState {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return withState(
    useQuery<Page<EntitySummary>, FlexitypeError>({
      queryKey: flexitypeKeys.entities.list(typeId ?? '', options),
      queryFn: ({ signal }) => client.entities.list(typeId as string, { ...options, signal }),
      enabled: typeId !== undefined && typeId !== '',
      ...queryDefaults(queryOptions),
    }),
    emptyPage,
  )
}

/**
 * An entity's values, grouped by attribute and scope.
 *
 * `data` is the raw rows, one per (attribute, locale, channel). `scoped` is
 * the same rows behind `ScopedValues`, which is how a screen should address
 * them: keying by attribute id alone shows one locale's value under another's
 * label.
 */
export function useEntityValues(
  typeId: string | undefined,
  entityId: string | undefined,
  options: { changeset?: string } = {},
  queryOptions?: QueryHookOptions<AttributeValue[]>,
): UseQueryResult<AttributeValue[], FlexitypeError> & WithState & { scoped: ScopedValues | undefined } {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  const result = withState(
    useQuery<AttributeValue[], FlexitypeError>({
      queryKey: flexitypeKeys.entities.values(typeId ?? '', entityId ?? '', options),
      queryFn: ({ signal }) =>
        client.entities.values(typeId as string, entityId as string, { ...options, signal }),
      enabled: typeId !== undefined && typeId !== '' && entityId !== undefined && entityId !== '',
      ...queryDefaults(queryOptions),
    }),
    emptyList,
  )
  const values = result.data
  const scoped = useMemo(() => (values === undefined ? undefined : new ScopedValues(values)), [values])
  return Object.assign(result, { scoped })
}

/** An entity's relationships, with each role resolved. */
export function useEntityRelationships(
  typeId: string | undefined,
  entityId: string | undefined,
  queryOptions?: QueryHookOptions<EntityLink[]>,
): UseQueryResult<EntityLink[], FlexitypeError> & WithState {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return withState(
    useQuery<EntityLink[], FlexitypeError>({
      queryKey: flexitypeKeys.entities.relationships(typeId ?? '', entityId ?? ''),
      queryFn: ({ signal }) => client.entities.relationships(typeId as string, entityId as string, { signal }),
      enabled: typeId !== undefined && typeId !== '' && entityId !== undefined && entityId !== '',
      ...queryDefaults(queryOptions),
    }),
    emptyList,
  )
}

/** How much of an entity's required schema is filled. */
export function useEntityCompleteness(
  typeId: string | undefined,
  entityId: string | undefined,
  queryOptions?: QueryHookOptions<Completeness>,
): UseQueryResult<Completeness, FlexitypeError> {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return useQuery<Completeness, FlexitypeError>({
    queryKey: flexitypeKeys.entities.completeness(typeId ?? '', entityId ?? ''),
    queryFn: ({ signal }) => client.entities.completeness(typeId as string, entityId as string, { signal }),
    enabled: typeId !== undefined && typeId !== '' && entityId !== undefined && entityId !== '',
    ...queryDefaults(queryOptions),
  })
}

/**
 * One attribute's dependency-resolved state for one entity: whether a
 * dependency makes it required, and which values it still allows. A cascading
 * picklist reads its options from here.
 */
export function useEffectiveSchema(
  typeId: string | undefined,
  entityId: string | undefined,
  attributeId: string | undefined,
  queryOptions?: QueryHookOptions<EffectiveSchema>,
): UseQueryResult<EffectiveSchema, FlexitypeError> {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return useQuery<EffectiveSchema, FlexitypeError>({
    queryKey: flexitypeKeys.entities.effectiveSchema(typeId ?? '', entityId ?? '', attributeId ?? ''),
    queryFn: ({ signal }) =>
      client.entities.effectiveSchema(typeId as string, entityId as string, attributeId as string, { signal }),
    enabled:
      typeId !== undefined &&
      typeId !== '' &&
      entityId !== undefined &&
      entityId !== '' &&
      attributeId !== undefined &&
      attributeId !== '',
    ...queryDefaults(queryOptions),
  })
}

/** An entity's saved revisions. */
export function useEntityRevisions(
  typeId: string | undefined,
  entityId: string | undefined,
  queryOptions?: QueryHookOptions<Revision[]>,
): UseQueryResult<Revision[], FlexitypeError> & WithState {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return withState(
    useQuery<Revision[], FlexitypeError>({
      queryKey: flexitypeKeys.entities.revisions(typeId ?? '', entityId ?? ''),
      queryFn: ({ signal }) => client.entities.revisions(typeId as string, entityId as string, { signal }),
      enabled: typeId !== undefined && typeId !== '' && entityId !== undefined && entityId !== '',
      ...queryDefaults(queryOptions),
    }),
    emptyList,
  )
}

/** A page of entities projected onto chosen attribute columns. */
export function useEntityGrid(
  typeId: string | undefined,
  options: GridOptions,
  queryOptions?: QueryHookOptions<GridPage>,
): UseQueryResult<GridPage, FlexitypeError> & WithState {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return withState(
    useQuery<GridPage, FlexitypeError>({
      queryKey: flexitypeKeys.entities.grid(typeId ?? '', options),
      queryFn: ({ signal }) => client.entities.grid(typeId as string, { ...options, signal }),
      enabled: typeId !== undefined && typeId !== '',
      ...queryDefaults(queryOptions),
    }),
    (grid) => (grid?.rows?.length ?? 0) === 0,
  )
}

// --- query (FQL) ------------------------------------------------------------

/** The options an infinite query hook accepts, minus the parts it sets itself. */
export type InfiniteHookOptions<TData> = Omit<
  UseInfiniteQueryOptions<
    Page<TData>,
    FlexitypeError,
    { pages: Page<TData>[]; pageParams: (string | undefined)[] },
    readonly unknown[],
    string | undefined
  >,
  'queryKey' | 'queryFn' | 'initialPageParam' | 'getNextPageParam'
>

/** One page of an FQL result set. */
export function useQueryEntities(
  type: string | undefined,
  q: string,
  options: QueryOptions = {},
  queryOptions?: QueryHookOptions<Page<EntitySummary>>,
): UseQueryResult<Page<EntitySummary>, FlexitypeError> & WithState {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return withState(
    useQuery<Page<EntitySummary>, FlexitypeError>({
      queryKey: flexitypeKeys.query.run(type ?? '', q, options),
      queryFn: ({ signal }) => client.query.run(type as string, q, { ...options, signal }),
      enabled: type !== undefined && type !== '',
      ...queryDefaults(queryOptions),
    }),
    emptyPage,
  )
}

/**
 * An FQL result set as an infinite query, wired to the keyset cursor.
 *
 * `getNextPageParam` reads `page_info.next_cursor`, so `fetchNextPage()` walks
 * the same forward-only pagination the core iterator does. A cursor the
 * service refuses answers 422, which arrives as a VALIDATION FlexitypeError
 * and is never retried.
 */
export function useInfiniteQueryEntities(
  type: string | undefined,
  q: string,
  options: QueryOptions = {},
  queryOptions?: InfiniteHookOptions<EntitySummary>,
): UseInfiniteQueryResult<{ pages: Page<EntitySummary>[]; pageParams: (string | undefined)[] }, FlexitypeError> &
  WithState {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  const result = useInfiniteQuery({
    queryKey: flexitypeKeys.query.run(type ?? '', q, options),
    queryFn: ({ pageParam, signal }) =>
      client.query.run(type as string, q, { ...options, cursor: pageParam as string | undefined, signal }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage: Page<EntitySummary>) =>
      lastPage.page_info?.has_next_page === true ? lastPage.page_info.next_cursor : undefined,
    enabled: type !== undefined && type !== '',
    retry: false,
    ...queryOptions,
  })
  const state = stateOf(result, (data) => {
    const pages = (data as { pages?: Page<EntitySummary>[] } | undefined)?.pages ?? []
    return pages.every((page) => page.items.length === 0)
  })
  return Object.assign(result, { state, isEmpty: state === 'empty' })
}

/** Every entity of one FQL page set, flattened. */
export function flattenPages<T>(data: { pages: Page<T>[] } | undefined): T[] {
  return (data?.pages ?? []).flatMap((page) => page.items)
}

// --- other reads ------------------------------------------------------------

/** The tenant's saved views. */
export function useSavedViews(
  queryOptions?: QueryHookOptions<SavedView[]>,
): UseQueryResult<SavedView[], FlexitypeError> & WithState {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return withState(
    useQuery<SavedView[], FlexitypeError>({
      queryKey: flexitypeKeys.savedViews.list(),
      queryFn: ({ signal }) => client.savedViews.list({ signal }),
      ...queryDefaults(queryOptions),
    }),
    emptyList,
  )
}

/** The tenant's unit families, which quantity fields draw their units from. */
export function useUnitFamilies(
  queryOptions?: QueryHookOptions<UnitFamily[]>,
): UseQueryResult<UnitFamily[], FlexitypeError> & WithState {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return withState(
    useQuery<UnitFamily[], FlexitypeError>({
      queryKey: flexitypeKeys.unitFamilies.list(),
      queryFn: ({ signal }) => client.unitFamilies.list({ signal }),
      ...queryDefaults(queryOptions),
    }),
    emptyList,
  )
}

/** The dependency rules, so a form can explain why a field is restricted. */
export function useDependencies(
  options: { sourceAttributeId?: string; targetAttributeId?: string } = {},
  queryOptions?: QueryHookOptions<Page<Dependency>>,
): UseQueryResult<Page<Dependency>, FlexitypeError> & WithState {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return withState(
    useQuery<Page<Dependency>, FlexitypeError>({
      queryKey: flexitypeKeys.dependencies.list(options),
      queryFn: ({ signal }) => client.dependencies.list({ ...options, signal }),
      ...queryDefaults(queryOptions),
    }),
    emptyPage,
  )
}

/** The deployment's enabled capabilities. */
export function useFeatures(
  queryOptions?: QueryHookOptions<Features>,
): UseQueryResult<Features, FlexitypeError> {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  return useQuery<Features, FlexitypeError>({
    queryKey: flexitypeKeys.ops.features(),
    queryFn: ({ signal }) => client.features({ signal }),
    staleTime: 5 * 60 * 1000,
    ...queryDefaults(queryOptions),
  })
}

// --- mutations --------------------------------------------------------------

/** The body `client.attributes.create` takes. */
export type CreateAttributeInput = Parameters<FlexitypeClient['attributes']['create']>[0]
/** The body `client.types.create` takes. */
export type CreateTypeInput = Parameters<FlexitypeClient['types']['create']>[0]

/**
 * The arguments TanStack passes to `onSuccess`.
 *
 * They are read from the installed version's own type rather than written out,
 * because the callback gained a parameter within the v5 line and this package's
 * peer range spans both shapes.
 */
type OnSuccessArgs<TData, TVariables> = Parameters<
  NonNullable<UseMutationOptions<TData, FlexitypeError, TVariables>['onSuccess']>
>

/** Calls the caller's own onSuccess, whatever arity the installed version uses. */
async function forwardOnSuccess<TData, TVariables>(
  options: MutationHookOptions<TData, TVariables> | undefined,
  args: OnSuccessArgs<TData, TVariables>,
): Promise<void> {
  await options?.onSuccess?.(...args)
}

/** The options a mutation hook accepts, minus the parts it sets itself. */
export type MutationHookOptions<TData, TVariables> = Omit<
  UseMutationOptions<TData, FlexitypeError, TVariables>,
  'mutationFn'
>

/**
 * Invalidates everything a value write can change: the entity's values, its
 * completeness, and any query or grid whose result set the new value may join
 * or leave.
 */
export async function invalidateAfterValueWrite(
  queryClient: QueryClient,
  input: { typeId?: string | undefined; entityId?: string | undefined; cacheKey: string },
): Promise<void> {
  // Scoped to ONE client, and cacheKey is REQUIRED to say which. Defaulting it
  // to '' built keys rooted at ['flexitype',''], which is a prefix of nothing
  // any hook registers — so a caller that omitted it invalidated nothing at
  // all, silently, where the same call used to work. A compile error is the
  // only honest answer.
  if (!input.cacheKey) {
    throw new Error('invalidateAfterValueWrite: cacheKey is required — pass client.cacheKey')
  }
  const flexitypeKeys = flexitypeKeysFor(input.cacheKey)
  const work: Promise<unknown>[] = [
    // A changed value can move the entity into or out of any result set, and
    // the client cannot evaluate FQL to know which, so every query, grid and
    // entity list is refetched. The audit log gains an entry either way.
    queryClient.invalidateQueries({ queryKey: flexitypeKeys.query.all }),
    queryClient.invalidateQueries({ queryKey: flexitypeKeys.values.all }),
    queryClient.invalidateQueries({ queryKey: flexitypeKeys.activity.all }),
  ]
  if (input.typeId !== undefined && input.typeId !== '' && input.entityId !== undefined && input.entityId !== '') {
    // The entity's own key covers its values, completeness, relationships and
    // revisions in one prefix.
    work.push(queryClient.invalidateQueries({ queryKey: flexitypeKeys.entities.detail(input.typeId, input.entityId) }))
    work.push(queryClient.invalidateQueries({ queryKey: flexitypeKeys.entities.lists(input.typeId) }))
    work.push(queryClient.invalidateQueries({ queryKey: flexitypeKeys.entities.grid(input.typeId) }))
  } else {
    work.push(queryClient.invalidateQueries({ queryKey: flexitypeKeys.entities.all }))
  }
  await Promise.all(work)
}

/**
 * Writes one attribute value.
 *
 * On success it invalidates the entity's values, every FQL result and every
 * grid, because a changed value can move an entity into or out of a result
 * set. Build the body with `scopedValueInput` when the attribute is
 * localizable or scopable.
 */
export function useSetValue(
  options?: MutationHookOptions<AttributeValue, SetValue>,
): UseMutationResult<AttributeValue, FlexitypeError, SetValue> {
  const client = useFlexitypeClient()
  const queryClient = useQueryClient()
  return useMutation<AttributeValue, FlexitypeError, SetValue>({
    mutationFn: (input: SetValue) => client.values.set(input),
    ...options,
    onSuccess: async (...args: OnSuccessArgs<AttributeValue, SetValue>) => {
      const variables = args[1]
      await invalidateAfterValueWrite(queryClient, {
        cacheKey: client.cacheKey,
        typeId: variables.type_definition_id,
        entityId: variables.entity_id,
      })
      await forwardOnSuccess(options, args)
    },
  })
}

/** Writes many values in one transaction. It invalidates as `useSetValue` does. */
export function useSetValues(
  options?: MutationHookOptions<AttributeValue[], SetValue[]>,
): UseMutationResult<AttributeValue[], FlexitypeError, SetValue[]> {
  const client = useFlexitypeClient()
  const queryClient = useQueryClient()
  return useMutation<AttributeValue[], FlexitypeError, SetValue[]>({
    mutationFn: (items: SetValue[]) => client.values.setBatch(items),
    ...options,
    onSuccess: async (...args: OnSuccessArgs<AttributeValue[], SetValue[]>) => {
      await invalidateAfterValueWrite(queryClient, { cacheKey: client.cacheKey })
      await forwardOnSuccess(options, args)
    },
  })
}

/** Archives one stored value. It invalidates as `useSetValue` does. */
export function useRemoveValue(
  options?: MutationHookOptions<AttributeValue, string>,
): UseMutationResult<AttributeValue, FlexitypeError, string> {
  const client = useFlexitypeClient()
  const queryClient = useQueryClient()
  return useMutation<AttributeValue, FlexitypeError, string>({
    mutationFn: (id: string) => client.values.remove(id),
    ...options,
    onSuccess: async (...args: OnSuccessArgs<AttributeValue, string>) => {
      await invalidateAfterValueWrite(queryClient, { cacheKey: client.cacheKey })
      await forwardOnSuccess(options, args)
    },
  })
}

/**
 * Creates an attribute.
 *
 * It invalidates every attribute query AND every type query, because a new
 * attribute changes the effective attributes of the declaring type and of
 * every type below it — which is what a dynamic form renders from.
 */
export function useCreateAttribute(
  options?: MutationHookOptions<Attribute, CreateAttributeInput>,
): UseMutationResult<Attribute, FlexitypeError, CreateAttributeInput> {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  const queryClient = useQueryClient()
  return useMutation<Attribute, FlexitypeError, CreateAttributeInput>({
    mutationFn: (input) => client.attributes.create(input),
    ...options,
    onSuccess: async (...args: OnSuccessArgs<Attribute, CreateAttributeInput>) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: flexitypeKeys.attributes.all }),
        queryClient.invalidateQueries({ queryKey: flexitypeKeys.types.all }),
      ])
      await forwardOnSuccess(options, args)
    },
  })
}

/** Creates a type definition. It invalidates every type query. */
export function useCreateType(
  options?: MutationHookOptions<TypeDefinition, CreateTypeInput>,
): UseMutationResult<TypeDefinition, FlexitypeError, CreateTypeInput> {
  const client = useFlexitypeClient()
  const flexitypeKeys = flexitypeKeysFor(client.cacheKey)
  const queryClient = useQueryClient()
  return useMutation<TypeDefinition, FlexitypeError, CreateTypeInput>({
    mutationFn: (input) => client.types.create(input),
    ...options,
    onSuccess: async (...args: OnSuccessArgs<TypeDefinition, CreateTypeInput>) => {
      await queryClient.invalidateQueries({ queryKey: flexitypeKeys.types.all })
      await forwardOnSuccess(options, args)
    },
  })
}

/** Soft-deletes an entity. It invalidates every entity and query result. */
export function useRemoveEntity(
  options?: MutationHookOptions<unknown, { typeId: string; entityId: string }>,
): UseMutationResult<unknown, FlexitypeError, { typeId: string; entityId: string }> {
  const client = useFlexitypeClient()
  const queryClient = useQueryClient()
  return useMutation<unknown, FlexitypeError, { typeId: string; entityId: string }>({
    mutationFn: ({ typeId, entityId }) => client.entities.remove(typeId, entityId),
    ...options,
    onSuccess: async (...args: OnSuccessArgs<unknown, { typeId: string; entityId: string }>) => {
      await invalidateAfterValueWrite(queryClient, { ...args[1], cacheKey: client.cacheKey })
      await forwardOnSuccess(options, args)
    },
  })
}
