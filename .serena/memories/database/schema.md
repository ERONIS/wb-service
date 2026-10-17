# Database Schema & Storage (`wb` namespace)

## Storage Stack
- PostgreSQL 18 with driver `pgx/v5` and connection pool `puddle/v2` (`internal/core/repository/postgres/pool`).
- Unit of Work pattern (`internal/core/repository/postgres/transaction`) wraps business operations in `pgx.Tx`.

## Migration Architecture (`migration/`)
Database schema is managed via `golang-migrate` (`migrate/migrate:v4.19.1`) partitioned into 7 domain-aligned layers (14 files: 7 `.up.sql` and 7 `.down.sql`):
1. `000001_users`: Schema `wb`, `wb.users` with `BIGSERIAL` ID (roles: `user`, `partner`, `admin`), `wb.telegram_active_screens`.
2. `000002_wbcabinet`: `wb.cabinet_identity_bindings`, `wb.api_cabinets` (with `token_properties`, owner FK, status index).
3. `000003_card_import`: Ingestion pipeline & staging: `wb.card_import_*`, `wb.card_batches`, `wb.card_batch_items`, `wb.cabinet_copy_*`; both `author_user_id` foreign keys are `BIGINT` from creation.
4. `000004_transfer`: Pipeline master and targets: `wb.transfers`, `wb.transfer_targets`, `wb.transfer_groups`, `wb.transfer_items`, `wb.transfer_group_targets` (includes diagnostics `preparation_item_position`, `preparation_field`, `preparation_started_at`, `preparation_finished_at`), `wb.transfer_item_targets` and partial status/running indexes, including the pending keyset covering index `transfer_group_targets_pending_keyset_idx`.
5. `000005_card_preparation`: Directly creates immutable WB proposal artifacts `wb.card_preparation_artifacts` bound to `wb.transfer_group_targets(transfer_id, id)`. The current fresh-install baseline omits old intermediate tables (`card_preparations`, `card_preparation_groups`, `card_preparation_items`). The user will reinitialize the database from scratch, so upgrading the old preparation schema and migrating its data are out of scope. Validate that all migrations apply cleanly to an empty database and that the resulting schema matches current code.
6. `000006_card_publication`: Full publication cycle: `wb.publication_observations`, `wb.publication_plans`, `wb.publication_actions`, `wb.product_identities`, `wb.transfer_live_authorizations`, `wb.transfer_live_authorization_commands`, execution attempts, attributions, manual resolutions, error correlations/cursors/batches, media visibility, safe retries, and integrity triggers.
7. `000007_statistics`: Analytical views (`wb.statistics_*`).
The fresh-install baseline folds the former user-ID widening and pending keyset index into migrations 1, 3, and 4. There are no separate 8/9 upgrade or rollback migrations; the user will reinitialize the database. The covering index matches `ListPendingPreparationGroups`, though index-only execution still depends on planner choice and visibility-map coverage.\n
## Key Tables by Functional Area

### 1. Users, Access Control & Cabinets
- `wb.users`: Primary key `id BIGINT`, Telegram ID (`tg_id BIGINT`), username, full name, role (`admin`, `partner`, `user`), timestamps.
- `wb.cabinet_identity_bindings`: Links users to authorized cabinet identities.
- `wb.api_cabinets`: Cabinet ID, token stored as TEXT, token properties, seller info, verification status, active revision. Current application writes the token directly; AES-GCM encryption is not implemented.

### 2. Ingestion & Staging
- `wb.card_import_sessions`: Upload sessions, status, `author_user_id BIGINT` (FK -> `wb.users(id) ON DELETE SET NULL`).
- `wb.card_import_files`: Stored files, hash, size.
- `wb.card_import_issues`: Parsing warnings and validation errors.
- `wb.card_batches`: Finalized, deduplicated batches with canonical SHA-256 checksums.
- `wb.card_batch_items`: Normalized card payload JSON per item.
- `wb.cabinet_copy_sessions` & `wb.cabinet_copy_items`: Cross-cabinet card replication state, `author_user_id BIGINT` (FK -> `wb.users(id) ON DELETE SET NULL`).

### 3. Transfer Pipeline & Orchestration
- `wb.transfers`: Master transfer job, source batch, phase (`initializing`, `preparing`, `awaiting_authorization`, `publishing`, `reconciling`, `media`, `finished`), outcome, owner.
- `wb.transfer_targets`: Cohort of target cabinets, snapshot revision, capabilities.
- `wb.transfer_items`: Items attached to the transfer job.
- `wb.transfer_item_targets` & `wb.transfer_group_targets`: Granular item status per target cabinet with projections (`pending`, `running`, `terminal`). Keyset dispatch optimized via partial covering index `transfer_group_targets_pending_keyset_idx`.
- `wb.transfer_live_authorizations`: Live token leases per transfer and publication plan.

### 4. Preparation & Publication
- `wb.card_preparation_artifacts`: Validated WB-ready proposals, payload, metadata snapshot, and limits bound directly to `wb.transfer_group_targets(transfer_id, id)` with `reject_card_preparation_artifact_mutation()` immutability trigger.
- `wb.publication_plans`: Planned API batches for upload.
- `wb.publication_actions`: Specific API actions within a publication plan.
- `wb.publication_attempts`: Execution attempts against WB Content API (`/content/v2/cards/upload`).
- `wb.product_identities`: Mapping of vendorCode / barcode to WB NMID (nomenclature ID) and chrtID.
- `wb.publication_product_retries`: Safe retry tracker for ambiguous or empty responses from WB.
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