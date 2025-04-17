#!/bin/bash

# Stop the FlexiType server and PostgreSQL
echo "Stopping FlexiType..."
docker-compose down

echo "✅ FlexiType has been stopped."
echo "→ To start again: ./start.sh"
echo "→ To remove all data (including database): docker-compose down -v"