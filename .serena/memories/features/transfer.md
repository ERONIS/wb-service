# Transfer Orchestrator Feature (`internal/feature/transfer`)

## Architectural Role
The central State Machine and pipeline coordinator of `wb-service`. It tracks the end-to-end progress of transferring a batch of product cards to one or more target Wildberries cabinets, coordinating `cardimport`, `wbcabinet`, `cardprepare`, `cardpublication`, `statistics`, and `users`.

## Operational Modes & Configuration (`transport/config/config.go`)
- **Modes (`Mode`)**:
  - `ModeDisabled` (`"disabled"`): Transfer engine disabled; live authorization and mutation dispatch return `ErrLiveModeDisabled`.
  - `ModeDryRun` (`"dry-run"`): Validation, schema preparation, and planning run, but live API mutations are skipped.
  - `ModeLive` (`"live"`): Full production execution with live token leases, WB Content API dispatch, media upload, and reconciliation.
- **Environment Variables**:
  - `TRANSFER_MODE`: Execution mode (default: `"disabled"`).
  - `TRANSFER_COHORT_NAME`: Name of the mutation cohort (default: `"production"`).
  - `TRANSFER_POLL_INTERVAL`: Polling tick interval (default: `3s`).
  - `TRANSFER_AUTHORIZATION_TTL`: Maximum validity duration for a live token lease (default: `1h`, max `24h`).
  - `TRANSFER_MAX_ITEMS` (50 000), `TRANSFER_MAX_GROUPS` (50 000), `TRANSFER_MAX_TARGETS` (20), `TRANSFER_MAX_ITEM_TARGETS` (1 000 000), `TRANSFER_MAX_GROUP_TARGETS` (1 000 000): Capacity safety thresholds.

## Key Types & State Machine (`service/model/models.go`)
- **Phases (`Phase`)**:
  - `PhaseInitializing`: Target cohort selection and persistence of target rows; no target-set root or snapshot digest.
  - `PhasePreparing`: Schema validation via `cardprepare`.
  - `PhaseAwaitingAuthorization`: Acquiring live token leases on target cabinets.
  - `PhasePublishing`: Batch dispatch via `cardpublication`.
  - `PhaseReconciling`: Error feed ingestion and correlation.
  - `PhaseMedia`: Media attachment and catalog visibility checks.
  - `PhaseFinished`: Final projection into terminal outcomes.
- **Master Outcome (`Outcome`)**:
  - `running`, `succeeded`, `partial`, `rejected`, `unresolved`, `failed`, `cancelled`.
- **Item Projections (`ItemTargetProjection`)**:
  - States: `pending` -> `running` -> `terminal`.
  - Result classes: `success`, `skipped`, `rejected`, `partial`, `unresolved`, `internal_error`.
  - Invariants: Terminal states require `StartedAt`, `FinishedAt`, and valid `OutcomeCode`. `AttentionClosedAt` is only valid for `unresolved` or `internal_error`.

## Critical Data Structures & Identity
- **Capacity Policy (`model/capacity.go`)**:
  - Prevents combinatorial explosion (`Items * Targets = ItemTargets`, `Groups * Targets = GroupTargets`).
  - Strict integer overflow checking (`checkedMultiply`) before allocating records in DB.
- **Target snapshot (`registry/registry.go`)**: Holds selected target rows with seller ID, client generation, binding/capability revisions, expiry, and permissions. Transfer persists these rows; immutability and current credential checks use database state and numeric IDs, without a root or snapshot hash.

## Transfer Variants & Ingestion
1. **Fanout Transfer (`Start(ctx, batchID)`)**:
   - Broadcasts the batch to all cabinets belonging to the configured cohort (cabinets owned by users with `RolePartner` and `RoleAdmin`).
   - Dimension: `Items × N_targets`, `Groups × N_targets`.
2. **Directed Transfer (`StartToCabinet(ctx, batchID, sellerID)`)**:
   - Directed transfer to a single target cabinet owned by or accessible to the batch author.
   - Dimension: `Items × 1`, `Groups × 1`.
   - When concurrent creation finds an existing transfer inside UoW, target seller IDs are read through `ListTargetSellerIDsWithin(ctx, tx, transferID)` on the same `DBTX`; the already committed fast path uses pool-backed `ListTargetSellerIDs`. Both paths share the same set comparison.
3. **Scheduled Discovery**:
   - Polling engine stage 0 (`Service.ProcessPending`):
     - `resumeInitializing(ctx, onProgress)`: Resumes initialization for any transfers interrupted in `PhaseInitializing` (e.g. during pod restart).
     - `discoverFinalizedBatches(ctx, onProgress)`: Scans ready batch headers from cardimport, advancing `batchCursor`. Purpose filtering belongs to cardimport; `ReadyBatchHeader` has no purpose field.
     - `Initialize(ctx, transferID)` returns `(progress bool, error)`: a transfer outside `initializing` or one persisted as terminal initialization failure does not produce downstream preparation work; successful initialization returns progress. `ProcessPendingWithNotify` signals downstream after the preparation-ready transition. A workflow test verifies terminal failure does not wake preparation.
   - `RunPolling` constructs `server.NewPolling` without an external wake channel. Transfer scans finalized batches once at startup, then on each `TRANSFER_POLL_INTERVAL` tick (default 3s). Cardimport finalization commits the batch but sends no processing signal; restart resumes discovery from PostgreSQL.

## Ready Batch Boundary
- `cardimport.Service` exposes `GetReadyBatchHeader`, `ListReadyBatches`, and `GetReadyBatch`. Transfer consumes `cardpipeline.ReadyBatchHeader` and `ReadyBatchItem`, which contain batch identity, author Telegram ID, card payload, item position, and opaque group key, without import session/file IDs or separate source rows.
- `GetReadyBatch` loads immutable items, checks their order and validates each item before projecting ready items. Transfer compares batch item/group counts during initialization; no batch checksum or schema/normalization version compatibility gate remains. The finalized batch tables and transfer snapshots are immutable, and transfer discovery still resumes from PostgreSQL after restart.
- Transfer initialization stores card payload and grouping, not copied import file ID or source rows. The group key remains necessary to keep file-scoped card groups distinct.

## Live Authorization Subsystem (`service/authorization/`)
- **Role**: Guarantees valid, unrevoked token leases before any card payload is dispatched to WB Content API.
- **Lifecycle States (`LiveAuthorizationState`)**:
  - `requested` -> `authorized` -> `revoked` / `expired` / `superseded` / `closed`.
- **Automatic Authorization (`AutomaticAuthorizationProcessor`)**:
  - Depends on the narrow `AutomaticAuthorizationPlanSource` interface; the full `LivePlanSource` remains for manual/live authorization service operations.
  - Runs after publication planning and before product dispatch in the polling loop (stage 3 when counting transfer discovery as stage 0).
  - Scans `ListAutomaticAuthorizationPlans` for plans ready for dispatch.
  - Automatically authorizes leases on behalf of the batch author using deterministic idempotency keys (`auto:authorize:<transfer_id>:<plan_id>:<revision>:<auth_id>`). Implements `ProcessPendingWithNotify` and signals downstream only after successful authorization.
- **Pre-Dispatch Verification (`LockValidLive`)**:
  - Called under transaction by `cardpublication.ProductDispatcher` immediately before submitting data to `/content/v2/cards/upload`.
  - Verifies token lease is in `authorized` state, unexpired, bound to the same numeric plan/action/target IDs, and matches the current seller and client generation.

## Polling Engine & Stage Execution Sequence
- `RunPolling(ctx, onError, processors...)` runs independent stages with mutex protection (`processMu sync.Mutex`) and wake propagation:
- Sequence of stages wired in `cmd/wb-service/main.go`:
  0. `transferFeature.Service()`: resumes initialization and discovers finalized batches.
  1. `cardprepareFeature.Processor()`: prepares pending group targets.
  2. `cardpublicationFeature.Processor()`: creates incremental publication plans.
  3. `transferFeature.AutomaticAuthorizationProcessor()`: creates live authorization leases.
  4. `cardpublicationFeature.ProductDispatcher()`: dispatches card uploads.
  5. `cardpublicationFeature.ErrorFeed()`: ingests the WB error feed periodically.
- `server/polling.go` starts stages concurrently. Transfer initialization, committed preparation batches, saved publication plans, and successful automatic authorizations notify only the immediately following stage through buffered wake channels; all stages also poll on the timer. A three-stage polling test verifies that a stage without progress stops the wake chain. Error-feed and product-dispatch stages currently use only `ProcessPending`. Automatic authorization reads bounded keyset pages ordered by `(transfer.id, plan.id)` and advances its in-memory cursor after each page even when individual plans fail. It wraps to the beginning at the end of the eligible set, preventing a failing prefix from starving later plans; process restart resets the cursor; retry cooldown is not persisted. A focused processor test covers a failing full page followed by a later successful plan and wraparound.
- Independent background workers:
  - `cardpublicationFeature.RunMediaPolling()`: Verifies catalog visibility and uploads media (`PhaseMedia`).
  - `statisticsFeature.RunAutoAttentionResolver()`: Background catalog search resolving positive matches.

## Preparation Coordination & Appliers
- `ListPendingPreparationGroups(ctx, transferID, limit, after)`:
  - Exposes pending groups directly from `wb.transfer_group_targets`.
  - Uses keyset pagination ordered by `(source_group_id, target_id)` over running group targets. `cardprepare.Processor` stores an in-memory cursor per transfer and advances past transient failures until wraparound; this prevents a failing page from blocking later pages but does not implement cabinet-rank round-robin.
- `PreparationResultApplier`:
  - `ApplyTx(ctx, tx, command)`: Atomically saves preparation artifacts into `wb.card_preparation_artifacts (group_target_id)` and updates `wb.transfer_group_targets` projections in a single Unit of Work transaction.
  - Replays matching stored status, revision, outcome, diagnostics, and diagnostics are idempotent no-ops (`nil`); the full artifact is not compared. Conflicting result identities return `ErrPreparationResultConflict`.
- `PublicationPlanResultApplier.Apply(...)`: Binds group targets to `publication_plan_id`.
- `PublicationExecutionResultApplier`:
  - `ApplyActionResult(...)`: Commits upload outcome, sets item targets to `terminal` with NMID.
  - `ApplyMediaResult(...)`: Marks group media status.
  - `CorrectPublicationItemResult(...)`: Applies evidence-based correction for terminal publication items from manual or verified-WB resolution; recalculates group and finished transfer outcome in the same transaction. Group correction preserves unresolved/rejected media status as unresolved/partial instead of promoting the group to success solely because the card exists. The correction path reuses the shared group-count CTE and transfer-outcome expression from `repository/postgres/outcome.go`, including open-attention counts. Product completion, terminal-item correction, and media completion also reuse SQL fragments for item-result counts, publication status, and item-derived group outcome from `outcome.go`; each path retains its distinct phase guards, media precedence, and attention handling.

## Inter-Feature Coordination Map
1. **`cardimport`**: Provides immutable card batches and items; transfer discovers them on startup and scheduled polls.
2. **`wbcabinet`**: Provides cabinet access credentials (`TargetCredential`), permissions (`ContentRead`/`ContentWrite`), and active token validation.
3. **`cardprepare`**: Validates products against WB category attributes and produces upload proposals; results are committed atomically through transfer's `PreparationResultApplier`.
4. **`cardpublication`**: Plans batches, checks live authorization leases, dispatches cards to Content API `/content/v2/cards/upload`, uploads media to `/content/v2/cards/upload/add`, polls error feeds `/content/v2/cards/error/list`, and applies execution outcomes via transfer appliers.
5. **`statistics`**: Queries transfer projections, reports progress and errors to users via Telegram, generates Excel reports, and auto-resolves verified catalog cards via `cardpublication.VerifiedCardResolver`.
6. **`users`**: Controls user roles and cabinet ownership determining target cohorts.

## Database Schema (`wb` namespace)
- `wb.transfers`: Master transfer record (source batch ID, current phase, outcome, attention code, owner).
- `wb.transfer_targets`: Target cabinets attached to transfer, identified by `seller_id` and `client_generation`.
- `wb.transfer_items`: Frozen items copied from batch for the transfer.
- `wb.transfer_group_targets`: Group-level target progress tracking (preparation status, publication plan ID, media status, overall outcome).
- `wb.transfer_item_targets`: Granular item-level projections per target cabinet (pending -> running -> terminal, NMID, result class).
- `wb.transfer_live_authorizations`: Token leases per transfer and publication plan.
- The PostgreSQL repository's `listTransfers` centralizes validation, keyset pagination, transfer-row scanning, and error wrapping for initialization, preparation, and publication-planning transfer lists; each caller supplies a fixed phase/work predicate.
- `wb.card_preparation_artifacts`: Immutable preparation artifacts keyed directly to `wb.transfer_group_targets(group_target_id)`.

## Service Package Boundary
- `transfer/feature.go` composes `workflow`, `registry`, `preparation`, `publication`, and `authorization` directly. Transfer service, feature, repository, and transport code use types directly from their defining model/authorization/preparation/publication/workflow/registry packages. No `type X = Y` re-exports remain. Authorization sentinels and limits remain on the service boundary; repository tests use concrete owner packages.

## Invariants & Guardrails
- **Atomicity**: All state transitions run inside Unit of Work (`uow.WithinTransaction`).
- **Terminal Immutability**: Once an item projection reaches `terminal`, its outcome changes only through formal evidence-based correction (manual decision or verified WB catalog evidence), with expected revision/outcome checks and UoW.
- **Safe Retries**: Mutation endpoints (`/content/v2/cards/upload`) strictly prohibit automated retries (`RetryModeNever`).

## Related Memories
- Target transfer simplification & state consolidation plan: `mem:architecture/transfer_simplification_plan`
- End-to-end pipeline lifecycle: `mem:pipeline/lifecycle`
- Database schema: `mem:database/schema`
- Architecture overview: `mem:architecture/overview`
- Feature modules breakdown: `mem:features/modules`
