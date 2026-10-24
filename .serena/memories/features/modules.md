# Functional Business Modules (`internal/feature`)

## Architectural Pattern
Every feature module is encapsulated in `internal/feature/<name>` and follows a strict 3-tier internal layout:
- `transport/`: External adapters — either Telegram menus/handlers (`transport/telegram/`) or WB client adapters (`transport/wb/`).
- `service/`: Core business logic, domain models, validations, and state machines.
- `repository/`: PostgreSQL queries. Cardimport keeps pre-finalization state in `service/workflow`; only `PersistFinalized` uses a UoW and explicit `DBTX`. Wbcabinet verified upsert uses service-owned UoW with explicit `DBTX`. Statistics repository is read-only; positive verification goes through publication-owned UoW.
Only Excel import creates batches; external WB-cabinet batch creation was removed. Features expose public facades via `feature.go` and interact solely through Go interfaces defined in the composition root (`cmd/wb-service/main.go`).

---

## 1. `wbcabinet` (WB Cabinet & Credential Management)
- **Role**: Full lifecycle of Wildberries seller accounts and API tokens.
- **Key Subsystems**:
  - `service/service.go`: CRUD operations (`Add`, `Delete`, `List`, `Restore`).
  - Core `wb/credentials.ParseToken` decodes token claims and permissions; `ValidateTokenShape` centralizes token size and basic format checks for both parsing and client candidate construction. `service` owns cabinet name validation (128 runes); `transport/wb/verifier.go` checks Content `/ping` and prepares runtime activation. Invalid-token and expired-token errors remain distinguishable for status transitions.
  - `repository/postgres/repository.go`: Persists cabinet entities in `wb.api_cabinets`.
  - `transport/telegram/`: Menus for adding, listing, and deleting seller cabinets dynamically.
- **Invariants**:
  - Tokens are never logged in plain text. Verification maps Content `/ping` HTTP 401/403 to token rejection, 429 to a rate-limit category, and HTTP 5xx, transport failures, and malformed successful ping payloads to transient failure. `service.Add` preserves the verifier's category; the Telegram menu shows a transient message separately from an invalid-token message. Restore keeps transient failures pending for retry.
  - `Service.Add` verifies WB before its UoW, calls `Repository.UpsertVerified(ctx, DBTX, credential)` inside the transaction, and activates the prepared runtime client only after successful commit. The repository no longer owns a UoW.
  - Verified cabinets are dynamically registered into `wbClientset` without restarting the process.
  - Background loop `RunRestoreRetry` retries only cabinets whose startup restoration failed transiently, tracked by cabinet ID and generation. Active clients are not pinged/replaced every tick. Expiry still removes runtime access and marks the stored cabinet expired. Restoration rechecks the current persisted generation/name under the same mutation lock as Add/Delete, avoiding stale-token resurrection; successful Add and Delete clear pending restoration. Permanently failed cabinets require explicit onboarding. Regression tests cover pending-only retries, expiry, token rotation and deletion between listing and restoration; verification tests reject incomplete/ambiguous catalog matches.
  - Startup `Initialize(ctx)` performs `Restore(ctx)` from `wb.api_cabinets`. Static env cabinet parsing is removed.
  - Initial administrator cabinets are seeded using `scripts/bootstrap_cabinets.sql` via `make cabinet-bootstrap`.

## 2. `cardimport` (Excel Ingestion & Batch Finalization)
- **Role**: Parses Telegram XLSX uploads into immutable batches consumed by transfer.
- **Runtime**: `service/workflow/memory.go` owns collecting sessions, file bytes, parsed cards/issues and revisions under one mutex. `Begin` verifies the author exists through a read-only repository query, preserving the former DB admission rule without storing the session. Random high positive process prefix plus increasing counter produces session/file IDs and preserves file order. The in-process recovery scheduler retries eligible files; restart drops unfinished sessions, so users re-upload.
- **Finalization**: `service/workflow/memory_final.go` checks all active files valid, no blocking issues and at least one card; computes source group keys and `BatchChecksum`, then freezes the session while `repository/postgres.PersistFinalized` inserts session/file provenance and immutable batch/items in one UoW. A failed transaction releases the freeze for retry. A successful transaction marks the in-memory session finalized; replay is available while that terminal session remains in memory (pruned on a later Begin after 24 hours), and batch/items stay durable for transfer.
- **Storage**: Fresh-install migration `000003_card_import` creates only finalized provenance and batch tables; temporary issues/items and uploaded file content remain in process memory until Finalize. `wb.card_import_sessions` and `wb.card_import_files` are written only inside successful Finalize for downstream ownership/provenance joins. `wb.card_batches` and `wb.card_batch_items` remain immutable. Transfer discovers finalized batches on startup and its timer; cardimport does not notify transfer after Finalize.
- **Invariants**: Aggregation merges rows with the same trimmed vendor code within one file. `Continue` discards row-error cards, then duplicate vendor codes, then duplicate barcodes in that order; it retains warnings for surviving files. `SourceGroupKey` scopes named groups to a source file and an empty group to vendor code. `BatchChecksum` includes purpose, group count, ordered positions, file/row provenance, group key, vendor code and typed payload; it does not include schema or normalization versions. WB category validation remains in cardprepare. `Purpose`/`PurposeTransfer` and `MaxBatchPageSize` live in cardimport service model; `ReadyBatchHeader` omits purpose because only validated transfer batches are exposed. The cards menu exposes only Excel transfer.

## 3. `cardprepare` (WB Schema Adaptation & Validation)
- **Role**: Stateless calculation & validation engine that checks cards against target category rules and generates WB-compliant upload proposals.
- **Key Subsystems**:
  - `service/preparation/catalog.go` & `catalog_cache.go`: Queries WB category rules, required characteristics, and constraint dictionaries, caching responses in memory. `transport/wb/catalog.go` selects only the required Content operations through the shared core executor registry.
  - `service/preparation/upload_validation.go`: Validates product characteristics, dimensions, and barcodes against target cabinet category constraints. Rejected preparation is logged with bounded product context (vendor code, source rows, category, group size); long titles include normalized actual/max rune counts, unsupported `18+` includes parsed input, and invalid directory values include characteristic identity, offending value, directory count and a bounded sample. The log remains diagnostic only; no source or outcome is changed. Diagnostic strings and directory samples are rune-bounded to keep log entries manageable; a focused unit test covers the invalid-directory diagnostic payload. Formatting is Go-standard.
  - `service/workflow/processor.go`: Implements `Processor()` invoked by transfer poller; processes `PreparationPendingGroup` directly using bounded keyset batches and a per-transfer in-memory cursor, eliminating `PreparationWork`, `WorkStatus`, `PreparationID`, and `PreparationGroupID`.
  - `repository/postgres/`: Pure read repository (`proposal.go`) loading proposals for planning; zero write queries (result persistence and artifact storage are managed atomically by `transfer`).
  - `repository/postgres/`: Manages immutable proposal artifacts in `wb.card_preparation_artifacts (group_target_id)` and reads proposals directly from `wb.transfer_items` without intermediate tables.
- **Invariants**:
  - `transfer` is the sole owner of state; separate `wb.card_preparations`, `wb.card_preparation_groups`, and `wb.card_preparation_items` are eliminated.
  - Preparation results and artifacts are committed atomically with transfer group projection via `ApplyTx` in a single UoW transaction.
  - Items failing validation are marked `rejected` or `unresolved` with detailed error codes, preventing malformed payloads from reaching WB Content API.

## 4. `cardpublication` (Batch Dispatch, Media & Error Feed)
- **Role**: Slices prepared cards into publication batches, sends requests to Content API, tracks media visibility, ingests error feeds, and handles manual resolutions.
- **Detailed Reference**: See dedicated memory `mem:features/cardpublication`.
- **Key Components**: `planner.go`, `product_dispatcher.go`, `media_dispatcher.go`, `media_visibility.go`, `error_feed.go`, `manual.go`.

## 5. `transfer` (Central Pipeline Orchestrator)
- **Role**: Master State Machine coordinating the end-to-end multi-cabinet transfer lifecycle.
- **Detailed Reference**: See dedicated memory `mem:features/transfer`.
- **Key Components**: `workflow/service.go`, `polling.go`, `authorization/automatic.go`, `preparation/result.go`, `publication/planning.go`, `publication/execution.go`.

## 6. `statistics` (Metrics, Reporting & Attention Resolution)
- **Role**: Real-time progress tracking, user alerting, and post-transfer analytics.
- **Key Subsystems**:
  - `service/service.go`: Computes aggregate progress, success/failure counts, and terminal outcome projections.
  - `transport/telegram/`: Generates and sends completion alerts with attached `.xlsx` summary reports.
  - `RunAutoAttentionResolver`: Background worker polling every 10s to resolve positively verified cards. `transport/wb/verifier.go` uses only the core Content CardsList operation for catalog search. Missing cards remain in attention; neither background nor Telegram verification automatically requeues creation on absence. The unused RequeueCardForCreation service/repository path was removed, including its direct resets of publication state. Verification contracts belong to statistics/service and use complete, exact core catalog search; ambiguous matches fail.
  - `repository/postgres/`: Queries aggregations across `transfers`, `transfer_item_targets`, and `publication_error_correlations`. Cabinet progress `Errors` counts item targets with terminal non-success overall outcome or non-success item outcome, including media outcome; it is not the number of ERROR log lines or failed HTTP requests. A rejected preparation group terminates all its member item targets, so one group rejection can add multiple task errors. Statistics owns read-only attention/report queries. Positive WB verification delegates to `cardpublication.VerifiedCardResolver`, passing a complete catalog card and observation time. The resolver rechecks the action/member/revision under UoW, records a post-submission observation and attribution, updates publication member/identity/action/plan, then invokes transfer's `CorrectWithin` in the same transaction. Media outcome remains an independent contributor to group/transfer outcome.

## 7. `users` (RBAC & Cabinet Access Control)
- **Role**: Manages Telegram user identities, permissions, and cabinet ownership.
- **Key Subsystems**:
  - `service/service.go`: User CRUD, role assignment, authentication checks.
  - `repository/postgres/`: Table `wb.users`; cabinet ownership is stored directly in `wb.api_cabinets.owner_tg_id`.
  - `transport/telegram/`: Admin commands for managing users, approving registrations, and assigning roles (`admin`, `manager`, `partner`).
- **Invariants**:
  - Initial system admin is bootstrapped from environment variables (`ADMIN_TG_ID`) at startup.
  - Role-based middleware blocks unauthorized users before any feature handler executes.

---

## Related Memories
- Transfer Orchestrator deep dive: `mem:features/transfer`
- Publication & Media deep dive: `mem:features/cardpublication`
- Architecture overview: `mem:architecture/overview`
- End-to-end pipeline: `mem:pipeline/lifecycle`
- Database schema: `mem:database/schema`
- WB Client: `mem:core/wb_client`
