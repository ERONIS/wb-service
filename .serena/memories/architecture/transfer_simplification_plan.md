# Transfer Orchestrator Simplification & State Consolidation Plan

## Status & Scope
- **Document Reference**: `doc/transfer-simplification-plan.md` is a historical implementation plan. Its digest/root requirements were superseded by the current ID-and-state contracts in `mem:features/transfer`, `mem:features/cardpublication`, and `mem:database/schema`.
- **Status**: Full implementation completed and verified in codebase. All 7 prioritized steps completed:
  - Step 1 (Migrations & Clean DB Schema): Migrations 000001–000007 directly define target schema (34 tables). Migration 000008 permanently deleted. Three old preparation tables (`card_preparations`, `card_preparation_groups`, `card_preparation_items`) never created; `wb.card_preparation_artifacts` directly references `wb.transfer_group_targets(transfer_id, id)`.
  - Step 2 (Diagnostics): Diagnostics (`preparation_item_position`, `preparation_field`, `preparation_started_at`, `preparation_finished_at`) stored in `wb.transfer_group_targets` and updated on completion/rejection.
  - Step 3 (Atomic & Idempotent Persistence): `ApplyPreparationResult` executes within `uow.WithinTransaction` with `SELECT ... FOR UPDATE` row locks. Identical replays return `nil` (idempotent no-op); conflicting replays return `ErrPreparationResultConflict`.
  - Step 4 (Bounded Batch Looping): `ListPendingPreparationGroups` currently uses keyset pagination by `(source_group_id, target_id)`. `cardprepare.Processor` rotates in-memory cursors past transient failures (>1000 groups) without busy loops. Cabinet-rank round-robin is not present in the current preparation query.
  - Step 5 (Progress-Linked Stage Wakes): `polling.go` checks `ProgressNotifyingProcessor` and `ProgressAwareProcessor`. The preparation processor notifies after a committed group batch, including when a later group in the same pass fails. Transfer initialization, preparation, publication planning, and automatic authorization now use `ProcessPendingWithNotify` to wake only the immediately following stage after persisted progress; wiring order is transfer, preparation, planning, authorization, product dispatch, error feed. Error-feed and product-dispatch stages use only `ProcessPending`; empty passes do not wake downstream stages.
  - Step 6 (Architecture Consolidation): `transfer` is the single state owner. `cardprepare` is a stateless calculation and validation module without its own job queues.
  - Step 7 (Verification & Metrics): Clean migration deployment to PostgreSQL container verified. Integration tests, race detector (`go test -race ./...`), concurrency tests (10 parallel workers on same batch), and recovery tests (>1000 groups with transient errors) all pass.
  - Net Reduction Achieved: ~1 900 lines of Go code and SQL eliminated (exceeding the 1 200–2 000 target). Production binary builds cleanly.
- **Core Directive**: `transfer` becomes the sole owner of the transfer process and state; `cardprepare` is converted from a separate persisted workflow into a stateless calculation/validation engine without its own database job queues.
- **Safety Boundary**: Cabinet authorization, token leases, WB rate limits, mutation retry prohibition, and audit immutability triggers remain. Plan/action/request matching now uses numeric IDs, expected revisions, payload comparison, and database state; `TargetSetRoot` was removed.

## Current Bottlenecks & Motivation
1. **Dual State Management**:
   - Preparation currently manages its own jobs in `card_preparations` -> `card_preparation_groups` -> `card_preparation_items`, and then applies results into `transfer_group_targets`.
   - Occurs across two distinct database transactions, requiring an explicit replay mechanism (`ensureSamePreparationResult`) to recover from crashes between commits.
2. **Inefficient Polling & Rescan Overhead**:
   - `processor.transferSource.ListPreparing` reads all transfer jobs, including already-finished ones (`internal/feature/cardprepare/service/workflow/processor.go:200-240`).
   - In `internal/feature/transfer/server/polling.go:76-80`, the `wake` channel is connected only to processor 0 (`cardprepare`). Downstream stages (authorization, publication, dispatch, error feed) must wait for timer ticks.
3. **Redundant Item Storage & Verification**:
   - `card_preparation_items` re-persists items from immutable source groups solely to re-verify payload variants on reading.
4. **Duplicated Outcome Calculation**:
   - Repetitive SQL aggregation queries calculate master outcomes independently across `preparation_result.go`, `publication_result.go`, and `publication_execution.go`.

## Target Architecture & State Ownership
- **Work Unit**: Group of products × target cabinet (`GroupTarget`). Ready groups advance to authorization/publication immediately without waiting for other groups in the batch.
- **State Partitioning**:
  - `wb.transfers`: Master transfer identity, frozen snapshot, overall phase/outcome, preparation timing.
  - `wb.transfer_group_targets`: Single source of truth for group target progress; maintains independent status columns for preparation, publication, and media.
  - `wb.transfer_item_targets`: Granular item status per cabinet, NMID, source action ID, attention codes.
  - `wb.card_preparation_artifacts`: Immutable preparation artifacts (WB payload, metadata snapshot, limits snapshot) bound directly to `transfer_group_targets`.
  - `wb.publication_*`: Retains plan, action, attempt, and execution audit history.

## Table Transition Matrix (25 -> 22 -> 21 Tables)
| Table | Decision | Replacement / Justification |
|---|---|---|
| `wb.card_preparations` | **Remove (Phase 1)** | Batch identity already in `wb.transfers`. Preparation metrics and timestamps tracked in `wb.transfers` or derived from groups. |
| `wb.card_preparation_groups` | **Remove (Phase 1)** | State, diagnostics (`outcome_code`, `outcome_field`, `outcome_item_position`), timestamps, and artifact links stored directly in `wb.transfer_group_targets`. |
| `wb.card_preparation_items` | **Remove (Phase 1)** | Group membership and variant positions obtained directly from frozen `wb.transfer_items`. |
| `wb.card_preparation_artifacts` | **Retain** | Bound directly to `wb.transfer_group_targets` (`group_target_id`). Preserves payload bytes, snapshots, and uniqueness without digest columns. |
| `wb.transfer_items` | **Separate Evaluation (Phase 2)** | Evaluate referencing immutable `wb.card_batch_items` directly. Kept as separate stage due to source provenance (`SourceFileID`) and audit linkages. |
| `wb.transfer_groups` & targets | **Retain** | Retains group aggregation for Excel reporting and multi-level progress tracking. |

## Speedup & Optimization Mechanics
1. **Single-Transaction Persistence**:
   - Proposal artifact generation and `transfer_group_targets` status update execute inside a single Unit of Work transaction (`uow.WithinTransaction`).
   - Eliminates intermediate states and removes the need for catch-up replay logic.
2. **Incremental Paginated Processing**:
   - Fetch only uncompleted (`pending`/`running`) group targets in small bounded batches with fair round-robin scheduling across cabinets.
   - Finished groups are excluded from subsequent polling passes.
3. **Chained Stage Wake Notifications**:
   - Successful persistence of preparation results signals downstream stages via dedicated wake channels (`wake <-chan struct{}`) instead of waiting for timer intervals.
   - Polling loop interval remains as a fallback for recovery and startup reconciliation.
4. **Centralized Outcome Aggregation**:
   - Unified helper function for item->group and group->transfer outcome calculations, retaining distinct condition guards per lifecycle phase.

## Phased Implementation Roadmap
- **Phase 0: Baseline & Test Harness**: Capture LOC baseline, SQL query counts, lock durations, and end-to-end latency benchmarks. Establish comprehensive contract tests.
- **Phase 1: Atomic Persistence in Current Schema**: Unify preparation result save and transfer projection into a single transaction; retain old tables during transition.
- **Phase 2: Transfer-Driven Coordination**: Transfer feature selects pending work and invokes `cardprepare.Preparer` as a calculation library. Deprecate separate `cardprepare` coordinator.
- **Phase 3: Schema Migration & Table Pruning**: Rebind `card_preparation_artifacts` to `transfer_group_targets`. Switch proposal loading to `transfer_items`. Drop the 3 preparation tables via a sequential migration (`000008_...`).
- **Phase 4: Chained Wake & Work Partitioning**: Implement paginated uncompleted queries and inter-stage wake signaling in `polling.go`.
- **Phase 5: Unified Outcome Calculation & Cleanup**: Centralize outcome calculation logic; remove obsolete DTOs, mappers, and forwarding adapters.
- **Phase 6: Independent Evaluation of `transfer_items`**: Assess direct references to `card_batch_items` without breaking source provenance or report schemas.

## Invariants & Compliance Rules
- **No Degradation of WB Client Safety**: Mutation endpoints (`/content/v2/cards/upload`) strictly prohibit automated retries (`RetryModeNever`).
- **Current Identity Rule**: `TargetSetRoot`, `semantic_digest`, `metadata_digest`, and `proposal_root` were removed. Use transfer/plan/action/attempt IDs, expected revisions, explicit states, and persisted payloads for the relevant checks.
- **Russian Code Comments**: All new or modified Go code and comments must be strictly in Russian.
- **Post-Modification Memory Sync**: Every subsequent code change implementing this plan requires immediate Serena memory updates.

## Related Memories
- Master Architecture & Topology: `mem:architecture/overview`
- Implementation Mechanics: `mem:architecture/code_patterns`
- Transfer Feature Internals: `mem:features/transfer`
- Publication Pipeline: `mem:features/cardpublication`
- Pipeline Lifecycle: `mem:pipeline/lifecycle`
- Database Schema: `mem:database/schema`
