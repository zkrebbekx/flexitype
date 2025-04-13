package postgres

import (
	"database/sqlx"

	"github.com/zkrebbekx/flexitype/internal/domain/repository"
)

type postgresRepository struct {
	db *sqlx.DB
}

func NewPostgresRepository(db *sqlx.DB) repository.Repository {
	return &postgresRepository{db: db}
} 