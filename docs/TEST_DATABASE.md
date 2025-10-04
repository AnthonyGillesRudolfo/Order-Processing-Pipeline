# Testing Database Integration

This guide provides step-by-step instructions to test the PostgreSQL database integration with the Order Processing Pipeline.

## Prerequisites

1. PostgreSQL installed and running
2. Database and user created (see DATABASE_SETUP.md)
3. Application built: `make run`

## Test Procedure

See original `TEST_DATABASE.md` for full instructions. Key steps:
- Start PostgreSQL
- Run `scripts/setup_database.sql`
- Start app and verify tables exist
- Create an order and verify rows in `orders`, `payments`, `shipments`

## Queries

Examples:
```sql
SELECT * FROM orders ORDER BY created_at DESC;
SELECT * FROM payments ORDER BY created_at DESC;
SELECT * FROM shipments ORDER BY created_at DESC;
```
