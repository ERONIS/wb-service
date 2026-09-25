# Database Schema & Storage (`wb` namespace)

## Storage Stack
- PostgreSQL 18 with driver `pgx/v5` and connection pool `puddle/v2` (`internal/core/repository/postgres/pool`).
- Unit of Work pattern (`internal/core/repository/postgres/transaction`) wraps business operations in `pgx.Tx`.

## Migration Architecture (`migration/`)
Database schema is managed via `golang-migrate` (`migrate/migrate:v4.19.1`) partitioned into 7 domain-aligned layers (14 files: 7 `.up.sql` and 7 `.down.sql`):
1. `000001_users`: Schema `wb`, `wb.users` (roles: `user`, `partner`, `admin`), `wb.telegram_active_screens`.
2. `000002_wbcabinet`: `wb.cabinet_identity_bindings`, `wb.api_cabinets` (with `token_properties`, owner FK, status index).
3. `000003_card_import`: Ingestion pipeline & staging: `wb.card_import_*`, `wb.card_batches`, `wb.card_batch_items`, `wb.cabinet_copy_*`.
4. `000004_transfer`: Pipeline master and targets: `wb.transfers`, `wb.transfer_targets`, `wb.transfer_groups`, `wb.transfer_items`, `wb.transfer_group_targets`, `wb.transfer_item_targets` and partial status/running indexes.
5. `000005_card_preparation`: Validation and WB proposal artifacts: `wb.card_preparations`, `wb.card_preparation_groups`, `wb.card_preparation_artifacts`, `wb.card_preparation_items`.
6. `000006_card_publication`: Full publication cycle: `wb.publication_observations`, `wb.publication_plans`, `wb.publication_actions`, `wb.product_identities`, `wb.transfer_live_authorizations`, `wb.transfer_live_authorization_commands`, execution attempts, attributions, manual resolutions, error correlations/cursors/batches, media visibility, safe retries, and integrity triggers.
7. `000007_statistics`: Analytical views (`wb.statistics_*`).

## Key Tables by Functional Area

### 1. Users, Access Control & Cabinets
- `wb.users`: Telegram ID, username, full name, role (`admin`, `manager`, `partner`), status (`active`, `blocked`), timestamps.
- `wb.cabinet_identity_bindings`: Links users to authorized cabinet identities.
- `wb.api_cabinets`: Cabinet ID, encrypted token (AES-GCM), token properties, seller info, verification status, active revision.

### 2. Ingestion & Staging
- `wb.card_import_sessions`: Upload sessions, status, user ID.
- `wb.card_import_files`: Stored files, hash, size.
- `wb.card_import_issues`: Parsing warnings and validation errors.
- `wb.card_batches`: Finalized, deduplicated batches with canonical SHA-256 checksums.
- `wb.card_batch_items`: Normalized card payload JSON per item.
- `wb.cabinet_copy_sessions` & `wb.cabinet_copy_items`: Cross-cabinet card replication state.

### 3. Transfer Pipeline & Orchestration
- `wb.transfers`: Master transfer job, source batch, phase (`initializing`, `preparing`, `awaiting_authorization`, `publishing`, `reconciling`, `media`, `finished`), outcome, owner.
- `wb.transfer_targets`: Cohort of target cabinets, snapshot revision, capabilities.
- `wb.transfer_items`: Items attached to the transfer job.
- `wb.transfer_item_targets` & `wb.transfer_group_targets`: Granular item status per target cabinet with projections (`pending`, `running`, `terminal`).
- `wb.transfer_live_authorizations`: Live token leases per transfer and publication plan.

### 4. Preparation & Publication
- `wb.card_preparations` & `wb.card_preparation_items`: Validated WB-ready proposals and artifacts.
- `wb.publication_plans`: Planned API batches for upload.
- `wb.publication_actions`: Specific API actions within a publication plan.
- `wb.publication_attempts`: Execution attempts against WB Content API (`/content/v2/cards/upload`).
- `wb.product_identities`: Mapping of vendorCode / barcode to WB NMID (nomenclature ID) and chrtID.
- `wb.publication_product_retries`: Safe retry tracker for ambiguous or empty responses from WB.
- `wb.publication_media_visibility`: Tracks whether newly uploaded cards are visible in WB before uploading media.
- `wb.publication_error_correlations`: Error mapping from WB `/content/v2/cards/error/list` to specific transfer items.
- `wb.publication_manual_resolutions`: User intervention logs for rejected/failed items.

### 5. UI State & Reporting
- `wb.telegram_active_screens`: Preserves active screen ID per Telegram user for restart survivability.

## Immutability & Audit Triggers
- `wb.reject_publication_fact_delete()`: Database trigger preventing `DELETE` operations on publication facts, attempts, and error correlations.
- `wb.protect_publication_product_retry()`: Trigger preventing changes to identity and payload columns on retry records once finished.

## Related Memories
- Architecture and invariants: `mem:architecture/overview`
- Pipeline lifecycle: `mem:pipeline/lifecycle`
- Migrations and maintenance: `mem:operations/run_and_build`
