package model

import (
	"time"

	"github.com/oklog/ulid"
)

type AttributeValue struct {
	ID                   ulid.ULID
	AttributeDefinitionID ulid.ULID
	Value                interface{}
	CreatedAt            time.Time
	UpdatedAt            time.Time
	ArchivedAt           *time.Time
} 