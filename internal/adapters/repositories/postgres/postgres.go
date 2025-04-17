package postgres

import (
	"context"
	"fmt"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

// PostgresRepository implements the TypeRepository and InstanceRepository interfaces using PostgreSQL
type PostgresRepository struct {
	db *sqlx.DB
}

// NewPostgresRepository creates a new PostgreSQL repository
func NewPostgresRepository(connectionString string) (*PostgresRepository, error) {
	db, err := sqlx.Connect("postgres", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	return &PostgresRepository{
		db: db,
	}, nil
}

// Close closes the database connection
func (r *PostgresRepository) Close() error {
	return r.db.Close()
}

// Initialize runs database migrations to set up the schema
func (r *PostgresRepository) Initialize(ctx context.Context) error {
	// Set up Goose for migrations
	goose.SetBaseFS(nil) // Use the OS filesystem

	// Apply migrations
	if err := goose.Up(r.db.DB, "db/migrations"); err != nil {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	return nil
}
