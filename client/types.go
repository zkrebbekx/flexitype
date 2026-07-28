package client

import (
	"encoding/json"
	"time"
)

// TypedValue is the self-describing form used for constraint operands,
// condition operands and default/allowed values: {"type":"decimal","value":"9.99"}.
type TypedValue struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

// Constraint is one attribute constraint. Only the fields relevant to Kind are
// set (n for lengths, value for min/max, expr for pattern, values for one_of,
// mime/max_size for media).
type Constraint struct {
	Kind    string       `json:"kind"`
	N       *int         `json:"n,omitempty"`
	Value   *TypedValue  `json:"value,omitempty"`
	Expr    string       `json:"expr,omitempty"`
	Values  []TypedValue `json:"values,omitempty"`
	MIME    []string     `json:"mime,omitempty"`
	MaxSize *int64       `json:"max_size,omitempty"`
}

// Dynamic is a dynamic (time-relative) value.
type Dynamic struct {
	Kind   string `json:"kind"` // now | today | relative_time
	Period string `json:"period,omitempty"`
	Amount int    `json:"amount,omitempty"`
}

// DefaultValue is an attribute's default, static or dynamic.
type DefaultValue struct {
	Static  *TypedValue `json:"static,omitempty"`
	Dynamic *Dynamic    `json:"dynamic,omitempty"`
}

// ComputedSpec makes an attribute derived rather than stored.
type ComputedSpec struct {
	Kind    string `json:"kind"` // formula | rollup
	Formula string `json:"formula,omitempty"`
}

// TypeDefinition is a soft type.
type TypeDefinition struct {
	ID           string     `json:"id"`
	TenantID     string     `json:"tenant_id"`
	Kind         string     `json:"kind,omitempty"`
	ExtendsID    string     `json:"extends_id,omitempty"`
	InternalName string     `json:"internal_name"`
	DisplayName  string     `json:"display_name"`
	Description  string     `json:"description,omitempty"`
	Version      int        `json:"version"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ArchivedAt   *time.Time `json:"archived_at,omitempty"`
}

// AttributeDefinition is a typed, constrained attribute on a type.
type AttributeDefinition struct {
	ID               string        `json:"id"`
	TenantID         string        `json:"tenant_id"`
	TypeDefinitionID string        `json:"type_definition_id"`
	InternalName     string        `json:"internal_name"`
	DisplayName      string        `json:"display_name"`
	Description      string        `json:"description,omitempty"`
	DataType         string        `json:"data_type"`
	Required         bool          `json:"required"`
	MultiValued      bool          `json:"multi_valued"`
	Unique           bool          `json:"unique"`
	Localizable      bool          `json:"localizable,omitempty"`
	Scopable         bool          `json:"scopable,omitempty"`
	UnitFamilyID     string        `json:"unit_family_id,omitempty"`
	DisplayUnit      string        `json:"display_unit,omitempty"`
	Computed         *ComputedSpec `json:"computed,omitempty"`
	Constraints      []Constraint  `json:"constraints"`
	DefaultValue     *DefaultValue `json:"default_value,omitempty"`
	Group            string        `json:"group,omitempty"`
	SortOrder        int           `json:"sort_order"`
	HelpText         string        `json:"help_text,omitempty"`
	Version          int           `json:"version"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	ArchivedAt       *time.Time    `json:"archived_at,omitempty"`
}

// AttributeValue is one stored value. Value is the raw JSON payload (its shape
// follows the attribute's data type).
type AttributeValue struct {
	ID                    string          `json:"id"`
	TenantID              string          `json:"tenant_id"`
	TypeDefinitionID      string          `json:"type_definition_id"`
	AttributeDefinitionID string          `json:"attribute_definition_id"`
	EntityID              string          `json:"entity_id"`
	Locale                string          `json:"locale,omitempty"`
	Channel               string          `json:"channel,omitempty"`
	Value                 json.RawMessage `json:"value"`
	DefinitionVersion     int             `json:"definition_version"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
	ArchivedAt            *time.Time      `json:"archived_at,omitempty"`
}

// EffectiveAttribute pairs an attribute with the type that declares it.
type EffectiveAttribute struct {
	Attribute  AttributeDefinition `json:"attribute"`
	DeclaredIn TypeDefinition      `json:"declared_in"`
}

// EntitySummary is one entity in a list or query result.
type EntitySummary struct {
	EntityID         string    `json:"entity_id"`
	TypeDefinitionID string    `json:"type_definition_id"`
	ValueCount       int       `json:"value_count"`
	LastUpdatedAt    time.Time `json:"last_updated_at"`
}

// Condition is one dependency condition.
type Condition struct {
	Kind    string       `json:"kind"`
	Value   *TypedValue  `json:"value,omitempty"`
	Values  []TypedValue `json:"values,omitempty"`
	Min     *TypedValue  `json:"min,omitempty"`
	Max     *TypedValue  `json:"max,omitempty"`
	Pattern string       `json:"pattern,omitempty"`
	Dynamic *Dynamic     `json:"dynamic,omitempty"`
	Op      string       `json:"op,omitempty"`
}

// Effect is a dependency's effect on its target.
type Effect struct {
	AllowedValues []TypedValue `json:"allowed_values,omitempty"`
	Constraints   []Constraint `json:"constraints,omitempty"`
	Required      *bool        `json:"required,omitempty"`
}

// Dependency is a conditional-validation / cascading-picklist rule.
type Dependency struct {
	ID                string      `json:"id"`
	TenantID          string      `json:"tenant_id"`
	SourceAttributeID string      `json:"source_attribute_id"`
	TargetAttributeID string      `json:"target_attribute_id"`
	Conditions        []Condition `json:"conditions"`
	Effect            Effect      `json:"effect"`
	Description       string      `json:"description,omitempty"`
	Version           int         `json:"version"`
	CreatedAt         time.Time   `json:"created_at"`
	UpdatedAt         time.Time   `json:"updated_at"`
	ArchivedAt        *time.Time  `json:"archived_at,omitempty"`
}

// EffectiveSchema is an attribute's dependency-resolved state for one entity.
type EffectiveSchema struct {
	AttributeDefinitionID string            `json:"attribute_definition_id"`
	EntityID              string            `json:"entity_id"`
	Required              bool              `json:"required"`
	Restricted            bool              `json:"restricted"`
	AllowedValues         []json.RawMessage `json:"allowed_values,omitempty"`
}

// RelationshipDefinition is a user-defined relationship type.
type RelationshipDefinition struct {
	ID                  string     `json:"id"`
	TenantID            string     `json:"tenant_id"`
	InternalName        string     `json:"internal_name"`
	DisplayName         string     `json:"display_name"`
	Description         string     `json:"description,omitempty"`
	Kind                string     `json:"kind"`
	ParentTypeID        string     `json:"parent_type_id"`
	ChildTypeID         string     `json:"child_type_id"`
	ParentLabel         string     `json:"parent_label,omitempty"`
	ChildLabel          string     `json:"child_label,omitempty"`
	AttributeSetID      string     `json:"attribute_set_id"`
	ExtendsID           string     `json:"extends_id,omitempty"`
	ParentVersionPolicy string     `json:"parent_version_policy"`
	ChildVersionPolicy  string     `json:"child_version_policy"`
	MinChildren         *int       `json:"min_children,omitempty"`
	MaxChildren         *int       `json:"max_children,omitempty"`
	MinParents          *int       `json:"min_parents,omitempty"`
	MaxParents          *int       `json:"max_parents,omitempty"`
	Version             int        `json:"version"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	ArchivedAt          *time.Time `json:"archived_at,omitempty"`
}

// Relationship is one live link between two entities.
type Relationship struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	DefinitionID   string     `json:"relationship_definition_id"`
	ParentEntityID string     `json:"parent_entity_id"`
	ChildEntityID  string     `json:"child_entity_id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty"`
}

// EntityLink is a relationship with its definition and the queried entity's
// role resolved.
type EntityLink struct {
	Relationship Relationship           `json:"relationship"`
	Definition   RelationshipDefinition `json:"definition"`
	Role         string                 `json:"role"`
}

// RelationshipRequirement reports an unmet cardinality minimum for an entity.
type RelationshipRequirement struct {
	DefinitionID string `json:"definition_id"`
	Side         string `json:"side"`
	Required     int    `json:"required"`
	Current      int    `json:"current"`
}

// MissingAttribute names a required attribute an entity has not filled.
type MissingAttribute struct {
	AttributeDefinitionID string `json:"attribute_definition_id"`
	InternalName          string `json:"internal_name"`
	DisplayName           string `json:"display_name"`
	Group                 string `json:"group,omitempty"`
}

// Completeness scores one entity against its effective required schema.
type Completeness struct {
	EntityID         string             `json:"entity_id"`
	TypeDefinitionID string             `json:"type_definition_id"`
	Required         int                `json:"required"`
	Filled           int                `json:"filled"`
	Score            float64            `json:"score"`
	Missing          []MissingAttribute `json:"missing"`
}

// EntityScore is one entity's completeness inside a type aggregate.
type EntityScore struct {
	EntityID string  `json:"entity_id"`
	Score    float64 `json:"score"`
	Filled   int     `json:"filled"`
	Required int     `json:"required"`
}

// TypeCompleteness aggregates completeness across a type's entities.
type TypeCompleteness struct {
	TypeDefinitionID string        `json:"type_definition_id"`
	Count            int           `json:"count"`
	Scored           int           `json:"scored"`
	Truncated        bool          `json:"truncated"`
	AverageScore     float64       `json:"average_score"`
	Complete         int           `json:"complete"`
	Incomplete       int           `json:"incomplete"`
	Entities         []EntityScore `json:"entities"`
}

// RevisionValue is one attribute's value captured in a revision.
type RevisionValue struct {
	AttributeDefinitionID string `json:"attribute_definition_id"`
	InternalName          string `json:"internal_name"`
	DisplayName           string `json:"display_name"`
	DataType              string `json:"data_type"`
	// Locale and Channel are the value's scope. Without them a localized
	// entity came back as N rows with identical InternalName and no way to
	// tell them apart.
	Locale  string `json:"locale,omitempty"`
	Channel string `json:"channel,omitempty"`
	// Value is the DISPLAY form: lossy for structured types, and what the
	// console and the diff show. A quantity renders as "10 kg", a media value
	// as its filename.
	Value string `json:"value"`
	// Typed is the self-describing round-trippable form, added on the server
	// precisely because Value is lossy. Read this to reconstruct a value.
	Typed json.RawMessage `json:"typed,omitempty"`
}

// EntityRevision is an immutable point-in-time snapshot of an entity's values.
type EntityRevision struct {
	ID               string          `json:"id"`
	TypeDefinitionID string          `json:"type_definition_id"`
	EntityID         string          `json:"entity_id"`
	Seq              int             `json:"seq"`
	Label            string          `json:"label,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	Values           []RevisionValue `json:"values"`
}

// UnitFamily is a set of units sharing a base with per-unit conversion factors.
type UnitFamily struct {
	ID       string             `json:"id"`
	TenantID string             `json:"tenant_id"`
	Name     string             `json:"name"`
	BaseUnit string             `json:"base_unit"`
	Units    map[string]float64 `json:"units"`
}

// SavedView persists a type + query + grid columns under a name.
type SavedView struct {
	ID       string   `json:"id"`
	TenantID string   `json:"tenant_id"`
	Name     string   `json:"name"`
	RootType string   `json:"root_type"`
	Query    string   `json:"query"`
	Columns  []string `json:"columns,omitempty"`
	// Sort is the view's ordering clause. It is part of what makes a saved
	// view reproducible, and had no field to land in.
	Sort      string    `json:"sort,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Version increments on every write. Send it back in SavedViewPatch to
	// compare-and-swap, so a concurrent edit answers 409 instead of being
	// silently overwritten.
	Version int `json:"version"`
}

// MatchRule is a per-type duplicate-detection rule.
type MatchRule struct {
	ID                    string    `json:"id"`
	TenantID              string    `json:"tenant_id"`
	TypeDefinitionID      string    `json:"type_definition_id"`
	AttributeDefinitionID string    `json:"attribute_definition_id"`
	Strategy              string    `json:"strategy"`
	Threshold             float64   `json:"threshold"`
	CreatedAt             time.Time `json:"created_at"`
}

// MatchCandidate is a scored pair of possibly-duplicate entities.
type MatchCandidate struct {
	EntityA string  `json:"entity_a"`
	EntityB string  `json:"entity_b"`
	ValueA  string  `json:"value_a"`
	ValueB  string  `json:"value_b"`
	Score   float64 `json:"score"`
}

// MatchScan is a rule's report of candidate duplicate pairs.
type MatchScan struct {
	RuleID     string           `json:"rule_id"`
	Strategy   string           `json:"strategy"`
	Candidates []MatchCandidate `json:"candidates"`
	Truncated  bool             `json:"truncated"`
}

// ChangeSet is a staged batch of value edits moving through review.
type ChangeSet struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	// Name is the change-set's label. The server emits "name"; this field
	// was declared as "title", so it never populated and every change-set
	// read back with a blank label.
	Name            string          `json:"name"`
	State           string          `json:"state"`
	RequireApproval bool            `json:"require_approval"`
	Author          string          `json:"author,omitempty"`
	Approver        string          `json:"approver,omitempty"`
	Mutations       json.RawMessage `json:"mutations,omitempty"`
	PublishAt       *time.Time      `json:"publish_at,omitempty"`
	PublishedAt     *time.Time      `json:"published_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`

	// Title mirrors Name.
	//
	// Deprecated: the server has no "title" field and never sent one, so
	// this was always empty. It is populated from "name" so that code
	// written against it keeps compiling and now reads a real value. Use
	// Name; Title is removed in the next major version of this module.
	Title string `json:"-"`
}

// UnmarshalJSON decodes a change-set and mirrors Name onto the deprecated
// Title field.
func (c *ChangeSet) UnmarshalJSON(b []byte) error {
	type alias ChangeSet // no methods, so this does not recurse
	var out alias
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	out.Title = out.Name
	*c = ChangeSet(out)
	return nil
}

// GridResult is a faceted grid page: chosen attribute values per entity.
type GridResult struct {
	Columns  []string  `json:"columns"`
	Rows     []GridRow `json:"rows"`
	PageInfo PageInfo  `json:"page_info"`
}

// GridRow is one entity's projected column values.
type GridRow struct {
	EntityID string            `json:"entity_id"`
	Values   map[string]string `json:"values"`
}

// ImportError points at one rejected import cell or row.
type ImportError struct {
	Row       int    `json:"row"`
	Column    string `json:"column,omitempty"`
	Attribute string `json:"attribute,omitempty"`
	Reason    string `json:"reason"`
}

// ImportReport summarizes an import run.
type ImportReport struct {
	RowsTotal   int           `json:"rows_total"`
	RowsValid   int           `json:"rows_valid"`
	RowsWritten int           `json:"rows_written"`
	DryRun      bool          `json:"dry_run"`
	Mode        string        `json:"mode"`
	Errors      []ImportError `json:"errors"`
}

// FacetBucket is one distinct value and its count within a faceted column.
type FacetBucket struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Facets maps each requested attribute to its value buckets over the current
// result set.
type Facets struct {
	Facets map[string][]FacetBucket `json:"facets"`
	// Truncated reports that at least one attribute has more distinct values
	// than the buckets returned. Without it a partial bucket list is
	// indistinguishable from a complete one, so a filter sidebar shows
	// "3 materials" where there are 300.
	Truncated bool `json:"truncated,omitempty"`
}

// ExportResult is a parsed CSV export: a header and data rows.
type ExportResult struct {
	Columns []string
	Rows    [][]string
}

// Features reports the deployment's enabled capabilities.
type Features struct {
	Search        bool `json:"search"`
	Activity      bool `json:"activity"`
	SearchIndex   bool `json:"search_index"`
	EventDelivery bool `json:"event_delivery"`

	// MaxImportBytes and MaxMediaBytes are the deployment's upload ceilings.
	// They were compile-time constants the client could not discover, so a
	// bulk CSV load was sized against a guess and failed part-way through.
	// Chunk against these rather than assuming the defaults.
	MaxImportBytes int64 `json:"max_import_bytes"`
	MaxMediaBytes  int64 `json:"max_media_bytes"`
}

// KindCount tallies created vs skipped objects of one kind on import.
type KindCount struct {
	Created int `json:"created"`
	Skipped int `json:"skipped"`
}

// SchemaImportResult reports what a schema import created versus skipped.
type SchemaImportResult struct {
	Types                   KindCount `json:"types"`
	Attributes              KindCount `json:"attributes"`
	RelationshipDefinitions KindCount `json:"relationship_definitions"`
	Dependencies            KindCount `json:"dependencies"`
}

// SchemaTemplate is a curated starter schema's metadata.
type SchemaTemplate struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Bundle is the template's schema payload, present when a single
	// template is fetched. Without it, Template returned exactly what
	// Templates already lists — its godoc promised "including its bundle",
	// which is the only reason to fetch one by name.
	Bundle json.RawMessage `json:"bundle,omitempty"`
}

// Mutation is one staged value change inside a change-set.
type Mutation struct {
	Kind                  string          `json:"kind"` // set | remove
	AttributeDefinitionID string          `json:"attribute_definition_id"`
	EntityID              string          `json:"entity_id"`
	TypeDefinitionID      string          `json:"type_definition_id,omitempty"`
	Locale                string          `json:"locale,omitempty"`
	Channel               string          `json:"channel,omitempty"`
	Value                 json.RawMessage `json:"value,omitempty"`
}

// WebhookSubscription is a managed delivery endpoint.
type WebhookSubscription struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	EventTypes []string  `json:"event_types"`
	Active     bool      `json:"active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// WebhookDelivery is one delivery attempt record.
type WebhookDelivery struct {
	ID             string    `json:"id"`
	SubscriptionID string    `json:"subscription_id"`
	EnvelopeID     string    `json:"envelope_id"`
	EventType      string    `json:"event_type"`
	FeedSeq        int64     `json:"feed_seq"`
	Status         string    `json:"status"`
	Attempts       int       `json:"attempts"`
	NextAttemptAt  time.Time `json:"next_attempt_at"`
	LastError      string    `json:"last_error,omitempty"`
	ResponseCode   int       `json:"response_code,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// FeedEvent is one entry in the events feed.
//
// The wire shape is NESTED: the server sends {"seq":N,"envelope":{...}}. This
// type was flat (feed_seq, id, type, …) and no key overlapped, so decoding
// produced the right NUMBER of events with every field zero and no error — a
// consumer dispatching on Type saw "" for ever. UnmarshalJSON maps the nested
// form onto these fields so callers keep one flat struct.
type FeedEvent struct {
	// Seq is the feed position. Pass the last one you processed as After.
	Seq           int64           `json:"seq"`
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	AggregateType string          `json:"aggregate_type,omitempty"`
	AggregateID   string          `json:"aggregate_id,omitempty"`
	TenantID      string          `json:"tenant_id,omitempty"`
	Actor         string          `json:"actor,omitempty"`
	OccurredAt    time.Time       `json:"occurred_at"`
	RecordedAt    time.Time       `json:"recorded_at,omitempty"`
	SchemaVersion int             `json:"schema_version,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

// UnmarshalJSON decodes the server's {"seq":…,"envelope":{…}} shape.
func (e *FeedEvent) UnmarshalJSON(data []byte) error {
	var wire struct {
		Seq      int64 `json:"seq"`
		Envelope struct {
			ID            string          `json:"id"`
			Type          string          `json:"type"`
			AggregateType string          `json:"aggregate_type"`
			AggregateID   string          `json:"aggregate_id"`
			TenantID      string          `json:"tenant_id"`
			Actor         string          `json:"actor"`
			OccurredAt    time.Time       `json:"occurred_at"`
			RecordedAt    time.Time       `json:"recorded_at"`
			SchemaVersion int             `json:"schema_version"`
			Payload       json.RawMessage `json:"payload"`
		} `json:"envelope"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	e.Seq = wire.Seq
	e.ID = wire.Envelope.ID
	e.Type = wire.Envelope.Type
	e.AggregateType = wire.Envelope.AggregateType
	e.AggregateID = wire.Envelope.AggregateID
	e.TenantID = wire.Envelope.TenantID
	e.Actor = wire.Envelope.Actor
	e.OccurredAt = wire.Envelope.OccurredAt
	e.RecordedAt = wire.Envelope.RecordedAt
	e.SchemaVersion = wire.Envelope.SchemaVersion
	e.Payload = wire.Envelope.Payload
	return nil
}

// Tenant is a provisioning tenant.
type Tenant struct {
	Name      string    `json:"name"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// ServiceAccount is a provisioned credential. Token is set only at creation
// and rotation; it is never returned again.
type ServiceAccount struct {
	ID       string   `json:"id"`
	TenantID string   `json:"tenant_id,omitempty"`
	Name     string   `json:"name"`
	Scopes   []string `json:"scopes"`
	// Roles the account holds. Its effective scopes are the union of Scopes
	// and every role's scopes.
	Roles []string `json:"roles,omitempty"`
	// FieldPermissions are this account's own overrides, which win over any
	// level a role grants. Keys are attribute internal names; values are
	// none, read or write.
	FieldPermissions map[string]string `json:"field_permissions,omitempty"`
	Token            string            `json:"token,omitempty"`
}

// EffectiveAccount is an account's permissions after its roles are merged in.
type EffectiveAccount struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id,omitempty"`
	Name     string `json:"name"`
	Active   bool   `json:"active"`
	// Roles are the names the account holds, as stored.
	Roles []string `json:"roles,omitempty"`
	// Scopes are the account's own scopes unioned with its roles'.
	Scopes []string `json:"scopes"`
	// FieldPermissions are the merged per-attribute levels.
	FieldPermissions map[string]string `json:"field_permissions,omitempty"`
	// UnresolvedRoles names roles the account holds that no longer exist.
	// A principal carrying one is denied every attribute.
	UnresolvedRoles []string `json:"unresolved_roles,omitempty"`
}

// Role is a named permission set that accounts in the same tenant hold.
type Role struct {
	ID          string   `json:"id"`
	TenantID    string   `json:"tenant_id,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Scopes      []string `json:"scopes"`
	// FieldPermissions maps an attribute internal name to none, read or
	// write. An attribute that is not listed stays fully accessible.
	FieldPermissions map[string]string `json:"field_permissions,omitempty"`
}

// ActivityEntry is one audit-log record.
type ActivityEntry struct {
	ID         string          `json:"id"`
	TenantID   string          `json:"tenant_id"`
	Actor      string          `json:"actor"`
	Entity     string          `json:"entity"`
	EntityID   string          `json:"entity_id"`
	Action     string          `json:"action"`
	Before     json.RawMessage `json:"before,omitempty"`
	After      json.RawMessage `json:"after,omitempty"`
	OccurredAt time.Time       `json:"occurred_at"`
}
