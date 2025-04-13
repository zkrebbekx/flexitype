package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/zkrebbekx/flexitype/internal/api/http"
	"github.com/zkrebbekx/flexitype/internal/domain/service"
	"github.com/zkrebbekx/flexitype/internal/infrastructure/postgres"
)

var (
	port     = flag.Int("port", 8080, "HTTP server port")
	dbHost   = flag.String("db-host", "localhost", "Database host")
	dbPort   = flag.Int("db-port", 5432, "Database port")
	dbUser   = flag.String("db-user", "postgres", "Database user")
	dbPass   = flag.String("db-pass", "postgres", "Database password")
	dbName   = flag.String("db-name", "flexitype", "Database name")
	dbSSL    = flag.String("db-ssl", "disable", "Database SSL mode")
)

func main() {
	flag.Parse()

	// Create database connection
	db, err := sqlx.Connect("postgres", fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		*dbHost, *dbPort, *dbUser, *dbPass, *dbName, *dbSSL,
	))
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Set connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Create repository and service
	repo := postgres.NewPostgresRepository(db)
	svc := service.NewService(repo)

	// Create HTTP server
	server := http.NewServer(svc, fmt.Sprintf(":%d", *port))

	// Start server in a goroutine
	go func() {
		log.Printf("Starting server on port %d", *port)
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Shutdown server
	log.Println("Shutting down server...")
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exiting")
} 