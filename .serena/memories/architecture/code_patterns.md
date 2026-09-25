# Code Patterns & Implementation Mechanics

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
  - Rate limits partitioned per cabinet and API bucket (Content, General, Prices).
  - Dynamic adaptation: observes `X-Ratelimit-*` headers on responses and updates rate/burst or sets `blockedUntil`.
  - Bounded queue: uses `waiters chan struct{}` to reject callers with `ErrLimiterQueueFull` when backlog overflows.
- **Strict Safe-Retry Policy (`policy/retry.go`)**:
  - Two modes: `RetryModeNever` and `RetryModeReadSafe`.
  - Only idempotent `GET` reads allow automated retries with exponential backoff.
  - Mutations (`POST`, `PUT`, `DELETE`) strictly use `RetryModeNever`. If network drops or timeouts occur, operation fails immediately without retry to prevent double creation.
- **Crash Recovery & Reconciliation**:
  - `ProductDispatcher.ProcessPending` begins by calling `ListInterruptedProductActions` to re-verify actions left in-flight during a service crash before picking up new work.

## 4. Telegram UI Persistence & Throttling (`internal/core/transport/telegram`)
- **Persistence (`MenuStore`)**:
  - Backed by table `wb.telegram_active_screens`.
  - Stores `(user_id, last_message_id, screen_name)`.
  - Avoids orphaned inline keyboards; edits existing message instead of sending new spam.
- **Throttling**:
  - `menuUpdateInterval = 1500ms`: prevents Telegram HTTP 429 rate limit errors.
  - `menuTextUnitLimit = 4096`: enforces Telegram message size constraints with automatic truncation marker.

## 5. Dynamic Cabinet Hot-Reload (`internal/feature/wbcabinet`)
- **Candidate Pattern (`CredentialCandidate`)**:
  - Unverified tokens are encapsulated in `CredentialCandidate` and cannot be used by feature modules.
  - Once verified via JWT claims + Content `/ping` + General `/seller-info`, `wbcabinet` calls `runtime.Upsert(candidate)`.
  - Dynamically updates active `Clientset` in-memory without service restart.
- **Encryption**: Tokens are encrypted using AES-GCM before writing to `wb.api_cabinets`.

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
  - `internal/core/transport/telegram.NotifyServiceError(ctx, err, extraCases...)` auto-matches core errors to Russian localized user notifications, collapsing repetitive switch-case error blocks across features into single-line handlers.

## Related Memories
- Architecture overview: `mem:architecture/overview`
- Feature modules breakdown: `mem:features/modules`
- Pipeline lifecycle: `mem:pipeline/lifecycle`
- Database schema: `mem:database/schema`
- WB Client: `mem:core/wb_client`
