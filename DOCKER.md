# Running FlexiType with Docker

This document explains how to run FlexiType using Docker and Docker Compose.

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/)
- [Docker Compose](https://docs.docker.com/compose/install/)

## Running the Application

1. Clone the repository:
   ```bash
   git clone https://github.com/zac300/flexitype.git
   cd flexitype
   ```

2. Build and start the containers:
   ```bash
   docker-compose up -d
   ```

   This will:
   - Start a PostgreSQL container
   - Build and start the FlexiType server

3. The FlexiType server will be available at:
   ```
   http://localhost:8080
   ```

4. View logs:
   ```bash
   # View all logs
   docker-compose logs

   # View only server logs
   docker-compose logs server

   # Follow logs
   docker-compose logs -f
   ```

5. Stop the application:
   ```bash
   docker-compose down
   ```

## Environment Variables

The FlexiType server accepts the following environment variables:

- `FLEXITYPE_PG_CONN`: PostgreSQL connection string
- `FLEXITYPE_PORT`: The port on which the server listens (default: 8080)

These are already configured in the docker-compose.yml file.

## Data Persistence

PostgreSQL data is persisted in a Docker volume named `postgres-data`. This ensures your data isn't lost when containers are restarted.

To remove all data and start fresh:
```bash
docker-compose down -v
```

## Connecting to the Database

To connect to the PostgreSQL database directly:

```bash
docker exec -it flexitype-postgres psql -U flexitype -d flexitype
```

## Using the Client

To use the FlexiType client with the dockerized server:

```bash
# Build the client
go build -o bin/client ./cmd/client

# Run the client against the server
bin/client --server localhost:8080
```