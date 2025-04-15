#!/bin/bash
set -e

# Check if protoc is installed
if ! command -v protoc &> /dev/null; then
    echo "Error: protoc is not installed. Please install Protocol Buffers compiler."
    exit 1
fi

echo "Installing/updating protoc-gen-go..."
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

echo "Installing/updating protoc-gen-connect-go..."
go install github.com/bufbuild/connect-go/cmd/protoc-gen-connect-go@latest

# Add GOPATH/bin to PATH if it's not already there
export PATH="$PATH:$(go env GOPATH)/bin"

# Remove existing generated files
rm -rf api/flexitypev1*

# Create output directories
mkdir -p api
mkdir -p api/flexitypev1
mkdir -p api/flexitypev1connect

# Generate Go code from protobuf definitions
echo "Generating Go code from proto files..."
protoc \
    --proto_path=api \
    --go_out=api/flexitypev1 \
    --go_opt=paths=source_relative \
    --connect-go_out=api \
    --connect-go_opt=paths=source_relative \
    api/flexitype.proto

echo "Protocol buffer code generation completed successfully!"