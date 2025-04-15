.PHONY: proto build run-server run-client clean db-migrate db-status db-down db-reset db-create run-examples build-examples

# Default Go build tags
BUILD_TAGS ?=

# Go build flags
GOFLAGS ?= -tags "$(BUILD_TAGS)"

# Build output directory
BIN_DIR = ./bin

# Go source files
GO_FILES = $(shell find . -name "*.go" -not -path "./api/*")

all: proto build

# Generate proto files
proto:
	@echo "Generating protobuf and Connect gRPC files..."
	@mkdir -p api/flexitypev1
	@./scripts/generate_proto.sh

# Build all binaries
build: build-server build-client

# Build server binary
build-server:
	@echo "Building server..."
	@mkdir -p $(BIN_DIR)
	@go build $(GOFLAGS) -o $(BIN_DIR)/flexitype-server ./cmd/server

# Build client binary
build-client:
	@echo "Building client..."
	@mkdir -p $(BIN_DIR)
	@go build $(GOFLAGS) -o $(BIN_DIR)/flexitype-client ./cmd/client

# Run server
run-server: build-server
	@echo "Running server..."
	@$(BIN_DIR)/flexitype-server

# Run client
run-client: build-client
	@echo "Running client..."
	@$(BIN_DIR)/flexitype-client $(ARGS)

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BIN_DIR)
	@go clean

# Run tests
test:
	@echo "Running tests..."
	@go test -v ./...

# Build example executables
build-examples:
	@echo "Building examples..."
	@mkdir -p $(BIN_DIR)
	@go build $(GOFLAGS) -o $(BIN_DIR)/disabled-attr-grpc ./examples/disabled_attr_grpc_example.go

# Run the gRPC example
run-examples:
	@echo "Running gRPC example..."
	@chmod +x ./examples/run_grpc_example.sh
	@./examples/run_grpc_example.sh

# Install development tools
install-tools:
	@echo "Installing development tools..."
	@go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	@go install github.com/bufbuild/connect-go/cmd/protoc-gen-connect-go@latest

# Database migration commands using Goose
db-migrate:
	@echo "Running database migrations..."
	@go run cmd/migrate/main.go -db "$(DB_URL)" -cmd up

db-status:
	@echo "Checking migration status..."
	@go run cmd/migrate/main.go -db "$(DB_URL)" -cmd status

db-down:
	@echo "Rolling back latest migration..."
	@go run cmd/migrate/main.go -db "$(DB_URL)" -cmd down

db-reset:
	@echo "Rolling back all migrations..."
	@go run cmd/migrate/main.go -db "$(DB_URL)" -cmd reset

db-create:
	@echo "Creating new migration $(name)..."
	@go run cmd/migrate/main.go -db "$(DB_URL)" -cmd create $(name)