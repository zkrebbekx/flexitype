# Flexitype

Flexitype is a flexible attribute model system inspired by PTC Windchill. It provides a robust way to define and manage attributes with various types and constraints, supporting complex relationships between attributes.

## Features

- **Flexible Attribute Types**: Support for various attribute types including:
  - Boolean
  - String
  - Integer
  - Float
  - Date
  - Time
  - Enum
  - Decimal
  - URL
  - Email
  - JSON

- **Rich Constraints**: Each attribute can have multiple constraints:
  - Required
  - Min/Max Length
  - Min/Max Value
  - Pattern (Regex)
  - Enum Values
  - Multi-value
  - Unique
  - Custom Validation

- **Attribute Relationships**: Support for linked attributes where the value of one attribute can affect the constraints or allowed values of another.

- **Query Language**: JIRA-like query language for searching and filtering attributes and their values.

- **Multiple Interfaces**: Support for both HTTP and gRPC APIs.

- **Extensible Storage**: Default PostgreSQL implementation with clean architecture allowing for custom storage implementations.

## Getting Started

### Prerequisites

- Go 1.16 or later
- PostgreSQL 12 or later

### Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/zkrebbekx/flexitype.git
   cd flexitype
   ```

2. Install dependencies:
   ```bash
   go mod download
   ```

3. Set up the database:
   ```bash
   createdb flexitype
   psql -d flexitype -f migrations/000001_init.up.sql
   ```

4. Build and run the server:
   ```bash
   go build -o flexitype cmd/server/main.go
   ./flexitype
   ```

### Configuration

The server can be configured using command-line flags:

```bash
./flexitype -port 8080 \
  -db-host localhost \
  -db-port 5432 \
  -db-user postgres \
  -db-pass postgres \
  -db-name flexitype \
  -db-ssl disable
```

## API Documentation

### HTTP API

The HTTP API is available at `http://localhost:8080/api/v1/` with the following endpoints:

- `GET /api/v1/attributes` - List all attributes
- `POST /api/v1/attributes` - Create a new attribute
- `GET /api/v1/attributes/{id}` - Get an attribute by ID
- `PUT /api/v1/attributes/{id}` - Update an attribute
- `DELETE /api/v1/attributes/{id}` - Delete an attribute

- `GET /api/v1/values` - List all attribute values
- `POST /api/v1/values` - Create a new attribute value
- `GET /api/v1/values/{id}` - Get an attribute value by ID
- `PUT /api/v1/values/{id}` - Update an attribute value
- `DELETE /api/v1/values/{id}` - Delete an attribute value

- `GET /api/v1/links` - List all type links
- `POST /api/v1/links` - Create a new type link
- `GET /api/v1/links/{id}` - Get a type link by ID
- `PUT /api/v1/links/{id}` - Update a type link
- `DELETE /api/v1/links/{id}` - Delete a type link

- `GET /api/v1/search?q={query}` - Search attributes and values

### gRPC API

The gRPC API is available on the same port as the HTTP API. See the proto files in the `api/connect` directory for the service definition.

## Query Language

The search query language is similar to JIRA's query language. Examples:

- `type = "string"` - Find all string attributes
- `name ~ "user"` - Find attributes with names containing "user"
- `constraints.required = true` - Find all required attributes
- `value > 100` - Find attribute values greater than 100

## Contributing

1. Fork the repository
2. Create a feature branch
3. Commit your changes
4. Push to the branch
5. Create a Pull Request

## License

This project is licensed under the MIT License - see the LICENSE file for details. 