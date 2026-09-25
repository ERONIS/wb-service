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
  - Cryptographic canonical SHA-256 (`"transfer-target-set:v2"`) over sorted target cabinet IDs, seller keys, revisions, credential expiration timestamps, and content capabilities.
  - Invariant: Prevents silent target mutation or permission drift mid-transfer.

## Subsystems, Functions & Contracts
1. **Workflow Service (`service/workflow/service.go`)**:
   - `Start(ctx, batchID) (TransferID, error)`: Initiates fanout transfer across default cohort.
   - `StartToCabinet(ctx, batchID, cabinetID) (TransferID, error)`: Initiates directed transfer to single target cabinet.
   - `Initialize(ctx, transferID) error`: Transitions transfer from `PhaseInitializing` -> `PhasePreparing`. Seeds `transfer_items`, `transfer_group_targets`, and `transfer_item_targets`.
   - **High-Performance Seeding**: Repository pre-allocates PostgreSQL sequences in bulk (`reserveIDs` on sequence objects) and streams thousands of rows via `pgx.CopyFrom`.
2. **Polling Engine (`server/polling.go`)**:
   - `Run(ctx, onError, processors...)`: Runs independent concurrent goroutines per stage under `sync.WaitGroup`.
   - Supports `wake <-chan struct{}` for immediate reaction to new uploads without waiting for ticker intervals.
   - Per-processor mutexes (`processMu sync.Mutex`) prevent concurrent overlap on the same stage.
   - Polling loop sequence:
     1. `cardprepare.Processor()`: Evaluates pending items, outputs preparation proposals.
     2. `AutomaticAuthorizationProcessor()`: Verifies live tokens, creates `wb.transfer_live_authorizations`.
     3. `cardpublication.Processor()`: Plans incremental batches into `wb.publication_plans`.
     4. `cardpublication.ProductDispatcher()`: Submits cards to `/content/v2/cards/upload`.
     5. `cardpublication.ErrorFeed()`: Pulls `/cards/error/list` and correlates errors.
3. **Cross-Feature Appliers**:
   - `PreparationResultApplier.Apply(...)`: Called by `cardprepare` to mark group targets `succeeded`/`rejected`/`unresolved`.
   - `PublicationPlanResultApplier.Apply(...)`: Binds group targets to `publication_plan_id`.
   - `PublicationExecutionResultApplier`:
     - `ApplyActionResult(...)`: Commits upload outcome, sets item targets to `terminal` with NMID.
     - `ApplyMediaResult(...)`: Marks group media status.
     - `CorrectPublicationItemResult(...)`: Updates item target from error feed or manual resolution.

## Database Schema (`wb` namespace)
- `wb.transfers`: Master transfer record (source batch ID, current phase, outcome, owner).
- `wb.transfer_targets`: Target cabinets attached to transfer.
- `wb.transfer_items`: Items attached to transfer.
- `wb.transfer_group_targets`: Group-level target progress tracking with foreign key to `wb.publication_plans` (migration 000016).
- `wb.transfer_item_targets`: Granular item-level projections per target cabinet.
- `wb.transfer_live_authorizations`: Token leases per transfer and publication plan.

## Invariants & Guardrails
- **Atomicity**: All transitions run inside Unit of Work (`uow.WithinTransaction`).
- **Terminal Immutability**: Once an item projection reaches `terminal`, its outcome cannot be altered except via formal manual resolution.

## Related Memories
- Target transfer simplification & state consolidation plan: `mem:architecture/transfer_simplification_plan`
- End-to-end pipeline lifecycle: `mem:pipeline/lifecycle`
- Database schema: `mem:database/schema`
- Architecture overview: `mem:architecture/overview`
