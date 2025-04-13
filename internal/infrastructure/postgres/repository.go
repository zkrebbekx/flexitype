package postgres

import (
	"github.com/jmoiron/sqlx"
	"github.com/zkrebbekx/flexitype/internal/domain/repository"
)

type postgresRepository struct {
	db *sqlx.DB
}

// NewPostgresRepository creates a new PostgreSQL repository
func NewPostgresRepository(db *sqlx.DB) repository.Repository {
	return &postgresRepository{db: db}
} 