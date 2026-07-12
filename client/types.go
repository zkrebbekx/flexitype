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
	Value                 string `json:"value"`
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
	ID              string          `json:"id"`
	TenantID        string          `json:"tenant_id"`
	Title           string          `json:"title"`
	State           string          `json:"state"`
	RequireApproval bool            `json:"require_approval"`
	Author          string          `json:"author,omitempty"`
	Mutations       json.RawMessage `json:"mutations,omitempty"`
	PublishAt       *time.Time      `json:"publish_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
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
type FeedEvent struct {
	FeedSeq    int64           `json:"feed_seq"`
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	TenantID   string          `json:"tenant_id,omitempty"`
	Actor      string          `json:"actor,omitempty"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload,omitempty"`
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
	Token    string   `json:"token,omitempty"`
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
