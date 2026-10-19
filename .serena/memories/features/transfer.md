# Transfer Orchestrator Feature (`internal/feature/transfer`)

## Architectural Role
The central State Machine and pipeline coordinator of `wb-service`. It tracks the end-to-end progress of transferring a batch of product cards to one or more target Wildberries cabinets.

## Key Types & State Machine (`service/model/models.go`)
- **Phases (`Phase`)**:
  - `PhaseInitializing`: Target cohort selection and canonical `TargetSetRoot` computation.
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

## Critical Data Structures & Math
- **Capacity Policy (`model/capacity.go`)**:
  - Prevents combinatorial explosion (`Items * Targets = ItemTargets`, `Groups * Targets = GroupTargets`).
  - Strict integer overflow checking (`checkedMultiply`) before allocating records in DB.
- **TargetSetRoot (`model/root.go`)**:
  - Cryptographic canonical SHA-256 (`"transfer-target-set:v4"`) over sorted raw seller IDs, revisions, credential expiration timestamps, and content capabilities.
  - Invariant: Prevents silent target mutation or permission drift mid-transfer.

## Publication Authorization
- Live target authorization requires the cabinet owner to be a partner or administrator.

## Shared Identity Contract
- TargetCredential aliases core `domain.CabinetAccess`; cabinet and transfer adapters share it without field-by-field mapping. Transfer registry owns cohort selection (partner/admin fan-out), snapshot hashing and MutationTarget position. Its transport only reads credentials by owner, role or all cabinets; it contains no role-selection policy. The unused in-memory snapshot override was removed; snapshots always use current credentials. The target root and snapshot revision use version `v4`, hash each `SellerID` once and do not include display names.

## Subsystems, Functions & Contracts
1. **Workflow Service (`service/workflow/service.go`)**:
   - `Start(ctx, batchID) (TransferID, error)`: Initiates fanout transfer across default cohort.
   - `StartToCabinet(ctx, batchID, sellerID) (TransferID, error)`: Initiates directed transfer to single target cabinet.
   - `Initialize(ctx, transferID) error`: Transitions transfer from `PhaseInitializing` -> `PhasePreparing`. Seeds `transfer_items`, `transfer_group_targets`, and `transfer_item_targets`.
   - **High-Performance Seeding**: Repository pre-allocates PostgreSQL sequences in bulk through `core/repository/postgres/sequence.ReserveIDs` and its transfer-specific `reserveIDs` wrapper and streams thousands of rows via `pgx.CopyFrom`.
2. **Polling Engine (`server/polling.go`)**:
   - `Run(ctx, onError, processors...)`: Runs independent concurrent goroutines per stage under `sync.WaitGroup`.
   - `ProgressAwareProcessor`: Optional interface (`ProcessPendingWithProgress(ctx) (bool, error)`). Polling engine triggers downstream wakes (`stageWakes` / `wakeDownstream`) strictly when real forward progress occurs (`progress == true`); empty or non-advancing passes do not wake downstream stages, preventing wake storms.
   - Per-processor mutexes (`processMu sync.Mutex`) prevent concurrent overlap on the same stage.
   - Polling loop sequence:
     1. `cardprepare.Processor()`: Evaluates pending items, outputs preparation proposals.
     2. `AutomaticAuthorizationProcessor()`: Verifies live tokens, creates `wb.transfer_live_authorizations`.
     3. `cardpublication.Processor()`: Plans incremental batches into `wb.publication_plans`.
     4. `cardpublication.ProductDispatcher()`: Submits cards to `/content/v2/cards/upload`.
     5. `cardpublication.ErrorFeed()`: Pulls `/cards/error/list` and correlates errors.
3. **Preparation Coordination & Appliers**:
   - `ListPendingPreparationGroups(ctx, transferID, limit)`: Exposes pending preparation work directly from `wb.transfer_group_targets` using fair window partitioning (`ROW_NUMBER() OVER (PARTITION BY group_target.target_id ORDER BY group_target.id) AS cabinet_rank`). Interleaves target cabinets in round-robin fashion, preventing starvation and advancing past transient errors without busy loops.
   - `PreparationResultApplier`:
     - `Apply(ctx, command)`: Standalone transactional applier.
     - `ApplyTx(ctx, tx, command)`: Called by `cardprepare` inside the outer Unit of Work transaction to atomically save preparation artifacts into `card_preparation_artifacts (transfer_id, group_target_id)` and update `transfer_group_targets` projections (status, outcome_code, item_position, field, started_at, finished_at) in a single commit.
     - **Concurrency & Idempotency**: Secures row locks via `SELECT ... FOR UPDATE`. Replays with identical payload and diagnostics return `nil` (idempotent no-op). Conflicting replays reject with `ErrPreparationResultConflict`.
   - `Outcome Aggregation & Transitions (`repository/postgres/outcome.go`)`:
     - Centralizes SQL outcome expressions (`groupOutcomeCountsCTE`, `groupToTransferOutcomeExpression`) and lifecycle transitions (`advanceTransferAfterPreparation`, `finishPublicationTransfer`, `reduceTransferAfterPlanning`).
   - `PublicationPlanResultApplier.Apply(...)`: Binds group targets to `publication_plan_id`. `PublicationPlanningGroup` references groups directly via `GroupTargetID` (legacy `PreparationGroupID` removed).
   - `PublicationExecutionResultApplier`:
     - `ApplyActionResult(...)`: Commits upload outcome, sets item targets to `terminal` with NMID.
     - `ApplyMediaResult(...)`: Marks group media status.
     - `CorrectPublicationItemResult(...)`: Applies evidence-based correction for terminal publication items from manual or verified-WB resolution; recalculates group and finished transfer outcome in the same transaction. Group correction preserves unresolved/rejected media status as unresolved/partial instead of promoting the group to success solely because the card exists.

## Database Schema (`wb` namespace)
- `wb.transfers`: Master transfer record (source batch ID, current phase, outcome, owner).
- `wb.transfer_targets`: Target cabinets attached to transfer, identified by a single `seller_id` column plus a separate `client_generation` snapshot.
- `wb.transfer_items`: Items attached to transfer.
- `wb.transfer_group_targets`: Group-level target progress tracking with foreign key to `wb.publication_plans` (migration 000016).
- `wb.transfer_item_targets`: Granular item-level projections per target cabinet.
- `wb.transfer_live_authorizations`: Token leases per transfer and publication plan.

## Invariants & Guardrails
- **Atomicity**: All transitions run inside Unit of Work (`uow.WithinTransaction`).
- **Terminal Immutability**: Once an item projection reaches `terminal`, its outcome changes only through formal evidence-based correction (manual decision or verified WB catalog evidence), with expected revision/outcome checks and UoW.

## Related Memories
- Target transfer simplification & state consolidation plan: `mem:architecture/transfer_simplification_plan`
- End-to-end pipeline lifecycle: `mem:pipeline/lifecycle`
- Database schema: `mem:database/schema`
- Architecture overview: `mem:architecture/overview`
