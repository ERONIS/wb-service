# Operations, Build, Migrations & Environment

## Environment Variables
- `.env`:
  - `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, `POSTGRES_PORT`
  - `POSTGRES_TIMEOUT`: default request timeout for PostgreSQL queries
  - `POSTGRES_SSLMODE`: SSL mode (`disable`, `require`, `verify-ca`, `verify-full`, default `disable`)
  - `POSTGRES_MAX_CONNS`: maximum pool connections (default `25`)
  - `POSTGRES_MIN_CONNS`: minimum pool connections (default `4`)
  - `POSTGRES_MAX_CONN_LIFETIME`: max connection lifetime (default `1h`)
  - `POSTGRES_MAX_CONN_IDLE_TIME`: max connection idle duration (default `30m`)
  - `POSTGRES_HEALTHCHECK_PERIOD`: background healthcheck interval (default `1m`)
  - `POSTGRES_APPLICATION_NAME`: stable PostgreSQL session name for `pg_stat_activity` (default `wb-service`; ASCII letters, digits, `-`, `_`, `.`, up to 63 bytes)
  - `TELEGRAM_TOKEN`, `TELEGRAM_POLL_TIMEOUT`, etc.
  - `ADMIN_TG_ID`, `ADMIN_FULL_NAME` (required for bootstrapping admin and admin cabinets)
  - `LOG_LEVEL` / `LOGGER_LEVEL`
- The Makefile includes only `.env`; `.env.wb` is no longer used. WB API hosts and the client timeout are configured in code, while cabinets are stored in PostgreSQL (`wb.api_cabinets`). See `mem:core/wb_client`.

## Common Makefile Targets
- `make env-init`: Full setup: starts PostgreSQL container, waits for readiness, runs migrations (`migrate-up`), bootstraps admin user (`admin-bootstrap`), and bootstraps initial WB cabinets (`cabinet-bootstrap`).
- `make env-up` / `make env-down`: Start / stop PostgreSQL docker container.
- `make env-wait`: Polls PostgreSQL container with `pg_isready`.
- `make env-cleanup`: Deletes local PostgreSQL volume data in `.runtime/pgdata` (with confirmation).
- `make migrate-up`: Applies the 7 ordered baseline database migrations located in `migration/` to a fresh database.
- `make migrate-down`: Rolls back the last applied migration (`down 1`) using its paired `.down.sql` file. Transition migrations for editing an existing deployed schema are omitted from this fresh-install baseline.
- `make migrate-action action="<command>"`: Runs custom migrate action (e.g. `action="down 2"`, `action="force 7"`, `action=version`).
- `make migrate-create seq=<name>`: Creates a new SQL migration pair in `migration/`.
- `make admin-bootstrap`: Executes `scripts/bootstrap_admin.sql` to upsert admin user from `.env`.
- `make cabinet-bootstrap`: Executes `scripts/bootstrap_cabinets.sql` to upsert initial WB cabinets for the admin user.
- `make postgres-integration-test`: Requires `POSTGRES_TEST_DSN`, then runs cardimport and transfer PostgreSQL integration tests with cache disabled (`-count=1`); use a separate migrated database. A missing DSN fails the target.
- `make postgres-integration-test-local`: Uses Python 3 and Docker Compose to start local PostgreSQL, create a uniquely named isolated database, apply all migrations, start the loopback port forwarder, run the strict integration test target with an encoded DSN, and drop the database after success. On failure, leaves the test database for diagnosis. It reads Compose-resolved credentials without printing them.
- `make wb-service-run`: Applies migrations, starts the loopback PostgreSQL port forwarder, then runs the service locally (`go run cmd/wb-service/main.go`). The host-side `POSTGRES_HOST=127.0.0.1` connection requires that forwarder because the PostgreSQL service itself does not publish port 5432.

## Repository Hygiene
- New root-level Markdown is ignored by default; `README.md`, `AGENTS.md`, and `GEMINI.md` are exceptions. Project documentation under `doc/` and Serena memories under `.serena/memories/` remain eligible for tracking.
- Root `notes/`, `drafts/`, and `scratch/` directories, personal Markdown suffixes (`.local.md`, `.draft.md`), and common editor backup files are ignored. Existing tracked files remain tracked.

- Build output `wb-service` and `.vscode/settings.json` are ignored artifacts. History rewrites must use a separate mirror clone so the active worktree and unpublished commits are preserved; verify every branch head before force-with-lease updates. After a rewrite, old local branches must be recreated or rebased onto the new remote history rather than merged or pushed as-is. For an old branch with unpublished commits and dirty WIP, first checkpoint the WIP on a new branch, verify the old and rewritten base trees match, then replay only the unpublished commits onto the rewritten origin. GitHub `refs/pull/*` are read-only and can retain old commits after branch history is rewritten.

## Testing & Verification Commands
- User preference: add or update tests when needed. For schema changes, edit the fresh-install baseline migrations; do not add transition migrations to upgrade an existing database. The user will reinitialize their database personally; do not reset it as part of refactoring.
- Run all unit tests: `go test ./...`
- Test specific module: `go test ./internal/feature/transfer/...`
- Check code compilation: `go build ./cmd/wb-service`
- Cardimport and transfer PostgreSQL integration tests require an isolated migrated database via `POSTGRES_TEST_DSN`; absent DSN skips direct `go test` integration cases, while a configured but unavailable database fails. `make postgres-integration-test` is the strict entry point. Cardimport now covers atomic sealed batch/item insertion, source-file JSONB, replay, and rollback without leaving batch rows. In-memory workflow tests cover finalize retry and concurrent revision updates. Transfer's fixture uses the current schema and checks every required write; its integration cases also assert that an aborted preparation UoW removes both the proposal artifact and group projection. Successful preparation fixtures must compute the metadata digest from `json.RawMessage(snapshot)`, matching `ApplyPreparationResultCommand.Validate`; hashing plain `[]byte` marshals it as a base64 JSON string and rejects the command before any database write. The rollback test validates its command before entering UoW.

## Related Memories
- Overall architecture: `mem:architecture/overview`
- Database schema: `mem:database/schema`
- WB Client: `mem:core/wb_client`
