#!/bin/bash

# Start the FlexiType server and PostgreSQL
echo "Starting FlexiType with Docker Compose..."
docker-compose up -d

# Wait for services to start
echo "Waiting for services to be ready..."
sleep 5

# Check if services are running
SERVER_RUNNING=$(docker ps | grep flexitype-server)
DB_RUNNING=$(docker ps | grep flexitype-postgres)

if [ -n "$SERVER_RUNNING" ] && [ -n "$DB_RUNNING" ]; then
  echo "✅ FlexiType is now running!"
  echo "→ Server is available at: http://localhost:8080"
  echo "→ To view logs: docker-compose logs -f"
  echo "→ To stop: ./stop.sh or docker-compose down"
else
  echo "❌ Error: Not all services are running."
  echo "Check the logs with: docker-compose logs"
fi