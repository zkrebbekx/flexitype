#!/bin/bash

# Create output directories if they don't exist
mkdir -p api/connect/pb
mkdir -p api/connect/pbconnect

# Generate protobuf and Connect files in one command
protoc \
    --go_out=api/connect/pb --go_opt=paths=source_relative \
    --go-grpc_out=api/connect/pb --go-grpc_opt=paths=source_relative \
    --connect-go_out=. --connect-go_opt=paths=source_relative \
    api/connect/type_definition.proto \
    api/connect/attribute.proto \
    api/connect/attribute_value.proto \
    api/connect/attribute_value_dependency.proto 