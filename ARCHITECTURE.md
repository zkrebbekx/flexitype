# FlexiType Architecture

FlexiType is designed following hexagonal/clean architecture principles, with a rich domain model. This document explains the architectural structure and key design decisions.

## Architectural Layers

### 1. Domain Layer (`internal/domain/`)

The domain layer contains the core business logic and entities, independent of any external concerns:

- `core/`: Contains the domain entities and business logic
  - `type.go`: Type definition entity
  - `attribute.go`: Attribute definition entity
  - `instance.go`: Instance entity

- `validation/`: Contains validation logic
  - `rules.go`: Defines validation rules and their implementation

The domain layer has no dependencies on other layers. It represents the pure business logic.

### 2. Application Layer (`internal/application/`)

The application layer orchestrates domain objects to fulfill use cases:

- `services/`: Contains services that implement business use cases
  - `type_service.go`: Operations on type definitions
  - `instance_service.go`: Operations on instances

This layer depends on the domain layer and uses ports (interfaces) to interact with external systems.

### 3. Ports Layer (`internal/ports/`)

The ports layer defines interfaces for interacting with the outside world:

- `repositories.go`: Defines interfaces for persistence operations
  - `TypeRepository`: For storing and retrieving type definitions
  - `InstanceRepository`: For storing and retrieving instances

These interfaces are implemented by adapters in the infrastructure layer.

### 4. Adapters Layer (`internal/adapters/`)

The adapters layer provides concrete implementations of the ports:

- `repositories/`: Contains repository implementations
  - `memory/`: In-memory implementation (for testing/embedded use)
  - `postgres/`: PostgreSQL implementation (for production)

- `grpc/`: gRPC server implementation for exposing the API

### 5. Interface Layer (`api/`, `cmd/`, `pkg/`)

- `api/`: Contains API definitions (Protocol Buffers)
  - `flexitype.proto`: gRPC service and message definitions

- `cmd/`: Contains application entry points
  - `server/`: Standalone gRPC server
  - `flexitype/`: CLI application example

- `pkg/`: Contains public packages for client code
  - `sdk/`: Client SDK for interacting with FlexiType

## Key Design Decisions

### 1. Modular Deployments

FlexiType can be deployed in multiple ways:

1. **Standalone Microservice**
   - Uses the PostgreSQL repository implementation
   - Exposes a gRPC API
   - Suitable for large-scale, distributed systems

2. **Embedded Library**
   - Consumers can use the SDK directly in their application
   - Can use either in-memory or PostgreSQL repositories
   - Allows tight integration with existing systems

3. **In-Memory Mode**
   - Uses the in-memory repository implementation
   - Useful for testing, development, and small deployments
   - No external dependencies required

### 2. Repository Pattern

The repository pattern is used to abstract data access:

- Repositories provide a collection-like interface for accessing domain objects
- Repositories hide the details of data access from the domain layer
- Different implementations can be provided for different storage backends
- This allows easy swapping of storage technologies

### 3. Rich Domain Model

The domain model is rich with behavior, not just data:

- Entities contain business logic (validation, inheritance, etc.)
- Business rules are enforced by the domain model itself
- The domain layer is not dependent on any external concerns

### 4. YAML Import/Export

Type definitions can be imported and exported as YAML:

- Allows easy sharing of type definitions between systems
- Provides a human-readable format for configuration
- Makes version control of type definitions straightforward

### 5. Dependency Injection

Dependencies are injected rather than directly created:

- Services receive repositories via constructor parameters
- Repositories receive database connections via constructor parameters
- This pattern makes testing easier and allows flexibility in configuration

## Project Structure

```
flexitype/
├── api/                  # API definitions
│   └── flexitype.proto   # gRPC service definition
├── cmd/                  # Command-line applications
│   ├── flexitype/        # Example client application
│   └── server/           # Standalone server
├── config/               # Configuration files
├── examples/             # Example type definitions
├── internal/             # Internal packages
│   ├── adapters/         # Implementation of ports
│   │   ├── grpc/         # gRPC server implementation
│   │   └── repositories/ # Repository implementations
│   │       ├── memory/   # In-memory repositories
│   │       └── postgres/ # PostgreSQL repositories
│   ├── application/      # Application services
│   │   └── services/     # Service implementations
│   ├── domain/           # Domain model
│   │   ├── core/         # Core domain entities
│   │   └── validation/   # Validation rules
│   └── ports/            # Interface definitions
├── pkg/                  # Public packages
│   └── sdk/              # Client SDK
└── scripts/              # Utility scripts
```

## Dependency Flow

The dependencies flow inward, with the domain at the center:

```
  │    ┌─────────┐
  │    │ Domain  │
  │    └─────────┘
  │         ▲
  │         │
  │    ┌─────────┐
  │    │ Ports   │
  │    └─────────┘
  │         ▲
inward      │
  │    ┌─────────┐
  │    │ Application │
  │    └─────────┘
  │         ▲
  │         │
  │    ┌─────────┐
  │    │ Adapters │
  │    └─────────┘
  │         ▲
  │         │
  │    ┌─────────┐
  ▼    │Interface│
       └─────────┘
```

This architectural approach ensures that:

1. The domain model is isolated from technical concerns
2. Components are loosely coupled and can be changed independently
3. The system is testable at all levels
4. New storage backends or API protocols can be added without changing the domain logic
5. The system can be deployed in various configurations to meet different needs