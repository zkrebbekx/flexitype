# FlexiType

FlexiType is a Go library for defining dynamic types with attributes and validation rules. It follows Domain-Driven Design principles with a clean/hexagonal architecture.

## Features

- Define custom types with dynamic attributes
- Apply validation rules to attributes
- Type versioning with instance migration
- Cascades with inheritance and logic expressions
- Attribute disabling between type versions
- Extensible architecture (standalone service or embedded library)
- Import/export type definitions via YAML
- Multiple storage backends (PostgreSQL, in-memory)

## Usage Modes

- Standalone microservice (Connect gRPC API with PostgreSQL)
- Embedded library (bring your own storage)
- In-memory mode for testing/development

## Architecture

- Clean/Hexagonal architecture with rich domain model
- Modular components that can be used independently
- SDK for easy integration into existing applications

## Getting Started

### Prerequisites

- Go 1.21 or later
- Protocol Buffers compiler (`protoc`)
- PostgreSQL (optional, for PostgreSQL storage backend)

### Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/zac300/flexitype.git
   cd flexitype
   ```

2. Install required tools:
   ```bash
   make install-tools
   ```

3. Generate Protocol Buffer and Connect gRPC code:
   ```bash
   make proto
   ```

4. Build the server and client:
   ```bash
   make build
   ```

### Running the Server

Run the standalone server:

```bash
make run-server
```

By default, the server uses in-memory storage. To use PostgreSQL:

```bash
# Set the PostgreSQL connection string
export FLEXITYPE_PG_CONN="postgres://user:password@localhost:5432/flexitype?sslmode=disable"

# Run database migrations
make db-migrate DB_URL="${FLEXITYPE_PG_CONN}"

# Start the server
make run-server
```

### Using the Client

The command-line client can be used to interact with the server:

```bash
# List all types
make run-client ARGS="-action list"

# Create a new type
make run-client ARGS="-action create -id product-001 -name Product -desc 'Product type'"

# Get a type by ID
make run-client ARGS="-action get -id product-001"
```

### Using as a Library

Import the FlexiType library in your Go code:

```go
import (
	"github.com/zac300/flexitype/pkg/sdk"
	"github.com/zac300/flexitype/internal/adapters/repositories/memory"
)

func main() {
	// Initialize in-memory repositories
	typeRepo := memory.NewInMemoryTypeRepository()
	instanceRepo := memory.NewInMemoryInstanceRepository()
	
	// Create a client
	client := sdk.NewClient(typeRepo, instanceRepo)
	
	// Use the client to work with types and instances
	typeDef, err := client.CreateType(ctx, "user-001", "User", "User type")
	// ...
}
```

## Connect gRPC API

FlexiType exposes a Connect gRPC API that can be used with any Connect client. The API is defined in the `api/flexitype.proto` file.

### Example Client in Go

```go
import (
	"context"
	"net/http"
	
	"github.com/bufbuild/connect-go"
	
	"github.com/zac300/flexitype/api/flexitypev1"
	"github.com/zac300/flexitype/api/flexitypev1/flexitypev1connect"
)

func main() {
	client := flexitypev1connect.NewFlexiTypeServiceClient(
		http.DefaultClient,
		"http://localhost:8080",
	)
	
	req := &flexitypev1.ListTypesRequest{}
	res, err := client.ListTypes(context.Background(), connect.NewRequest(req))
	if err != nil {
		// Handle error
	}
	
	// Use the response
	for _, typeDef := range res.Msg.Types {
		fmt.Printf("%s (ID: %s)\n", typeDef.Name, typeDef.Id)
	}
}
```

## Database Schema

FlexiType supports multiple database backends, with a focus on PostgreSQL support for production use. The schema is designed to be portable across major databases (PostgreSQL, MySQL, Oracle) and follows these principles:

- All tables are in the `flexitype` schema
- Table names are singular (not plural)
- Avoids database-specific features like JSON types
- Uses standard SQL data types for maximum compatibility
- Comprehensive indexing for query performance

### Schema Design

The database schema consists of these main tables:

1. `type_definition` - Stores type definitions with versioning support
2. `attribute_definition` - Stores attribute definitions with validation and inheritance rules
3. `validation_rule` - Stores validation rules for attributes
4. `instance` - Stores instances of types
5. `attribute_value` - Stores attribute values for instances
6. `object_value` - Supports nested complex values for attributes

### Database Migrations

FlexiType uses [Goose](https://github.com/pressly/goose) for database migrations, which provides a robust way to manage schema changes:

```bash
# Apply all migrations
make db-migrate DB_URL="postgres://user:password@localhost:5432/flexitype?sslmode=disable"

# Check migration status
make db-status DB_URL="postgres://user:password@localhost:5432/flexitype?sslmode=disable"

# Roll back the latest migration
make db-down DB_URL="postgres://user:password@localhost:5432/flexitype?sslmode=disable"

# Roll back all migrations
make db-reset DB_URL="postgres://user:password@localhost:5432/flexitype?sslmode=disable"

# Create a new migration
make db-create name=add_new_field DB_URL="postgres://user:password@localhost:5432/flexitype?sslmode=disable"
```

Migration files are stored in the `db/migrations` directory using Goose's standard format:

```sql
-- +goose Up
-- SQL in this section is executed when the migration is applied
CREATE TABLE example (
  id SERIAL PRIMARY KEY
);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
DROP TABLE example;
```

## Examples

FlexiType includes several examples that demonstrate various features:

### Attribute Disabling

FlexiType supports disabling attributes in newer versions of a type definition. A disabled attribute:

- Cannot be set or updated via APIs
- Is not enforced in validation
- Can be re-enabled in a later version
- Is preserved in existing instances but ignored for validation

You can see this feature in action in these examples:
- `examples/version_with_disabled_attr.go` - SDK/in-memory example
- `examples/disabled_attr_grpc_example.go` - Connect gRPC example

### Cascades and Inheritance

The cascade system allows attributes to be inherited by child types with configurable behavior:

- Inheritance of attributes from parent types
- Customizable cascade behavior (inherit/override/disable)
- Logic expressions to enforce complex business rules

See `examples/cascade_example.go` for usage.

### Type Versioning

Type definitions can evolve through versions, with instances tracking the version they were created with:

- Automatic versioning when types are modified
- Migration of instances to newer versions
- Validation against the current schema

See `examples/versioning_example.go` for details.