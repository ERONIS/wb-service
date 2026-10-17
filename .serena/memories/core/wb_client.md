# Wildberries Core Client (`internal/core/transport/wb`)

## Architecture & Responsibilities
- Single process-wide client manages Content (`content-api.wildberries.ru`) and Prices (`discounts-prices-api.wildberries.ru`) APIs. Unused General API clients, accessors, policies and DTOs were removed.
- Package layout follows client-go style:
  - Direct domain binding: `CabinetID` and `ClientGeneration` come directly from `internal/core/domain/wb_identity.go`. `credentials.go` deleted entirely.
  - `clientset.go`: `NewClientset(ctx, logger)` delegates to `NewClientsetWithHTTPClient` with a 45s default timeout; the latter accepts an injected HTTP client and builds the rate limiter registry before acquiring shared transport. Production Content and Prices URLs are created directly from fixed HTTPS hosts when constructing executors; they are not stored in `Clientset` or parsed at runtime. `options.go`, `ClientOptions`, and envconfig were deleted entirely. `NewCandidate(id, name, token)` validates and builds credentials for dynamic activation (`ActivateCandidate`, `RemoveCabinet`); Content and Prices executors are built through one service specification loop. Candidates expose generation and the Content executor for verification; the unused name accessor was removed. The registry shares a connection pool and re-exports `CabinetID` and `ClientGeneration` for transport-level callers.
  - `Clientset` stores the Content and Prices `client.Executor` values directly in each immutable candidate; there is no `CabinetClient` wrapper. Authorization headers and flow control are bound when those executors are constructed.
  - `flowcontrol/`: Token bucket rate limiting, rate admission, header observation (`X-Ratelimit-*`).
  - `policy/`: Safe retry policies with exponential backoff.
  - `transport/`: Underlying HTTP roundtripper, metrics, connection pooling.

## Cabinet Lifecycle & Dynamic Loading
- Static cabinet env parsing is removed.
- At application startup, `internal/feature/wbcabinet.Initialize(ctx)` calls `service.Restore(ctx)` to load active cabinets from PostgreSQL (`wb.api_cabinets`) and dynamically register them into `Clientset`.
- A transient `/ping` verification failure leaves the cabinet active in PostgreSQL but absent from `Clientset`; the background restore worker retries pending cabinets every 30 seconds. Until activation succeeds, database-derived publication targets can reach the error-feed poller and fail with `get WB cabinet executor: WB cabinet: not found`. The feed uses `errgroup.WithContext`, so one such failure can cancel concurrent cabinet queries and surface additional `context canceled` SQL/transport warnings. Verification currently wraps all prepare errors with `ErrVerification` (`unauthorized`), including transient transport failures; inspect the nested cause before diagnosing token rejection.
- New cabinets at runtime are added exclusively via Telegram (`feature/wbcabinet`) or initial database seed (`scripts/bootstrap_cabinets.sql`).

## Operational Rules
- **Core Retry Policy**: Automatic retries depend on `OperationKindRead` plus `RetryModeReadSafe`, not HTTP method. Content operation specs use local `safeGetJSON`, `safePostJSON`, and independent `mutationPostJSON` constructors for common shapes; ping remains an explicit no-retry read, and multipart upload remains explicit. Dynamic subject paths still validate IDs before constructing an operation. Catalog/error-list reads use POST and can retry. Mutation operations use `RetryModeNever` in the core executor. Publication sends each media link/file mutation once per business attempt; only external media downloads use its local retry loop. Ambiguous mutation outcomes are classified for reconciliation rather than automatically resent.
- **Rate Admission**: Calls pass through `flowcontrol` token buckets before hitting network.
- **Bounded Reading**: Response reading is strictly bounded by size limits to prevent memory exhaustion from oversized WB payloads.
- **Unified Logging**: Exactly one structured Zap log entry per logical request capturing attempt count, status, latency, and delivery state.

## Registry Consistency
- Clientset stores a single immutable candidate value per cabinet. Content and Prices accessors return the executor from that entry together with the lookup error. Pinned lookup checks generation and retrieves the executor from the same entry under one read lock; token rotation cannot substitute a different generation between these steps. A mismatch returns `ErrClientGenerationChanged` (core conflict). Already acquired executors remain usable; removal does not cancel in-flight calls.

## Catalog Traversal
- `wb/catalog` owns active/trash pagination, cursor-cycle detection, nmID deduplication, cancellation, and bounded page counts. `ErrPageLimit` means an incomplete search, never proven absence. `FindByVendorCode` performs exact matching over the complete search result and preserves multiple distinct matches.

## Integration Boundaries
- Composition root constructs shared `wb.ContentClient` and `wb.PricesClient` instances, which own typed endpoint calls, including pinned mutations and multipart encoding. Preparation receives ContentClient directly; editing adds cabinet ownership selection; publication composes ContentClient with its separate MediaDownloader. Cabinet copying also consumes typed Content/Prices clients directly. Feature adapters must not duplicate endpoint wrappers.
- `wb/credentials.ParseToken` owns token format, canonical seller claim normalization, expiry and permission-bit decoding. Parsing does not verify authenticity; wbcabinet verifies the exact token via Content `/ping`. Invalid and expired token errors are distinct sentinels wrapping core invalid-argument. Cabinet ownership, persistence and activation remain in the feature.
- `CabinetID` identifies a local cabinet; `SellerKey` identifies its seller; `ClientGeneration` fingerprints the exact token. Generation checks do not establish seller ownership or publication authorization.
- Feature-specific catalog observations, reconciliation decisions, and publication outcomes remain business concerns. Media downloading in publication is a separate unauthenticated HTTP client, outside WB API rate limiting.

## Regression Checks
- Transport unit tests previously located in `api/content/v1/operations_test.go`, `catalog/cards_test.go`, and `credentials/token_test.go` were pruned. Integration and regression coverage remains in feature layers (`cardpublication`, `transfer`, `wbcabinet`). No live WB access is needed.

## Related Memories
- Overall architecture: `mem:architecture/overview`
- Cabinet management & verification: `mem:features/modules`
- Transfer pipeline execution: `mem:pipeline/lifecycle`
- Operations & Build: `mem:operations/run_and_build`
