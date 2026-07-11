// Typed client for the flexitype REST API. One fetch wrapper maps domain
// error codes onto ApiError so screens can speak plainly about failures.

export type DataType =
  | 'bool'
  | 'string'
  | 'integer'
  | 'float'
  | 'decimal'
  | 'date'
  | 'time'
  | 'datetime'
  | 'enum'
  | 'url'
  | 'email'
  | 'json'

export const DATA_TYPES: DataType[] = [
  'string',
  'integer',
  'float',
  'decimal',
  'bool',
  'enum',
  'date',
  'time',
  'datetime',
  'url',
  'email',
  'json',
]

export type ErrorCode =
  | 'VALIDATION'
  | 'NOT_FOUND'
  | 'CONFLICT'
  | 'ARCHIVED'
  | 'DEPENDENCY_VIOLATION'
  | 'UNAUTHENTICATED'
  | 'FORBIDDEN'
  | 'INTERNAL'

export class ApiError extends Error {
  code: ErrorCode
  details?: Record<string, unknown>
  status: number

  constructor(status: number, code: ErrorCode, message: string, details?: Record<string, unknown>) {
    super(message)
    this.status = status
    this.code = code
    this.details = details
  }
}

export interface PageInfo {
  has_next_page: boolean
  has_previous_page: boolean
  next_cursor?: string
  total_count: number
}

export interface Paged<T> {
  items: T[]
  page_info: PageInfo
}

export interface TypeDefinition {
  id: string
  tenant_id: string
  kind?: 'entity' | 'relationship_attributes'
  extends_id?: string
  internal_name: string
  display_name: string
  description?: string
  version: number
  created_at: string
  updated_at: string
  archived_at?: string
}

// TypedValue is the self-describing form used for constraint operands,
// condition operands and allowed values.
export interface TypedValue {
  type: DataType
  value: unknown
}

export interface Constraint {
  kind: 'min_length' | 'max_length' | 'min_value' | 'max_value' | 'pattern' | 'one_of'
  n?: number
  value?: TypedValue
  expr?: string
  values?: TypedValue[]
}

export interface DefaultValue {
  static?: TypedValue
  dynamic?: { kind: 'now' | 'today' | 'relative_time'; period?: string; amount?: number }
}

export interface AttributeDefinition {
  id: string
  tenant_id: string
  type_definition_id: string
  internal_name: string
  display_name: string
  description?: string
  data_type: DataType
  required: boolean
  multi_valued: boolean
  unique: boolean
  constraints: Constraint[]
  default_value?: DefaultValue
  version: number
  created_at: string
  updated_at: string
  archived_at?: string
}

export interface AttributeValue {
  id: string
  tenant_id: string
  type_definition_id: string
  attribute_definition_id: string
  entity_id: string
  value: unknown
  definition_version: number
  created_at: string
  updated_at: string
  archived_at?: string
}

export interface Condition {
  kind: 'equals' | 'in' | 'range' | 'pattern' | 'dynamic'
  value?: TypedValue
  values?: TypedValue[]
  min?: TypedValue
  max?: TypedValue
  pattern?: string
  dynamic?: { kind: 'now' | 'today' | 'relative_time'; period?: string; amount?: number }
  op?: 'before' | 'after' | 'on_or_before' | 'on_or_after'
}

export interface Effect {
  allowed_values?: TypedValue[]
  constraints?: Constraint[]
  required?: boolean
}

export interface Dependency {
  id: string
  tenant_id: string
  source_attribute_id: string
  target_attribute_id: string
  conditions: Condition[]
  effect: Effect
  description?: string
  version: number
  created_at: string
  updated_at: string
  archived_at?: string
}

export interface EffectiveSchema {
  attribute_definition_id: string
  entity_id: string
  required: boolean
  restricted: boolean
  allowed_values?: unknown[]
}

export type VersionPolicy = 'latest' | 'pinned'

export type RelationshipKind = 'directed' | 'symmetric'

export interface RelationshipDefinition {
  id: string
  tenant_id: string
  internal_name: string
  display_name: string
  description?: string
  kind: RelationshipKind
  parent_type_id: string
  child_type_id: string
  parent_label?: string
  child_label?: string
  attribute_set_id: string
  extends_id?: string
  parent_version_policy: VersionPolicy
  child_version_policy: VersionPolicy
  version: number
  created_at: string
  updated_at: string
  archived_at?: string
}

export interface Relationship {
  id: string
  tenant_id: string
  relationship_definition_id: string
  parent_entity_id: string
  child_entity_id: string
  parent_type_version?: number
  child_type_version?: number
  created_at: string
  updated_at: string
  archived_at?: string
}

export interface EntityLink {
  relationship: Relationship
  definition: RelationshipDefinition
  role: 'parent' | 'child'
}

export interface EntitySummary {
  entity_id: string
  type_definition_id: string
  value_count: number
  last_updated_at: string
}

export interface QueryResultRow {
  entity_id: string
  type_definition_id: string
  value_count: number
  last_updated_at: string
}

export interface EffectiveAttribute {
  attribute: AttributeDefinition
  declared_in: TypeDefinition
}

export interface ActivityEntry {
  id: string
  tenant_id: string
  actor: string
  entity: string
  entity_id: string
  action: 'created' | 'updated' | 'archived' | 'restored' | 'removed'
  before?: Record<string, unknown>
  after?: Record<string, unknown>
  occurred_at: string
}

export interface PageQuery {
  limit?: number
  cursor?: string
}

export interface WebhookSubscription {
  id: string
  name: string
  url: string
  event_types: string[]
  active: boolean
  created_at: string
  updated_at: string
}

export type DeliveryStatus = 'pending' | 'inflight' | 'delivered' | 'dead'

export interface WebhookDelivery {
  id: string
  subscription_id: string
  envelope_id: string
  event_type: string
  feed_seq: number
  status: DeliveryStatus
  attempts: number
  next_attempt_at: string
  last_error?: string
  response_code?: number
  created_at: string
  updated_at: string
}

export interface FeedEvent {
  seq: number
  envelope: {
    id: string
    type: string
    aggregate_type: string
    aggregate_id: string
    tenant_id: string
    actor: string
    occurred_at: string
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })

  if (res.status === 204) return undefined as T

  const text = await res.text()
  let parsed: unknown
  try {
    parsed = text ? JSON.parse(text) : undefined
  } catch {
    parsed = undefined
  }

  if (!res.ok) {
    const err = (parsed as { error?: { code?: string; message?: string; details?: Record<string, unknown> } })?.error
    throw new ApiError(
      res.status,
      (err?.code as ErrorCode) ?? 'INTERNAL',
      err?.message ?? `request failed with status ${res.status}`,
      err?.details,
    )
  }
  return parsed as T
}

function qs(params: object): string {
  const search = new URLSearchParams()
  for (const [k, v] of Object.entries(params) as [string, string | number | boolean | undefined][]) {
    if (v !== undefined && v !== '') search.set(k, String(v))
  }
  const s = search.toString()
  return s ? `?${s}` : ''
}

export const api = {
  // Type definitions
  listTypes: (q: PageQuery & { include_archived?: boolean } = {}) =>
    request<Paged<TypeDefinition>>('GET', `/type-definitions${qs(q)}`),
  getType: (id: string) => request<TypeDefinition>('GET', `/type-definitions/${id}`),
  createType: (input: { internal_name: string; display_name: string; description?: string; extends_id?: string }) =>
    request<TypeDefinition>('POST', '/type-definitions', input),
  effectiveAttributes: (typeId: string) =>
    request<{ items: EffectiveAttribute[] }>('GET', `/type-definitions/${typeId}/effective-attributes`),
  typeChildren: (typeId: string) =>
    request<{ items: TypeDefinition[] }>('GET', `/type-definitions/${typeId}/children`),
  updateType: (id: string, input: { display_name: string; description?: string }) =>
    request<TypeDefinition>('PATCH', `/type-definitions/${id}`, input),
  archiveType: (id: string) => request<TypeDefinition>('POST', `/type-definitions/${id}/archive`),
  restoreType: (id: string) => request<TypeDefinition>('POST', `/type-definitions/${id}/restore`),

  // Attributes
  listTypeAttributes: (typeId: string, q: PageQuery = {}) =>
    request<Paged<AttributeDefinition>>('GET', `/type-definitions/${typeId}/attributes${qs(q)}`),
  listAttributes: (q: PageQuery & { type_definition_id?: string; include_archived?: boolean } = {}) =>
    request<Paged<AttributeDefinition>>('GET', `/attributes${qs(q)}`),
  getAttribute: (id: string) => request<AttributeDefinition>('GET', `/attributes/${id}`),
  createAttribute: (input: {
    type_definition_id: string
    internal_name: string
    display_name: string
    description?: string
    data_type: DataType
    required?: boolean
    multi_valued?: boolean
    unique?: boolean
    constraints?: Constraint[]
    default_value?: DefaultValue
  }) => request<AttributeDefinition>('POST', '/attributes', input),
  updateAttribute: (
    id: string,
    input: {
      display_name: string
      description?: string
      required?: boolean
      multi_valued?: boolean
      unique?: boolean
      constraints?: Constraint[]
      default_value?: DefaultValue
    },
  ) => request<AttributeDefinition>('PATCH', `/attributes/${id}`, input),
  archiveAttribute: (id: string) => request<AttributeDefinition>('POST', `/attributes/${id}/archive`),
  validateAttributeValue: (id: string, value: unknown) =>
    request<{ valid: boolean }>('POST', `/attributes/${id}/validate-value`, { value }),
  restoreAttribute: (id: string) => request<AttributeDefinition>('POST', `/attributes/${id}/restore`),

  // Values & entities
  listEntities: (typeId: string, q: PageQuery & { include_descendants?: boolean } = {}) =>
    request<Paged<EntitySummary>>('GET', `/entities/${typeId}${qs(q)}`),
  listEntityValues: (typeId: string, entityId: string) =>
    request<{ items: AttributeValue[] }>('GET', `/entities/${typeId}/${encodeURIComponent(entityId)}/values`),
  effectiveSchema: (typeId: string, entityId: string, attributeId: string) =>
    request<EffectiveSchema>(
      'GET',
      `/entities/${typeId}/${encodeURIComponent(entityId)}/attributes/${attributeId}/effective-schema`,
    ),
  setValue: (input: { attribute_definition_id: string; entity_id: string; type_definition_id?: string; value: unknown }) =>
    request<AttributeValue>('POST', '/values', input),
  removeValue: (id: string) => request<AttributeValue>('DELETE', `/values/${id}`),
  listValues: (
    q: PageQuery & {
      type_definition_id?: string
      attribute_definition_id?: string
      entity_id?: string
      include_archived?: boolean
    } = {},
  ) => request<Paged<AttributeValue>>('GET', `/values${qs(q)}`),

  // Dependencies
  listDependencies: (
    q: PageQuery & { source_attribute_id?: string; target_attribute_id?: string; include_archived?: boolean } = {},
  ) => request<Paged<Dependency>>('GET', `/dependencies${qs(q)}`),
  createDependency: (input: {
    source_attribute_id: string
    target_attribute_id: string
    conditions: Condition[]
    effect: Effect
    description?: string
  }) => request<Dependency>('POST', '/dependencies', input),
  updateDependency: (
    id: string,
    input: { conditions: Condition[]; effect: Effect; description?: string },
  ) => request<Dependency>('PATCH', `/dependencies/${id}`, input),
  archiveDependency: (id: string) => request<Dependency>('DELETE', `/dependencies/${id}`),

  // Relationships
  listRelationshipDefinitions: (q: PageQuery & { type_definition_id?: string; include_archived?: boolean } = {}) =>
    request<Paged<RelationshipDefinition>>('GET', `/relationship-definitions${qs(q)}`),
  getRelationshipDefinition: (id: string) =>
    request<RelationshipDefinition>('GET', `/relationship-definitions/${id}`),
  createRelationshipDefinition: (input: {
    internal_name: string
    display_name: string
    description?: string
    kind?: RelationshipKind
    parent_type_id: string
    child_type_id: string
    parent_label?: string
    child_label?: string
    extends_id?: string
    parent_version_policy?: VersionPolicy
    child_version_policy?: VersionPolicy
  }) => request<RelationshipDefinition>('POST', '/relationship-definitions', input),
  updateRelationshipDefinition: (
    id: string,
    input: {
      display_name: string
      description?: string
      parent_label?: string
      child_label?: string
      parent_version_policy?: VersionPolicy
      child_version_policy?: VersionPolicy
    },
  ) => request<RelationshipDefinition>('PATCH', `/relationship-definitions/${id}`),
  archiveRelationshipDefinition: (id: string) =>
    request<RelationshipDefinition>('POST', `/relationship-definitions/${id}/archive`),
  relationshipAttributeSets: (id: string) =>
    request<{ attribute_set_ids: string[] }>('GET', `/relationship-definitions/${id}/attribute-sets`),
  link: (input: {
    relationship_definition_id: string
    parent_entity_id: string
    child_entity_id: string
    parent_type_version?: number
    child_type_version?: number
  }) => request<Relationship>('POST', '/relationships', input),
  unlink: (id: string) => request<Relationship>('DELETE', `/relationships/${id}`),
  listEntityRelationships: (typeId: string, entityId: string) =>
    request<{ items: EntityLink[] }>('GET', `/entities/${typeId}/${encodeURIComponent(entityId)}/relationships`),

  // Query (FQL)
  runQuery: (q: PageQuery & { type: string; q: string }) =>
    request<Paged<QueryResultRow>>('GET', `/query${qs(q)}`),
  validateQuery: (type: string, q: string) =>
    request<{ valid: boolean }>('POST', '/query/validate', { type, q }),

  // Activity
  listActivity: (q: PageQuery & { entity?: string; entity_id?: string; actor?: string } = {}) =>
    request<Paged<ActivityEntry>>('GET', `/activity${qs(q)}`),

  // Event delivery — webhook subscriptions
  listSubscriptions: () =>
    request<{ items: WebhookSubscription[] }>('GET', '/webhook-subscriptions'),
  createSubscription: (input: {
    name: string
    url: string
    secret?: string
    event_types?: string[]
    active?: boolean
  }) => request<WebhookSubscription>('POST', '/webhook-subscriptions', input),
  updateSubscription: (
    id: string,
    input: { url?: string; event_types?: string[]; active?: boolean; rotate_secret?: string },
  ) => request<WebhookSubscription>('PATCH', `/webhook-subscriptions/${id}`, input),
  deleteSubscription: (id: string) =>
    request<void>('DELETE', `/webhook-subscriptions/${id}`),
  listDeliveries: (id: string, q: PageQuery & { status?: DeliveryStatus } = {}) =>
    request<Paged<WebhookDelivery>>('GET', `/webhook-subscriptions/${id}/deliveries${qs(q)}`),
  redeliver: (deliveryId: string) =>
    request<{ status: string }>('POST', `/webhook-deliveries/${deliveryId}/redeliver`),

  // Event delivery — events feed
  listEvents: (q: { after?: number; types?: string; limit?: number } = {}) =>
    request<{ items: FeedEvent[]; next_cursor: number }>('GET', `/events${qs(q)}`),
}

// friendlyError renders an ApiError for inline display.
export function friendlyError(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.code) {
      case 'CONFLICT':
        return e.message
      case 'DEPENDENCY_VIOLATION':
        return `Blocked by an attribute dependency: ${e.message}`
      case 'ARCHIVED':
        return 'This item is archived; restore it before editing.'
      case 'NOT_FOUND':
        return 'Not found — it may have been removed in another session.'
      case 'UNAUTHENTICATED':
        return 'Authentication required.'
      case 'FORBIDDEN':
        return 'Your service account is missing the required scope.'
      default:
        return e.message
    }
  }
  return e instanceof Error ? e.message : 'Something went wrong.'
}
