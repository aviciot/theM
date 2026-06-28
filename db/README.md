# Odin Database Setup

Odin uses a separate PostgreSQL database (`odin`) inside the shared `omni-postgres` container.

## Initialize from scratch

```bash
./scripts/init_db.sh
```

## Manual steps (if needed)

```bash
# Create role + DB
docker exec omni-postgres psql -U postgres -c "CREATE ROLE odin LOGIN PASSWORD 'change_me';"
docker exec omni-postgres psql -U postgres -c "CREATE DATABASE odin OWNER odin;"

# Copy schema files into container and apply
docker cp db/001_schema.sql omni-postgres:/tmp/
docker cp auth_service/SCHEMA.sql omni-postgres:/tmp/auth_schema.sql
docker cp db/002_seed.sql omni-postgres:/tmp/

docker exec omni-postgres psql -U odin -d odin -f /tmp/001_schema.sql
docker exec omni-postgres psql -U odin -d odin -f /tmp/auth_schema.sql
docker exec omni-postgres psql -U odin -d odin -f /tmp/002_seed.sql
```

## Schema ownership
- `odin` schema — owned by odin-bridge (app/)
- `auth_service` schema — owned by odin-auth-service (auth_service/)
