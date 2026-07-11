package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/zkrebbekx/flexitype/internal/api/connect"
	"github.com/zkrebbekx/flexitype/internal/domain/attribute"
	"github.com/zkrebbekx/flexitype/internal/domain/attribute_value"
	"github.com/zkrebbekx/flexitype/internal/domain/attribute_value_dependency"
	"github.com/zkrebbekx/flexitype/internal/domain/type_definition"
	"github.com/zkrebbekx/flexitype/internal/postgres"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	// Create a context that will be canceled when we receive an interrupt signal
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize database connection
	db, err := postgres.NewDB()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Initialize repositories
	typeDefinitionRepo := postgres.NewTypeDefinitionRepository(db)
	attributeRepo := postgres.NewAttributeRepository(db)
	attributeValueRepo := postgres.NewAttributeValueRepository(db)
	attributeValueDependencyRepo := postgres.NewAttributeValueDependencyRepository(db)

	// Initialize services
	typeDefinitionService := type_definition.NewService(typeDefinitionRepo)
	attributeService := attribute.NewService(attributeRepo)
	attributeValueService := attribute_value.NewService(attributeValueRepo)
	attributeValueDependencyService := attribute_value_dependency.NewService(attributeValueDependencyRepo)

	// Initialize Connect service
	connectService := connect.NewService(
		typeDefinitionService,
		attributeService,
		attributeValueService,
		attributeValueDependencyService,
	)

	// Create HTTP server with Connect handlers
	mux := http.NewServeMux()
	path, handler := pb.NewFlexitypeServiceHandler(connectService)
	mux.Handle(path, handler)

	// Create server with h2c support for HTTP/2 without TLS
	server := &http.Server{
		Addr:    ":8080",
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Starting server on :8080")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	<-ctx.Done()

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Attempt graceful shutdown
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exiting")
} 