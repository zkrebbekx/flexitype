/**
 * `@flexitype/client` — the framework-agnostic client for the flexitype REST
 * API.
 *
 * Nothing here imports React. The React hooks live behind a second entry
 * point, `@flexitype/client/react`, so a Vue or a plain-TypeScript caller
 * pulls none of it.
 *
 * ```ts
 * import { createClient } from '@flexitype/client'
 *
 * const client = createClient({ baseUrl: 'https://flexitype.internal', token })
 * for await (const entity of client.query.runAll('product', 'price > 10')) {
 *   console.log(entity.entity_id)
 * }
 * ```
 *
 * ONE CLIENT IS ONE TENANT. The service reads the tenant from the
 * authenticated service account, so the token decides which tenant every call
 * touches. An application that serves several tenants builds one client each.
 */

export { createClient, normalizeBaseUrl } from './client.js'
export type { ClientOptions, FlexitypeClient } from './client.js'

export {
  ERROR_CODES,
  FlexitypeError,
  hasErrorCode,
  isArchived,
  isConflict,
  isCursorConflict,
  isCursorExpired,
  isDependencyViolation,
  isFeatureDisabled,
  isFlexitypeError,
  isForbidden,
  isInternal,
  isNotFound,
  isRateLimited,
  isUnauthenticated,
  isValidation,
  parseRetryAfter,
} from './errors.js'
export type { ErrorCode, FlexitypeErrorInit } from './errors.js'

export { defaultRetryPolicy, isIdempotent, noRetryPolicy, retries, waitBeforeMs } from './retry.js'
export type { RetryPolicy } from './retry.js'

export { collect, CursorStack, paginate, paginatePages } from './pagination.js'
export type { ListOptions, Page, PageFetcher, PageInfo } from './pagination.js'

export { encodeQuery, Transport } from './http.js'
export type { FetchLike, QueryParams, QueryValue, RequestOptions, TransportConfig } from './http.js'

export * from './models.js'

// --- services ---------------------------------------------------------------

// The service classes are exported as types only. Build them through
// createClient; constructing one by hand would bypass the shared transport.
export type { TypesService, ListTypesOptions } from './services/types.js'
export type { AttributesService, ListAttributesOptions } from './services/attributes.js'
export type { ValuesService, ListValuesOptions } from './services/values.js'
export type { EntitiesService } from './services/entities.js'
export type { QueryService } from './services/query.js'
export type { SchemaService } from './services/schema.js'
export type { DependenciesService } from './services/dependencies.js'
export type { AdminService } from './services/admin.js'
export type { ChangeSetsService, EventsService, WebhooksService } from './services/events.js'
export type {
  ActivityService,
  MatchRulesService,
  RevisionsService,
  SavedViewsService,
  UnitFamiliesService,
} from './services/misc.js'
export type {
  RelationshipDefinitionsService,
  RelationshipsService,
} from './services/relationships.js'
export type {
  ExportOptions,
  FacetOptions,
  GridOptions,
  GridPage,
  ImportMapping,
  ListEntitiesOptions,
  RemoveEntityResult,
} from './services/entities.js'
export type { QueryOptions, ValidateQueryResult } from './services/query.js'
export type {
  ListRelationshipDefinitionsOptions,
  ListRelationshipsOptions,
} from './services/relationships.js'
export type { ListDependenciesOptions } from './services/dependencies.js'
export type { ListActivityOptions } from './services/misc.js'
export type { ListEventsOptions } from './services/events.js'
export type { CreateServiceAccountInput, ParkedFilter, UpsertRoleInput } from './services/admin.js'

// --- soft-type helpers ------------------------------------------------------

export {
  compareDecimalStrings,
  compareQuantities,
  compareValues,
  convertQuantity,
  DATE_FORMAT,
  formatDecimal,
  formatValue,
  fromWire,
  parseQuantity,
  TIME_FORMAT,
  toWire,
  withBase,
} from './softtype/values.js'
export type { FormatOptions } from './softtype/values.js'

export {
  BASE_SCOPE,
  groupScopedValues,
  isBaseScope,
  sameScope,
  ScopedValues,
  scopedValueInput,
  scopeKey,
  scopeOf,
} from './softtype/scoped.js'
export type { ScopeLookupOptions, ValueScope } from './softtype/scoped.js'

export {
  fieldKind,
  flattenConstraints,
  loadFormDescriptor,
  resolveEffectiveAttributes,
  sortEffectiveAttributes,
  toFormDescriptor,
  toFormField,
  unwrapTyped,
} from './softtype/schema.js'
export type {
  FieldConstraints,
  FieldKind,
  FieldOption,
  FormDescriptor,
  FormDescriptorOptions,
  FormField,
  FormGroup,
  ResolveOptions,
  TypeChainEntry,
} from './softtype/schema.js'
