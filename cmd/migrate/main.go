package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

func main() {
	// Define the flags
	dbURL := flag.String("db", "", "Database connection URL (required)")
	dir := flag.String("dir", "db/migrations", "Directory with migration files")
	command := flag.String("cmd", "up", "Goose command (up, down, status, create, etc.)")
	flag.Parse()

	// Verify required flags
	if *dbURL == "" {
		fmt.Println("Error: Database connection URL is required")
		fmt.Println("Usage: go run cmd/migrate/main.go -db <db-url> [-dir <migrations-dir>] [-cmd <goose-command>]")
		fmt.Println("Example: go run cmd/migrate/main.go -db postgres://user:password@localhost:5432/flexitype -cmd up")
		os.Exit(1)
	}

	// Connect to the database
	db, err := sql.Open("postgres", *dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Test the connection
	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	fmt.Println("Connected to database successfully")

	// Set up Goose
	goose.SetBaseFS(nil) // Use the OS filesystem, not an embedded one

	// Run the specified Goose command
	switch *command {
	case "up":
		fmt.Println("Applying all migrations...")
		if err := goose.Up(db, *dir); err != nil {
			log.Fatalf("Failed to apply migrations: %v", err)
		}
		fmt.Println("Successfully applied all migrations")

	case "up-by-one":
		fmt.Println("Applying next migration...")
		if err := goose.UpByOne(db, *dir); err != nil {
			log.Fatalf("Failed to apply migration: %v", err)
		}
		fmt.Println("Successfully applied migration")

	case "down":
		fmt.Println("Rolling back the latest migration...")
		if err := goose.Down(db, *dir); err != nil {
			log.Fatalf("Failed to rollback migration: %v", err)
		}
		fmt.Println("Successfully rolled back migration")

	case "reset":
		fmt.Println("Rolling back all migrations...")
		if err := goose.Reset(db, *dir); err != nil {
			log.Fatalf("Failed to reset migrations: %v", err)
		}
		fmt.Println("Successfully reset all migrations")

	case "status":
		fmt.Println("Getting migration status...")
		if err := goose.Status(db, *dir); err != nil {
			log.Fatalf("Failed to get migration status: %v", err)
		}

	case "version":
		fmt.Println("Getting migration version...")
		if err := goose.Version(db, *dir); err != nil {
			log.Fatalf("Failed to get migration version: %v", err)
		}

	default:
		// Handle the case where a new migration should be created
		if *command == "create" {
			// Expect the migration name as a non-flag argument
			args := flag.Args()
			if len(args) == 0 {
				fmt.Println("Error: Migration name is required for 'create' command")
				fmt.Println("Usage: go run cmd/migrate/main.go -db <db-url> -cmd create <migration-name>")
				fmt.Println("Example: go run cmd/migrate/main.go -db postgres://... -cmd create add_new_field")
				os.Exit(1)
			}

			fmt.Printf("Creating migration '%s'...\n", args[0])
			if err := goose.Create(db, *dir, args[0], "sql"); err != nil {
				log.Fatalf("Failed to create migration: %v", err)
			}
			fmt.Printf("Successfully created migration '%s'\n", args[0])
		} else {
			fmt.Printf("Error: Unknown command '%s'\n", *command)
			fmt.Println("Available commands: up, up-by-one, down, reset, status, version, create")
			os.Exit(1)
		}
	}
}
