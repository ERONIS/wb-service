# Code Patterns & Implementation Mechanics

## Go Source Layout
- Package-level constants, variables, aliases, structs, and interfaces precede all functions and methods in each Go source file (`AGENTS.md`).

## 1. Cryptographic Hashing (`internal/core/canonicalhash`)
- **Structure**: `canonicalhash.NewSHA256(domain string) *Builder`.
- **Length-Prefixed Binary Encoding**:
  - `Bytes(b []byte)` writes `binary.BigEndian.PutUint64` of length, then raw bytes.
  - `Int64(v int64)` writes 8 bytes BigEndian.
  - `Bool(v bool)` writes single byte `0x01` or `0x00`.
- **Purpose**: Eliminates delimiter collisions (e.g. `("ab", "c")` vs `("a", "bc")`) and guarantees deterministic state identification across restarts.

## 2. Unit of Work & Transactions (`internal/core/repository/postgres/transaction`)
- **Interface**: `UnitOfWork.WithinTransaction(ctx context.Context, fn func(context.Context, DBTX) error) error`.
- **Mechanics**:
  - Begins transaction via `beginner.BeginTx(ctx, options)`.
  - Injects `DBTX` interface (`Exec`, `Query`, `QueryRow`, `CopyFrom`).
  - Automatic `defer tx.Rollback(rollbackCtx)` with isolated 5-second timeout on error or panic.
  - Commits atomically on `fn` returning `nil`.

## 3. WB Client Flow Control & Safety Policy (`internal/core/transport/wb`)
- **Token Bucket Rate Limiter (`flowcontrol/limiter.go`)**:
  - Powered by `golang.org/x/time/rate.Limiter`.
  - Rate limits partitioned per cabinet and Content API bucket.
  - Response adaptation: observes `X-Ratelimit-*` headers, clamps available tokens to the reported remaining count, and sets `blockedUntil` on HTTP 429 (using `Retry-After` when present). Configured interval and burst remain fixed.
  - Bounded queue: uses `waiters chan struct{}` to reject callers with `ErrLimiterQueueFull` when backlog overflows.
- **Operation routing (`policy/operation.go`)**: Each operation carries a validated lowercase WB API subdomain under `wildberries.ru` and a path; malformed labels, external domains, ports, userinfo, and schemes in the host are rejected. Request preparation combines them as HTTPS per call; the credential-bound executor and `Clientset` contain no fixed API URL. API bucket specs are supplied by the composition root at registry construction.
- **Strict Safe-Retry Policy (`policy/retry.go`)**:
  - Two modes: `RetryModeNever` and `RetryModeReadSafe`.
  - Core retries require read semantics (`OperationKindRead`) and `RetryModeReadSafe`; this includes POST catalog reads. `policy.DefaultMaxReadAttempts` fixes three read attempts; `flowcontrol.DefaultRetryBaseDelay` fixes the 500 ms base delay. `APIClient` no longer accepts retry settings from `Clientset`.
  - Mutation operations use `RetryModeNever` inside the core executor. Publication sends link/file mutations once per business attempt; its local retry helper is restricted to downloading external media. Unknown delivery is handled by business reconciliation.
- **Crash Recovery & Reconciliation**:
  - `ProductDispatcher.ProcessPending` begins by calling `ListInterruptedProductActions` to re-verify actions left in-flight during a service crash before picking up new work.

## 4. Telegram UI Persistence & Throttling (`internal/core/transport/telegram`)
- **Persistence (`MenuStore`)**:
  - Backed by table `wb.telegram_active_screens`.
  - Stores `(user_id, last_message_id, screen_name)`.
  - Avoids orphaned inline keyboards; edits existing message instead of sending new spam.
  - Enforces Single Active Screen invariant through `menuHost.renderChat` across all modules.
- **Throttling & Size Control**:
  - `menuUpdateInterval = 1500ms`: prevents Telegram HTTP 429 rate limit errors.
  - `menuTextUnitLimit = 4096`: enforces Telegram message size constraints with automatic truncation marker.
  - Compact Base-36 ID encoding (`EncodeID`, `ParseID`, `ParseBase36Argument`) prevents reaching Telegram's 64-byte `callback_data` limit.
- **Reusable UI Components**:
  - `BuildPaginationRow` and `CalculateTotalPages` for standardized inline pagination.
  - `FormatProgressBar` and `CalcPercent` for deterministic progress rendering.
  - `FormatDateTime` / `FormatDateTimeUTC` for localized timestamp presentation.

## 5. Dynamic Cabinet Hot-Reload (`internal/feature/wbcabinet`)
- **Candidate Pattern (`CredentialCandidate`)**:
  - Unverified tokens are encapsulated in `CredentialCandidate` and cannot be used by feature modules. `Clientset.Cabinets()` returns copies of activated candidates, so callers of that list can also access each candidate's Content executor.
  - Core credentials parsing is followed by wbcabinet Content `/ping` verification, persistence and `ActivateCandidate`. General API support was removed as unused. Restoration only retries pending generations and does not reverify working clients on every tick.
  - Dynamically updates active `Clientset` in-memory without service restart.
- **Token storage boundary**: Current wbcabinet repository passes the raw verified token directly to the `wb.api_cabinets.token` TEXT column and reads it back on restoration; application-level AES-GCM encryption is not implemented. Do not assume encryption from earlier architecture notes.

## 6. Polling Worker Loop (`internal/feature/transfer/server/polling.go`)
- **Concurrency & Scheduling**:
  - Runs independent `runProcessor` goroutines per pipeline stage under a shared `sync.WaitGroup`.
  - Supports schedule ticker (`interval`) and wake-up notifications (`wake <-chan struct{}`).
  - Each processor executes inside an exclusive mutex (`processMu sync.Mutex`) ensuring no concurrent executions of the same pipeline step.

## 7. Language Standards & Code Comments
- **Mandatory Russian Comments**: All source code comments, docstrings, godoc comments, and inline code explanations MUST be written strictly in Russian.
- Non-Russian (e.g. English) comments in source code are forbidden across all packages in `cmd/` and `internal/`.

## 8. Unified Error Handling & Diagnostics (`internal/core/errors`)
- **Centralized Sentinels & Canonical Re-exports**:
  - `core_errors` provides standardized HTTP-aligned sentinels (`ErrNotFound`, `ErrInvalidArgument`, `ErrConflict`, `ErrForbidden`, `ErrUnauthorized`, `ErrRateLimited`, `ErrQueueFull`, `ErrCapacityExceeded`, `ErrInternal`, `ErrUnavailable`, etc.).
  - Re-exports standard library error functions: `Is`, `As`, `New`, `Join`, `Unwrap`.
- **Boilerplate Reduction Constructors**:
  - Single-line constructors wrapping standard sentinels: `NotFound(format, args...)`, `InvalidArgument(...)`, `Conflict(...)`, `Forbidden(...)`, `Unauthorized(...)`, `RateLimited(...)`, `Internal(...)`, `Unavailable(...)`, `CapacityExceeded(...)`, `PreconditionFailed(...)`.
  - Annotation helpers: `Wrap(err, msg)` and `Wrapf(err, format, args...)` which safely return `nil` on `nil` error.
- **Predicates & Validation Helpers**:
  - Fast semantic checks without verbose manual type assertions: `IsNotFound(err)`, `IsInvalidArgument(err)`, `IsConflict(err)` (checks conflict & already exists), `IsForbidden(err)`, `IsRateLimited(err)`, `IsUnavailable(err)`, `IsTimeout(err)`, `IsCanceled(err)`.
  - Guard conditions: `Require(condition, err)`, `RequireContext(ctx)`.
- **Telegram Error Presentation Integration**:
  - `internal/core/transport/telegram/presenter.NotifyServiceError(ctx, err, extraCases...)` auto-matches core errors to Russian localized user notifications, collapsing repetitive switch-case error blocks across features into single-line handlers.

## 9. Preparation Pipeline & State Ownership
- **Transfer Sole State Ownership**: `transfer` owns all state transitions and queue management. `cardprepare` is a calculation/validation engine with no separate database jobs.
- **Single UoW Transaction**: Proposal artifacts (`card_preparation_artifacts`) and group-target progress (`transfer_group_targets`) are updated atomically in one `uow.WithinTransaction` commit via `ApplyTx`.
- **Row Locking & Strict Idempotency**: `ApplyTx` locks target rows using `SELECT ... FOR UPDATE`. Replays with identical parameters/diagnostics return `nil` (idempotent no-op). Conflicting replays reject with `ErrPreparationResultConflict`.
- **Progress-Aware Chained Wake Signaling**: Polling engine supports `ProgressAwareProcessor`. Successful progression triggers `wakeDownstream` through dedicated stage channels (`stageWakes`). Passes with 0 successes or only errors do not wake downstream stages, preventing wake storms.
- **Fair Scheduling & Starvation Avoidance**: Pending groups query uses window partition ranking (`ROW_NUMBER() OVER (PARTITION BY group_target.target_id ORDER BY group_target.id) AS cabinet_rank`). Combined with `fairPendingGroupOrder`, batches interleave across all cabinets, enabling processors to advance past temporary cabinet failures without busy loops.
- **Elimination of Legacy Identifiers**: `PreparationID` and `PreparationGroupID` are completely eliminated across `cardprepare`, `transfer`, and `cardpublication`. `ProposalQuery` and `StoredProposal` link directly by `GroupTargetID` and `TransferID`.
- **Centralized Outcome Aggregation**: `internal/feature/transfer/repository/postgres/outcome.go` centralizes SQL aggregation expressions (`groupOutcomeCountsCTE`, `groupToTransferOutcomeExpression`) and outcome transition functions (`advanceTransferAfterPreparation`, `finishPublicationTransfer`, `reduceTransferAfterPlanning`).

## 10. Unified User Identity (Elimination of `Actor`)
- **Single Source of Truth**: `internal/core/domain/actor.go` is deleted. The domain model `domain.User` (`internal/core/domain/user.go`) serves as the sole identity across the pipeline, ingestion, and transport layers.
- **Minimal Codebase / Zero Bloat**: No redundant aliases (`TrustedActor`, `TrustedUser`) and no redundant helper methods on `domain.User`. `user.FullName` is used directly.
- **Entity Lifecycle Factories**: `domain.CreateUser` serves as the in-memory factory (initializing default `RoleUser` with zero ID/timestamps prior to persistence), shared by `core_tg_identity.UserFromContext` and `users_service.AddUser`. `domain.NewUser` reconstructs fully populated entities from storage rows.
- **Telegram Context Extraction**: `core_tg_identity.UserFromContext(ctx tele.Context)` extracts `domain.User` using `domain.CreateUser(telegramID, name)` with a fallback to `fmt.Sprintf("Telegram %d", telegramID)` (max 100 runes), ensuring `user.Validate()` invariants pass without bloated abstractions.
- **Batch Author Ingestion**: `cardimport` binds batch lifecycle directly to `domain.User` (`AuthorSnapshot` populated with `user.TelegramID`, `user.FullName`, and optional `user.ID`).

## 11. Canonical WB Identity Primitives (`internal/core/domain/wb_identity.go`)
- `domain.SellerID` is the sole stable seller/cabinet identity. It is the raw `sid` claim from the WB token after lowercase UUID normalization by `credentials.ParseToken`; the domain type is a string with canonical UUID validation. There is no seller-ID hash or second local cabinet ID.
- `domain.CabinetAccess` is the shared token-free access snapshot. It carries one `SellerID` plus name, revisions, capabilities, expiry and `ClientGeneration`; transfer-specific position, cohort and digest remain in transfer.
- `domain.ClientGeneration` remains a separate `[32]byte` canonical fingerprint of the exact API token. It changes on token rotation and pins executors and transfer/publication snapshots to the verified token generation; it is not a cabinet identity or encryption for the stored token.
- `wbcabinet.Service.Add` parses `sid`, casts the normalized claim to `domain.SellerID`, verifies the token through Content `/ping`, persists `api_cabinets` by raw seller ID and activates the candidate. Restoration checks the same ID against the stored cabinet.
- Transfer target-set root and snapshot revision use the `v4` domain tags and hash each raw `SellerID` once, together with generation, revisions, expiry and capabilities.


## 12. PostgreSQL Infrastructure & Observability (`internal/core/repository/postgres`)
- **Connection Pool Tuning (`pool/config.go` & `pool/pool.go`)**:
  - `Config` supports production pool controls via env: `POSTGRES_SSLMODE` (disable, require, verify-ca, verify-full), `POSTGRES_MAX_CONNS` (default 25), `POSTGRES_MIN_CONNS` (default 4), `POSTGRES_MAX_CONN_LIFETIME` (default 1h), `POSTGRES_MAX_CONN_IDLE_TIME` (default 30m), `POSTGRES_HEALTHCHECK_PERIOD` (default 1m).
  - Validated via `Config.Validate()` with self-healing defaults for zero values in test literals.
  - Safe DSN construction via `net/url.URL` and `net.JoinHostPort` preventing URI injection or parsing failures with special password characters (`@`, `:`, `#`, `%`).
- **Query & CopyFrom Tracing (`pool/tracer.go`)**:
  - Implements `pgx.QueryTracer` and `pgx.CopyFromTracer`.
  - Non-intrusive structured logging into `go.uber.org/zap`: tracks operation signature (first 2 SQL tokens or target table name), execution duration, rows affected/copied, and SQLSTATE codes on error.
  - Warnings for slow queries exceeding `slowQueryThreshold = 500ms`.
  - Query arguments are not explicitly logged, but the current tracer still logs raw errors and derives operation names from the first two SQL tokens. This does not guarantee redaction: PostgreSQL error messages or SQL literals can contain data. Safe tracing requires stable operation identifiers and allowlisted error metadata.
- **Technical Error Classification (`internal/core/repository/postgres/pgerr`)**:
  - Centralized predicates without changing domain semantics: `IsUniqueViolation` (23505), `IsForeignKeyViolation` (23503), `IsCheckViolation` (23514), `IsSerializationFailure` (40001), `IsDeadlock` (40P01), `IsObjectNotInPrerequisiteState` (55000).
  - Helper `ConstraintName(err)` for feature-level semantic translation.
  - Preserves error chain via `errors.As(err, &pgErr)` and `Unwrap`.
- **Clean Transfer Repository Wiring**:
  - Dead dependency `uow core_postgres_transaction.UnitOfWork` eliminated from `transfer_postgres_repository.Repository` and constructor `New(pool)`. UoW remains solely at the service/applier level.

## 13. Telegram Transport Boilerplate Reduction Patterns (`internal/*/transport/telegram`)
- **Table-Driven Status Mappings (`statistics/transport/telegram/cabinet.go`)**:
  - Replaced 120-line cascading if/switch blocks (`cabinetTaskStatus`, `terminalItemStatus`) with declarative lookup tables (`terminalOutcomeLabels`, `phaseLabels`), cutting 90 lines of repetitive branching.
- **Consolidated Role & Action Mutators (`users/transport/telegram/user_actions.go`)**:
  - Deduplicated `setAdmin`, `setPartner`, `revokePartner` via higher-order function `mutateUserRole(ctx, targetID, page, prefix, action)`.
  - Merged standalone `delete_user.go` into `user_actions.go` and deleted the redundant file.
- **Dead Code Cleanup (`cardimport/transport/telegram/helpers.go`)**:
  - Eliminated unused `recoverableFilesCount`.

## 14. Repository Modular Decomposition (Phase 2)
- **cardpublication (`internal/feature/cardpublication/repository/postgres`)**:
  - Monolithic `product.go` (2 187 lines) decomposed into 7 focused files within package `cardpublication_postgres_repository`:
    - `product_dispatch.go`: `ListDispatchableProductActions`, `ListInterruptedProductActions`, candidate scanners.
    - `product_lock.go`: `LockProductAction`, `lockProductIdentities`.
    - `product_observation.go`: `InsertActionObservation`, byte helpers (`equalBytes`, `nullablePositive`).
    - `product_plan_control.go`: `PlanAttemptCount`, `SupersedePlanBeforeDispatch`, `AbortPlanAfterDispatch`, `PublicationPlanTerminal`, `reducePublicationPlan`.
    - `product_attempt.go`: `BeginProductAttempt`, `RecordSubmission`.
    - `product_retry.go`: `BeginProductRetry`, `RecordProductRetry`.
    - `product_reconciliation.go`: `ListReconcilingProductActions`, `LoadErrorBatchMatches`, `RecordProductReconciliationPoll`, `FinishProductReconciliation`, `recordProductReconciliationMembers`, `finishPotentialMediaActions`, `PendingMediaGroupTargetIDs`.
  - Monolithic `media.go` (1 038 lines) decomposed into 4 focused files within package `cardpublication_postgres_repository`:
    - `media_dispatch.go`: `ListDispatchableMediaActions`, `ListInterruptedMediaActions`, `ListPendingMediaActions`.
    - `media_lock.go`: `LockMediaAction`.
    - `media_attempt.go`: `BeginMediaAttempt`, `FinishMediaWithoutAttempt`.
    - `media_result.go`: `RecordMediaResult`, `recordMediaAttemptResult`, `finishMediaAction`, `loadMediaGroupResult`.
- **cardimport (`internal/feature/cardimport/repository/postgres`)**:
  - `batch.go` (874 lines) decomposed:
    - `batch_external.go`: `CreateExternalBatch`, `loadBatchBySource`.
    - `batch_finalize.go`: `Finalize`, `lockFinalizeSession`, `validateFinalizeReadiness`, `loadFrozenItems`, `batchItemDomains`, `copyFrozenItems`.
    - `batch.go`: queries and headers (`GetBatch`, `ListFinalizedBatches`, `loadBatch`, `scanBatchHeader`, `ListBatchItems`).
- **transfer (`internal/feature/transfer/repository/postgres`)**:
  - `live_authorization.go` decomposed:
    - `live_authorization_verify.go`: `LockValidLive`, `targetOwnerMayMutate`.
    - `live_authorization.go`: state transitions and commands (`RequestLive`, `ApproveLive`, `RevokeLive`, `ChangeLiveState`, etc.).
- **Zero Breaking Changes**: Public signatures, SQL queries, lock order (`FOR UPDATE`), outcome aggregations, and transaction boundaries completely preserved.

## 15. Private Row DTO Mapping Pattern (Phase 3 Pilot)
- **Private `rowDTO` vs Public Domain Models**:
  - Domain models (e.g. `domain.User`) never receive SQL/database tags (`db:"..."`) or nullable database types.
  - Repositories define private struct `userRowDTO` with explicit `db` tags mapping exact query column aliases (`id`, `tg_id`, `tg_nickname`, `full_name`, `role`, `created_at`, `updated_at`).
  - DTO conversion uses `domain.NewUser`; the existing benchmark measures only `dtoToDomain`, not pgx scanning, CollectRows or slice conversion. Do not infer zero allocations for repository reads from that benchmark.
- **Idiomatic pgx v5 Row Collection**:
  - Multi-row queries use `pgx.CollectRows(rows, pgx.RowToStructByName[userRowDTO])`, eliminating manual `rows.Next()` / `rows.Scan()` / `rows.Close()` boilerplate while guaranteeing proper cursor cleanup.
  - Scalar and single-row queries retain direct `QueryRow().Scan` on the private DTO to avoid multi-row query lifecycle overhead.
- **Integrated Error Classification**:
  - Repositories utilize `pgerr.IsUniqueViolation(err)` rather than local duplicate SQLSTATE `"23505"` constants or manual `*pgconn.PgError` type assertions.

## 16. Verified WB Attention Resolution
- `statistics/service.Reader` is read-only. Positive WB evidence is passed as full `contentapi.Card` plus observation time to `cardpublication.VerifiedCardResolver`; the compact metadata fields remain for the Telegram view.
- Resolver's UoW locks transfer first, then the exact publication subject by transfer/action/member/revision/attempt. It rejects stale identities and observations older than the attempt, persists a time-specific `post_submission` observation, checks it against the original upload request, and applies publication plus transfer corrections atomically.
- Transfer correction recalculates group/master outcome while preserving unresolved or rejected media status. External WB lookup runs before the transaction.

## 17. Statistics Detail Read Pattern
- `statistics/repository/postgres/operations.go` uses one `collectDetails[T]` path for the five bounded detail lists (groups, items, actions, attempts, authorizations). It performs the `DetailsPageSize + 1` query, scans rows incrementally, preserves list/scan/iteration error context, checks `rows.Err`, closes the cursor, and trims the extra row to report truncation.
- Row-specific scanners preserve nullable timestamp conversion and column order. Shared `actionDetailsSelect`/`actionDetailsGroupBy` and `attemptDetailsSelect` keep list and single-action reads aligned without changing their filters or result cardinality.

## 18. Statistics Optional Filter Builder
- `statistics/repository/postgres/whereBuilder.addIf` adds a condition and argument together only when its filter is present. `addCreatedRange` applies inclusive `created_at >= from` and exclusive `created_at < to` using fixed query-local column names and bound time values.
- Operation, attention, and aggregate readers retain their entity-specific SQL predicates and their original argument order; only the optional-filter boilerplate is shared. Transfer outcome-class translation and per-entity attention semantics remain local to their respective builders.

## 19. Shared PostgreSQL Repository Primitives
- `core/repository/postgres/pool.Pool` embeds the existing `transaction.DBTX` contract for SQL execution; pool-only methods are transaction creation, close, and `OperationContext`.
- `core/repository/postgres/sequence.ReserveIDs` reserves a requested batch of IDs from a parameterized `regclass` within the caller's `DBTX`. Transfer initialization and publication planning share the query/row lifecycle; their wrappers retain distinct zero-count and count-mismatch rules. Publication's sequence is the `BIGSERIAL` sequence created for `wb.publication_actions.id`.
- `wbcabinet/repository/postgres` uses one scanner for common cabinet fields and digest checks. Stored-cabinet reads append the token destination; ordinary cabinet reads omit it.

## Related Memories
- Architecture overview: `mem:architecture/overview`
- Feature modules breakdown: `mem:features/modules`
- Pipeline lifecycle: `mem:pipeline/lifecycle`
- Database schema: `mem:database/schema`
- WB Client: `mem:core/wb_client`
- PostgreSQL refactoring plan: `mem:architecture/postgres_refactoring_plan`
