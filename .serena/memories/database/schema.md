# Database Schema & Storage (`wb` namespace)

## Storage Stack
- PostgreSQL 18 with driver `pgx/v5` and connection pool `puddle/v2` (`internal/core/repository/postgres/pool`).
- Unit of Work pattern (`internal/core/repository/postgres/transaction`) wraps business operations in `pgx.Tx`.

## Key Tables by Functional Area
- **Users & Permissions**:
  - `wb.users`: Telegram ID, username, full name, role (`admin`, `manager`, `partner`), status (`active`, `blocked`), created/updated timestamps.
  - `wb.cabinet_identity_bindings`: Links users to authorized cabinet identities.
- **Cabinets & Credentials**:
  - `wb.api_cabinets`: Cabinet ID, encrypted token (AES-GCM), token properties, seller info, verification status, active revision.
- **Import & Staging**:
  - `wb.card_import_sessions`: Upload session, status, user ID.
  - `wb.card_import_files`: Stored files, hash, size.
  - `wb.card_import_issues`: Parsing warnings and errors.
  - `wb.card_batches`: Finalized, deduplicated batches with semantic checksum.
  - `wb.card_batch_items`: Normalized card payload JSON per item.
- **Transfer Pipeline**:
  - `wb.transfers`: Master transfer job, source batch, phase, outcome, owner.
  - `wb.transfer_targets`: Cohort of target cabinets, snapshot revision.
  - `wb.transfer_items` & `wb.transfer_item_targets`: Granular item status per target cabinet with projections (`pending`, `running`, `terminal`).
- **Preparation & Publication**:
  - `wb.card_preparations` & `wb.card_preparation_items`: Validated WB-ready proposals and artifacts.
  - `wb.publication_plans` & `wb.publication_actions`: Planned API batches for upload.
  - `wb.product_identities`: Mapping of vendorCode / barcode to WB NMID (nomenclature ID) and chrtID.
  - `wb.publication_attempts`: Execution attempts against WB Content API.
  - `wb.publication_error_correlations`: Error mapping from WB error feed to specific items.
  - `wb.publication_manual_resolutions`: User intervention logs for rejected/failed items.
- **UI State & Copy**:
  - `wb.telegram_active_screens`: Preserves active screen ID per Telegram user.
  - `wb.cabinet_copy_sessions` & `wb.cabinet_copy_items`: Cross-cabinet card replication sessions.

## Related Memories
- Architecture and invariants: `mem:architecture/overview`
- Pipeline lifecycle: `mem:pipeline/lifecycle`
- Migrations and maintenance: `mem:operations/run_and_build`
