#!/bin/bash
set -e

echo "========================================="
echo "OpenFGA PostgreSQL Setup"
echo "========================================="

# Check if PostgreSQL is running
if ! docker exec postgres pg_isready -U orderpipelineadmin -d orderpipeline -h 127.0.0.1 &>/dev/null; then
    echo "❌ PostgreSQL is not running. Start it with: docker compose up -d postgres"
    exit 1
fi

echo "✓ PostgreSQL is running"

# Create OpenFGA database (connect to 'postgres' database first)
echo ""
echo "Creating 'openfga' database..."
docker exec -e PGPASSWORD=postgres postgres \
    psql -U orderpipelineadmin -d postgres \
    -tc "SELECT 1 FROM pg_database WHERE datname='openfga'" | grep -q 1 && {
    echo "✓ Database 'openfga' already exists"
} || {
    docker exec -e PGPASSWORD=postgres postgres \
        psql -U orderpipelineadmin -d postgres \
        -c "CREATE DATABASE openfga;"
    echo "✓ Created database 'openfga'"
}

# Verify the database was created
echo ""
echo "Verifying database creation..."
docker exec -e PGPASSWORD=postgres postgres \
    psql -U orderpipelineadmin -d postgres \
    -c "\l openfga"

echo ""
echo "========================================="
echo "✓ OpenFGA database setup complete!"
echo "========================================="
echo ""
echo "Next steps:"
echo "1. Update docker-compose.yml with PostgreSQL config for OpenFGA"
echo "2. Run: docker compose --profile openfga up -d openfga"
echo "3. Verify tables: docker exec -e PGPASSWORD=postgres postgres psql -U orderpipelineadmin -d openfga -c '\dt'"
echo ""