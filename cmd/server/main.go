package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/zac300/flexitype/internal/adapters/grpc"
	"github.com/zac300/flexitype/internal/adapters/repositories/memory"
	"github.com/zac300/flexitype/internal/adapters/repositories/postgres"
	"github.com/zac300/flexitype/internal/application/services"
	"github.com/zac300/flexitype/internal/ports"
)

func main() {
	fmt.Println("Starting FlexiType server...")

	// Create repository based on configuration
	ctx := context.Background()
	var typeRepo ports.TypeRepository
	var instanceRepo ports.InstanceRepository

	// Check if PostgreSQL connection string is provided
	pgConnString := os.Getenv("FLEXITYPE_PG_CONN")
	if pgConnString != "" {
		fmt.Println("Using PostgreSQL repository")
		pgRepo, err := postgres.NewPostgresRepository(pgConnString)
		if err != nil {
			log.Fatalf("Failed to create PostgreSQL repository: %v", err)
		}
		defer pgRepo.Close()

		// Initialize database schema
		err = pgRepo.Initialize(ctx)
		if err != nil {
			log.Fatalf("Failed to initialize database schema: %v", err)
		}

		// Create repositories
		typeRepo = postgres.NewTypeRepository(pgRepo)
		instanceRepo = postgres.NewInstanceRepository(pgRepo, typeRepo.(*postgres.TypeRepositoryImpl))
	} else {
		fmt.Println("Using in-memory repository")
		// Use in-memory repositories
		typeRepo = memory.NewInMemoryTypeRepository()
		instanceRepo = memory.NewInMemoryInstanceRepository()
	}

	// Create services
	_ = services.NewTypeService(typeRepo, instanceRepo)
	_ = services.NewInstanceService(typeRepo, instanceRepo)

	// Create Connect gRPC server
	server := grpc.NewConnectServer(typeRepo, instanceRepo)

	// Start server in a goroutine
	go func() {
		port := 8080
		if portEnv := os.Getenv("FLEXITYPE_PORT"); portEnv != "" {
			fmt.Sscanf(portEnv, "%d", &port)
		}

		err := server.Start(port)
		if err != nil && err.Error() != "http: Server closed" {
			log.Fatalf("Failed to start Connect gRPC server: %v", err)
		}
	}()

	fmt.Println("FlexiType server is running. Press Ctrl+C to stop.")

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	fmt.Println("Shutting down server...")
	server.Stop()
}
