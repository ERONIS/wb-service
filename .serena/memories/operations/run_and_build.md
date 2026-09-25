# Operations, Build, Migrations & Environment

## Environment Variables
- `.env`:
  - `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, `POSTGRES_PORT`
  - `TELEGRAM_BOT_TOKEN`, `TELEGRAM_BOT_DEBUG`
  - `ADMIN_TG_ID`, `ADMIN_FULL_NAME` (required for bootstrapping admin)
  - `LOG_LEVEL` (debug, info, warn, error)
- `.env.wb`:
  - `WB_CABINETS`: Comma-separated list or JSON mapping of initial cabinets and API tokens for auto-bootstrap.

## Common Makefile Targets
- `make env-init`: Full setup: starts PostgreSQL container, waits for readiness, runs migrations (`migrate-up`), and bootstraps admin user.
- `make env-up` / `make env-down`: Start / stop PostgreSQL docker container.
- `make env-wait`: Polls PostgreSQL container with `pg_isready`.
- `make env-cleanup`: Deletes local PostgreSQL volume data in `.runtime/pgdata` (with confirmation).
- `make migrate-up`: Applies pending database migrations located in `migration/`.
- `make migrate-down`: Rolls back the last applied migration.
- `make migrate-create seq=<name>`: Creates a new SQL migration pair in `migration/`.
- `make admin-bootstrap`: Executes `scripts/bootstrap_admin.sql` to upsert admin user from `.env`.
- `make wb-service-run`: Runs the service locally (`go run cmd/wb-service/main.go`).

## Testing & Verification Commands
- Run all unit tests: `go test ./...`
- Test specific module: `go test ./internal/feature/transfer/...`
- Check code compilation: `go build -o /dev/null ./cmd/wb-service`

## Related Memories
- Overall architecture: `mem:architecture/overview`
- Database schema: `mem:database/schema`
