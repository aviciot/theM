# the-M — Database

Postgres DB: `them`, schema: `them`. Runs in `them-postgres` container (own isolated instance).

## Init (run once after first `docker compose up`)

```powershell
# Windows PowerShell
docker cp db/001_schema.sql them-postgres:/tmp/them_001_schema.sql
docker cp auth_service/SCHEMA.sql them-postgres:/tmp/them_auth_schema.sql
docker cp db/002_seed.sql them-postgres:/tmp/them_002_seed.sql
docker exec them-postgres psql -U them -d them -c "CREATE SCHEMA IF NOT EXISTS auth_service;"
docker exec them-postgres psql -U them -d them -f /tmp/them_001_schema.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_auth_schema.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_002_seed.sql
```

## Access

```powershell
docker exec -it them-postgres psql -U them -d them
```

## Schema ownership

- `them` schema — owned by `them-bridge` (`app/`)
- `auth_service` schema — owned by `them-auth-service` (`auth_service/`)

## Files

| File | Purpose |
|---|---|
| `001_schema.sql` | Full `them` schema DDL — source of truth |
| `002_seed.sql` | Default LLM providers, agents, orchestrators |

See `docs/reference/schema-generated.md` for full table reference.
