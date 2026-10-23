# Architecture & Design Invariants

## System Summary
- Multi-cabinet Wildberries automation platform in Go 1.27.1 with PostgreSQL 18 and Telegram Bot API.
- Implements resilient e-commerce product lifecycle: Excel import, preparation & WB schema validation, authorization, publication dispatch, media upload tracking, error feed reconciliation, and performance metrics.

## Layered Topology
1. **Composition Root (`cmd/wb-service/main.go`)**:
   - Single wiring point instantiating infrastructure, features, and background loops.
   - Features communicate via contracts/interfaces; zero cyclic cross-feature imports.
2. **Infrastructure Core (`internal/core`)**:
   - `core/logger`: Zap structured JSON logger with trace/attempt logging.
   - `core/repository/postgres`: Connection pooling (`pgx/v5` + `puddle/v2`) and transactional Unit of Work (`core_postgres_transaction.UnitOfWork`).
   - `core/transport/wb`: Kubernetes `client-go` style HTTP client managing Wildberries Content API executors, operation specs, rate limits and retries. Feature-owned `transport/wb` adapters select only their required operations; unused Prices and General API support is removed.
   - `core/transport/telegram`: Long-polling / webhook engine on `telebot.v3`, screen state persistence, and role-based middleware.
   - `core/canonicalhash`: Deterministic SHA-256 with length-prefixed encoding for batch hashing, idempotency keys, and payload validation.
   - `core/domain`: Base types (`SellerID`, `ClientGeneration`, `TransferID`, `Digest`, `User`).
   - `core/errors`: Centralized error taxonomy (`core_errors`). Provides domain/HTTP sentinel errors (`ErrNotFound`, `ErrInvalidArgument`, `ErrConflict`, `ErrForbidden`, `ErrUnauthorized`, `ErrAlreadyExists`, `ErrRateLimited`, `ErrQueueFull`, `ErrCapacityExceeded`, `ErrUnavailable`, `ErrTimeout`, etc.) and re-exports stdlib `errors` functions (`Is`, `As`, `New`, `Join`, `Unwrap`). Feature modules wrap core sentinels via `%w`.
3. **Business Feature Layer (`internal/feature`)**:
   - 7 isolated feature packages: `wbcabinet`, `cardimport`, `cardprepare`, `cardpublication`, `transfer`, `statistics`, `users`.
   - Each feature encapsulates: `transport` (WB adapters / Telegram menus), `service` (domain logic / state machines), `repository` (Postgres queries within UoW).

## Background Workers Topology (in `main.go`)
- Service runs 4 concurrent daemon goroutines orchestrated by `sync.WaitGroup` and context cancellation:
  1. `wbCabinetFeature.RunRestoreRetry(ctx, 30s)`: Retries restoring/authenticating cabinets that failed token verification or ping on startup.
  2. `statisticsFeature.RunAutoAttentionResolver(ctx, 10s)`: Background poller auto-resolving pending attention items and timeout reconciliations.
  3. `cardpublicationFeature.RunMediaPolling(ctx)`: Polls until published cards register on WB, then uploads media files (photos/videos) to `/content/v2/cards/upload/add`.
  4. `transferFeature.RunPolling(ctx, processors...)`: Master polling loop executing 5 chained pipeline stages:
     - `cardprepareFeature.Processor()`: Evaluates pending transfers and builds WB-ready payloads.
     - `cardpublicationFeature.Processor()`: Plans incremental batches respecting WB size limits.
     - `transferFeature.AutomaticAuthorizationProcessor()`: Verifies live credentials before dispatch.
     - `cardpublicationFeature.ProductDispatcher()`: Dispatches card upload requests to Content API.
     - `cardpublicationFeature.ErrorFeed()`: Ingests `/content/v2/cards/error/list` and correlates errors.

## Architectural Invariants
- **Credential Isolation**: Feature modules NEVER touch raw API keys or construct unmanaged HTTP requests. All external WB communication goes through `internal/core/transport/wb` with token-bucket rate limiting (`flowcontrol`) and scoped authorization headers.
- **Dynamic Cabinet Lifecycle**: Cabinets are restored from PostgreSQL or added through Telegram. Onboarding decodes token claims and checks Content `/ping`, then activates a `CredentialCandidate` in `Clientset`. Core credentials parsing and shared access snapshots are reused by features; no General seller-profile request is required by this flow.
- **Atomic State Transitions**: Target convention is service-owned `uow.WithinTransaction` with explicit DBTX for multi-write scenarios; transaction objects are not stored in context. Repository-owned transaction exceptions remain in cardimport and wbcabinet (see `mem:features/modules`). Statistics now performs read-only DB access; positive WB verification uses publication-owned UoW and transfer applier. Preserve locking and revision checks while consolidating these paths.
- **Idempotency & Cryptographic Verification**: Batches, transfer roots, and requests use canonical hashing (`canonicalhash.Digest`). Prevents duplicate submission and tracks payload drift.
- **Core Safe-Retry Policy**: Core automatic retries require read semantics and an explicit safe-retry policy, including POST catalog reads. Core mutations and publication media mutations do not retry automatically after ambiguous delivery; only external media downloads use publication's local retry loop.
- **Audit Immutability**: Historical tables and attempt logs enforce DB triggers preventing `UPDATE` on identity columns and `DELETE` on execution facts (`reject_publication_fact_delete`).

## Related Memories
- Wildberries client internals: `mem:core/wb_client`
- Telegram bot architecture: `mem:core/telegram`
- Database schema & tables: `mem:database/schema`
- End-to-end card transfer lifecycle: `mem:pipeline/lifecycle`
- Transfer simplification & state consolidation plan: `mem:architecture/transfer_simplification_plan`
- Detailed feature modules: `mem:features/modules`
- Operational commands & runtime: `mem:operations/run_and_build`
