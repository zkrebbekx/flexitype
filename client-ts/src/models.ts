/**
 * The API's data shapes.
 *
 * Every type here is an alias of a schema in `src/generated/openapi.ts`, which
 * `npm run generate` derives from `api/openapi.yaml`. Nothing in this file is
 * hand-written, so a shape cannot drift from the document that defines it —
 * `test/generated.test.ts` fails when the checked-in output is stale.
 *
 * The fields keep their wire names (`internal_name`, `page_info`). A rename
 * layer would have to be maintained by hand, which is the drift this package
 * exists to avoid, and it would make every API document harder to read
 * alongside the code.
 */
import type { components } from './generated/openapi.js'

type Schemas = components['schemas']

// --- schema definition ------------------------------------------------------

/** A runtime-defined entity type. */
export type TypeDefinition = Schemas['TypeDefinition']
export type CreateTypeDefinition = Schemas['CreateTypeDefinition']
/** The body of a type or relationship-definition rename. */
export type UpdateDisplay = Schemas['UpdateDisplay']
export type CloneType = Schemas['CloneType']
export type CloneResult = Schemas['CloneResult']

/** An attribute an entity of a type may hold. */
export type Attribute = Schemas['Attribute']
export type CreateAttribute = Schemas['CreateAttribute']
export type UpdateAttribute = Schemas['UpdateAttribute']

/** An attribute paired with the type that declares it. */
export type EffectiveAttribute = Schemas['EffectiveAttribute']

/** One of the soft data types an attribute declares. */
export type DataType = Schemas['DataType']

/** A value that carries its own data type. */
export type TypedValue = Schemas['TypedValue']
export type Constraint = Schemas['Constraint']
export type ConstraintKind = NonNullable<Constraint['kind']>
export type DefaultValue = Schemas['DefaultValue']
export type DynamicValue = Schemas['DynamicValue']
export type Computed = Schemas['Computed']
export type Rollup = Schemas['Rollup']

// --- values and entities ----------------------------------------------------

/** A stored attribute value. */
export type AttributeValue = Schemas['Value']
export type SetValue = Schemas['SetValue']
export type ValidateValueResult = Schemas['ValidateValueResult']
export type EntitySummary = Schemas['EntitySummary']
export type EffectiveSchema = Schemas['EffectiveSchema']
export type Completeness = Schemas['Completeness']
export type MissingAttribute = Schemas['MissingAttribute']
export type TypeCompleteness = Schemas['TypeCompleteness']
export type EntityScore = Schemas['EntityScore']
export type AppliedDefaults = Schemas['AppliedDefaults']
export type PurgeReport = Schemas['PurgeReport']
export type GridResult = Schemas['GridResult']
export type GridRow = Schemas['GridRow']
export type Facets = Schemas['Facets']
export type FacetBucket = Schemas['FacetBucket']
export type ImportReport = Schemas['ImportReport']
export type ImportError = Schemas['ImportError']

// --- dependencies -----------------------------------------------------------

export type Dependency = Schemas['Dependency']
export type CreateDependency = Schemas['CreateDependency']
export type UpdateDependency = Schemas['UpdateDependency']
export type DependencyCondition = Schemas['DependencyCondition']
export type DependencyEffect = Schemas['DependencyEffect']

// --- relationships ----------------------------------------------------------

export type RelationshipDefinition = Schemas['RelationshipDefinition']
export type CreateRelationshipDefinition = Schemas['CreateRelationshipDefinition']
export type Relationship = Schemas['Relationship']
export type Link = Schemas['Link']
export type EntityLink = Schemas['EntityLink']
export type AttributeSetIDs = Schemas['AttributeSetIDs']
export type RelationshipRequirement = Schemas['RelationshipRequirement']

// --- revisions --------------------------------------------------------------

export type Revision = Schemas['Revision']
export type RevisionDiff = Schemas['RevisionDiff']
export type RevisionChange = Schemas['RevisionChange']

// --- schema transfer --------------------------------------------------------

export type SchemaBundle = Schemas['SchemaBundle']
export type SchemaTemplateSummary = Schemas['SchemaTemplateSummary']
export type SchemaTemplate = Schemas['SchemaTemplate']
export type ImportKindCount = Schemas['ImportKindCount']

/** The per-kind created and skipped counts a schema import reports. */
export interface SchemaImportResult {
  types?: ImportKindCount
  attributes?: ImportKindCount
  relationship_definitions?: ImportKindCount
  dependencies?: ImportKindCount
}

// --- saved views and change sets --------------------------------------------

export type SavedView = Schemas['SavedView']
export type SavedViewInput = Schemas['SavedViewInput']
export type SavedViewPatch = Schemas['SavedViewPatch']
export type ChangeSet = Schemas['ChangeSet']
export type CreateChangeSet = Schemas['CreateChangeSet']
export type Mutation = Schemas['Mutation']

// --- units and duplicates ---------------------------------------------------

export type UnitFamily = Schemas['UnitFamily']
export type CreateUnitFamily = Schemas['CreateUnitFamily']
export type MatchRule = Schemas['MatchRule']
export type CreateMatchRule = Schemas['CreateMatchRule']
export type ScanResult = Schemas['ScanResult']
export type ScanCandidate = Schemas['ScanCandidate']

// --- activity and events ----------------------------------------------------

export type ActivityEntry = Schemas['ActivityEntry']
export type FeedEvent = Schemas['FeedEvent']
export type EventCursor = Schemas['EventCursor']
export type ParkedEnvelope = Schemas['ParkedEnvelope']
export type Subscription = Schemas['Subscription']
export type CreateSubscription = Schemas['CreateSubscription']
export type UpdateSubscription = Schemas['UpdateSubscription']
export type Delivery = Schemas['Delivery']
export type DeliveryStatus = NonNullable<Delivery['status']>

// --- provisioning -----------------------------------------------------------

export type Tenant = Schemas['Tenant']
export type ServiceAccount = Schemas['ServiceAccount']
export type AccountWithToken = Schemas['AccountWithToken']
export type EffectiveAccount = Schemas['EffectiveAccount']
export type Role = Schemas['Role']
export type FieldPermissions = Schemas['FieldPermissions']
/** A per-attribute permission level. An unlisted attribute stays accessible. */
export type FieldPermission = 'none' | 'read' | 'write'
/** A scope a role or an account may hold. */
export type Scope = 'read' | 'write' | 'admin'

// --- operations -------------------------------------------------------------

export type GraphQLResult = Schemas['GraphQLResult']

/** The optional capabilities a deployment runs. */
export interface Features {
  search: boolean
  activity: boolean
  search_index: boolean
  event_delivery: boolean
  media?: boolean
  graphql?: boolean
  [capability: string]: boolean | number | undefined
}

/**
 * The soft data types the service supports, in the order a form should offer
 * them. It equals the `DataType` enum in `api/openapi.yaml`, which
 * `domain/valueobjects/datatype.go` defines.
 */
export const DATA_TYPES = [
  'string',
  'integer',
  'float',
  'decimal',
  'quantity',
  'bool',
  'enum',
  'date',
  'time',
  'datetime',
  'url',
  'email',
  'json',
  'media',
] as const satisfies readonly DataType[]

/** The metadata a media value carries. The bytes live in object storage. */
export interface MediaValue {
  object_key: string
  mime: string
  size: number
  checksum?: string
  filename?: string
}

/**
 * A magnitude with a unit.
 *
 * `magnitude` is a decimal string, never a JS number: a price or a weight that
 * round-trips through a float loses digits. `base` is the magnitude converted
 * to the unit family's base unit; the service computes it and comparisons use
 * it, so a value in grams and a value in kilograms compare correctly.
 */
export interface QuantityValue {
  magnitude: string
  unit: string
  base?: number
}
