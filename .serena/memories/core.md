# Project Core: wb-service

Go service automating multi-cabinet Wildberries product operations: card import (Excel), cross-cabinet copying, attribute & characteristic validation, publication planning, rate-limited execution, media polling, error reconciliation, and a Telegram bot interface.

## Tech Stack & Runtime
- **Language**: Go 1.26 (`go.mod`)
- **Database**: PostgreSQL 18 with `pgx/v5`, connection pooling via `puddle/v2`, schema in `wb` namespace
- **Interface**: Telegram Bot API via `gopkg.in/telebot.v3`
- **File Processing**: Excel parser/exporter via `github.com/xuri/excelize/v2`
- **Logging**: Structured logging via `go.uber.org/zap`
- **Integrity**: Deterministic canonical SHA-256 hashing via `internal/core/canonicalhash`

## Entry Point & Wiring
- Main entry point: `cmd/wb-service/main.go`.
- Composition root initializes logging, PostgreSQL pool, WB clientset, feature services, Telegram server, background polling workers (`RunMediaPolling`, `RunPolling`), and signal handlers for graceful shutdown.

## Knowledge Graph Navigation
Read specialized domain memories for details:
- Architectural patterns, boundaries and invariants: `mem:architecture/overview`
- Wildberries Core transport, rate limiting and clientset: `mem:core/wb_client`
- Telegram bot engine, screen persistence and routing: `mem:core/telegram`
- Database schema, tables, transaction/UoW management: `mem:database/schema`
- End-to-end card transfer state machine and lifecycle: `mem:pipeline/lifecycle`
- Functional business modules (import, edit, prepare, copy, stats, users): `mem:features/modules`
- Environment setup, docker compose, migrations, build and make targets: `mem:operations/run_and_build`
