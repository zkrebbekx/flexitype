package postgres

import (
	"context"
	"fmt"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// schemaVersionReader reads a tenant's persisted schema version from
// flexitype_schema_version — the counter a row trigger bumps on any type,
// attribute or relationship definition change (migration 000020). The GraphQL
// engine reads it to keep its per-replica schema cache correct: a definition
// change on one replica bumps the version, so every replica rebuilds its cached
// schema without needing to receive the (once-per-cluster) definition event
// (issue #192).
type schemaVersionReader struct {
	q db.QueryExecer
}

// NewSchemaVersionReader builds the persisted schema-version reader over the
// pool.
func NewSchemaVersionReader(q db.QueryExecer) application.SchemaVersionReader {
	return &schemaVersionReader{q: q}
}

// SchemaVersion returns the context tenant's current schema version, or 0 when
// the tenant has no definitions yet (no row).
func (r *schemaVersionReader) SchemaVersion(ctx context.Context) (uint64, error) {
	tenant := uow.TenantFromContext(ctx)
	var version int64
	err := r.q.GetContext(ctx, &version,
		bind(`SELECT version FROM flexitype_schema_version WHERE tenant_id = ?`), tenant.String())
	if err != nil {
		if isNoRows(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return uint64(version), nil
}
