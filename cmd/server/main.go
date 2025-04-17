package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
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
	typeService := services.NewTypeService(typeRepo, instanceRepo)
	instanceService := services.NewInstanceService(typeRepo, instanceRepo)

	// Determine gRPC server port
	port := 8080
	if portStr := os.Getenv("PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}

	// Create and start the gRPC server
	grpcServer := grpc.NewConnectServer(typeService, instanceService)

	// Create a channel to listen for OS signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start gRPC server in a goroutine
	go func() {
		fmt.Printf("Starting gRPC server on port %d...\n", port)
		if err := grpcServer.Start(port); err != nil {
			if err == net.ErrClosed {
				fmt.Println("Server stopped")
			} else {
				log.Fatalf("Failed to start gRPC server: %v", err)
			}
		}
	}()

	// Wait for a signal to shut down
	<-sigChan
	fmt.Println("Shutting down...")

	// Stop the gRPC server
	if err := grpcServer.Stop(); err != nil {
		log.Fatalf("Failed to stop gRPC server: %v", err)
	}

	fmt.Println("Server stopped")
}
