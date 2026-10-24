# Database Schema & Storage (`wb` namespace)

## Storage Stack
- PostgreSQL 18 with driver `pgx/v5` and connection pool `puddle/v2` (`internal/core/repository/postgres/pool`).
- Unit of Work pattern (`internal/core/repository/postgres/transaction`) wraps business operations in `pgx.Tx`.

## Migration Architecture (`migration/`)
Database schema is managed via `golang-migrate` (`migrate/migrate:v4.19.1`) with 7 ordered baseline migrations for a fresh database:
1. `000001_users`: Schema `wb`, `wb.users` with `BIGSERIAL` ID (roles: `user`, `partner`, `admin`), `wb.telegram_active_screens`.
2. `000002_wbcabinet`: `wb.api_cabinets` keyed by `seller_id VARCHAR(36)` (with `token_properties`, owner FK, status index).
3. `000003_card_import`: Only `wb.card_batches` and `wb.card_batch_items`. Collecting sessions and XLSX bytes remain in process memory; the header stores source session ID, author Telegram ID, retry key/revision and counts; item rows retain source file ID/rows.
4. `000004_transfer`: Pipeline master and targets: `wb.transfers`, `wb.transfer_targets`, `wb.transfer_groups`, `wb.transfer_items`, `wb.transfer_group_targets` (includes diagnostics `preparation_item_position`, `preparation_field`, `preparation_started_at`, `preparation_finished_at`), `wb.transfer_item_targets` and partial status/running indexes, including the pending keyset covering index `transfer_group_targets_pending_keyset_idx`. Transfer groups/items do not duplicate batch source file or source rows; `wb.card_batch_items` retains provenance.
5. `000005_card_preparation`: Directly creates immutable WB proposal artifacts `wb.card_preparation_artifacts` bound to `wb.transfer_group_targets(transfer_id, id)`; no old intermediate preparation tables.
6. `000006_card_publication`: Full publication cycle and integrity triggers.
7. `000007_statistics`: Analytical views (`wb.statistics_*`).
Historical transition migrations `000008_card_import_memory` and `000009_transfer_ready_batch` were folded into the baseline and removed. The migration directory has seven matching `.up.sql`/`.down.sql` pairs. `up` creates the current schema on a fresh database; `down` rolls back these baseline steps. The user reinitializes the database from scratch, so upgrading an existing database through transition migrations is out of scope. Validate fresh application and rollback of all migrations and match the resulting schema to current code.
The covering index matches `ListPendingPreparationGroups`, though index-only execution still depends on planner choice and visibility-map coverage.

## Key Tables by Functional Area

### 1. Users, Access Control & Cabinets
- `wb.users`: Primary key `id BIGINT`, Telegram ID (`tg_id BIGINT`), username, full name, role (`admin`, `partner`, `user`), timestamps.
- `wb.api_cabinets`: `seller_id VARCHAR(36)` primary key, token stored as TEXT, token properties, owner, verification status and revisions. Current application writes the token directly; AES-GCM encryption is not implemented.

### 2. Ingestion & Finalized Batches
- Before Finalize, sessions, uploaded XLSX bytes, parsed cards/issues and revisions are held only in process memory, not PostgreSQL. Restart drops them.
- `wb.card_batches`: One row per finalized source session (`source_session_id` unique), with author Telegram ID, finalize revision/idempotency key, counts, and `sealed` flag. No purpose/source-kind, author snapshot, session timestamps or file-metadata JSONB are persisted. Batch readers and cardpublication use the sealed batch directly; no separate persisted session/file tables exist.
- `wb.card_batch_items`: Immutable `Card` payload JSONB plus source file ID/rows, group key, vendor code and position. Source file ID/rows remain as provenance in each item; the service validates membership of item file IDs in the finalized in-memory file set before insert. The insert trigger allows items only while their batch is unsealed. Finalize inserts the unsealed header, copies items, then seals it in one UoW transaction. Only this seal transition is allowed; sealed batches/items reject later updates and deletes. No raw XLSX bytes or intermediate issues are persisted.

### 3. Transfer Pipeline & Orchestration
- `wb.transfers`: Master transfer job with source batch ID and item/group counts, phase (`initializing`, `preparing`, `awaiting_authorization`, `publishing`, `reconciling`, `media`, `finished`), and outcome. Batch checksum, batch versions, target snapshot revision, and target-set root are absent from the baseline and frozen-field trigger. `transfer_targets` stores selected cabinet state; `product_identities.normalization_version` remains unrelated.
- `wb.transfer_targets`: Cohort of target cabinets keyed per transfer by `seller_id VARCHAR(36)`, with token generation, revisions, expiry and capabilities.
- `wb.transfer_items`: Items attached to the transfer job.
- `wb.transfer_item_targets` & `wb.transfer_group_targets`: Granular item status per target cabinet with projections (`pending`, `running`, `terminal`). Keyset dispatch optimized via partial covering index `transfer_group_targets_pending_keyset_idx`.
- `wb.transfer_live_authorizations`: Live token leases per transfer and publication plan.

### 4. Preparation & Publication
- `wb.card_preparation_artifacts`: WB-ready proposals, payload, metadata snapshot, and limits (without digest/root columns) bound directly to `wb.transfer_group_targets(transfer_id, id)` with `reject_card_preparation_artifact_mutation()` immutability trigger.
- `wb.publication_plans`: Incremental API upload plans identified by numeric ID; a transfer can have multiple plans as preparation groups become ready. Group-target state and plan IDs coordinate retries and concurrent planning.
- `wb.publication_actions`: Specific API actions within a publication plan, identified by numeric ID and unique numeric position per plan. Plans, actions, attempts, observations, attribution, live authorization, error batches, and manual resolution no longer store Digest-family hash columns.
- `wb.publication_attempts`: Execution attempts against WB Content API (`/content/v2/cards/upload`).
- `wb.product_identities`: Mapping of vendorCode / barcode to WB NMID (nomenclature ID) and chrtID.
- `wb.publication_product_retries`: Safe retry tracker for ambiguous or empty responses from WB; retries compare stored request payload and action/authorization IDs instead of a request digest.
- `wb.publication_media_visibility`: Tracks whether newly uploaded cards are visible in WB before uploading media.
- `wb.publication_error_correlations`: Error mapping from WB `/content/v2/cards/error/list` to specific transfer items.
- `wb.publication_manual_resolutions`: User intervention logs for rejected/failed items.

### Reporting Mutation Boundary
- Statistics no longer has a RequeueCardForCreation repository path resetting item/group/identity/transfer state after a negative WB lookup. Missing cards remain in attention; publication controls retries. No schema migration is needed for this boundary change.

### 5. UI State & Reporting
- `wb.telegram_active_screens`: Preserves active screen ID per Telegram user for restart survivability.

## Immutability & Audit Triggers
- `wb.reject_publication_fact_delete()`: Database trigger preventing `DELETE` operations on publication facts, attempts, and error correlations.
- `wb.protect_publication_product_retry()`: Trigger preventing changes to identity and payload columns on retry records once finished.
- `wb.protect_transfer_group_target_identity()`: Trigger protecting transfer group target identity immutability.

## Related Memories
- Architecture and invariants: `mem:architecture/overview`
- PostgreSQL repository and schema refactoring plan: `mem:architecture/postgres_refactoring_plan`
- Pipeline lifecycle: `mem:pipeline/lifecycle`
- Migrations and maintenance: `mem:operations/run_and_build`