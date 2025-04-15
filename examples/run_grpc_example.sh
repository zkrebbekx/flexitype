#!/bin/bash
set -e

echo "Starting FlexiType gRPC server..."
# Start the server in the background
go run cmd/server/main.go &
SERVER_PID=$!

# Ensure server is killed on script exit
trap "kill $SERVER_PID > /dev/null 2>&1" EXIT

# Wait for server to start up
echo "Waiting for server to start..."
sleep 2

echo "Running gRPC client example..."
go run examples/disabled_attr_grpc_example.go

echo "Example completed"