FROM golang:1.23.8 AS builder

WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the server
RUN CGO_ENABLED=0 GOOS=linux go build -o /flexitype-server ./cmd/server

# Use a smaller image for the final container
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /flexitype-server /app/flexitype-server

# Copy migration files
COPY --from=builder /app/db/migrations /app/db/migrations

# Expose the port the server listens on
EXPOSE 8080

# Set environment variables
ENV FLEXITYPE_PORT=8080

# Run the server
CMD ["/app/flexitype-server"]