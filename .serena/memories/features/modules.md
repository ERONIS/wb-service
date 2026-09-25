# Functional Business Modules (`internal/feature`)

## Architectural Pattern
Every feature module is encapsulated in `internal/feature/<name>` and follows a strict 3-tier internal layout:
- `transport/`: External adapters — either Telegram menus/handlers (`transport/telegram/`) or WB client adapters (`transport/wb/`).
- `service/`: Core business logic, domain models, validations, and state machines.
- `repository/`: PostgreSQL data access implementing feature-scoped queries, always executing within the Unit of Work (`uow`) transaction context.
Features expose public facades via `feature.go` and interact solely through Go interfaces defined in the composition root (`cmd/wb-service/main.go`).

---

## 1. `wbcabinet` (WB Cabinet & Credential Management)
- **Role**: Full lifecycle of Wildberries seller accounts and API tokens.
- **Key Subsystems**:
  - `service/service.go`: CRUD operations (`Add`, `Delete`, `List`, `Initialize`).
  - `transport/wb/verifier.go`: Verifies candidate credentials via JWT inspection, Content `/ping`, and General `/api/v1/seller-info`.
  - `repository/postgres/repository.go`: Persists cabinet entities with AES-GCM encrypted tokens in `wb.api_cabinets`.
  - `transport/telegram/`: Menus for adding, listing, and deleting seller cabinets.
- **Invariants**:
  - Tokens are never logged or stored in plain text.
  - Verified cabinets are dynamically registered into `wbClientset` without restarting the process.
  - Background loop `RunRestoreRetry` runs every 30s to re-verify temporarily unreachable cabinets.

## 2. `cardimport` (Excel Ingestion & Batch Staging)
- **Role**: Parses product spreadsheets uploaded via Telegram documents into immutable staging batches.
- **Key Subsystems**:
  - `transport/xlsx/`: Robust Excel parser based on `excelize/v2` (`columns.go`, `row.go`, `parser.go`). Validates column headers, required attributes, barcode formats, and numeric constraints.
  - `service/aggregation/`: Normalizes spreadsheet rows, grouping color/size variants into unified `Card` domain models.
  - `service/workflow/`: Manages upload sessions (`session.go`), parse issue recording, and final batch commit.
  - `repository/postgres/`: Tables `wb.card_import_sessions`, `wb.card_import_files`, `wb.card_import_issues`, `wb.card_batches`, `wb.card_batch_items`.
- **Invariants**:
  - Committed batches are assigned a deterministic `canonicalhash.Digest`. Once finalized, batch items are strictly immutable.

## 3. `cardedit` (Pre-Publication Interactive Editor)
- **Role**: Telegram-based inspection and modification of card parameters before transfer.
- **Key Subsystems**:
  - `service/service.go`: Stateful edit sessions managing field updates (prices, vendor codes, barcodes, descriptions).
  - `transport/wb/gateway.go`: Queries live WB catalog metadata to validate edited values.
  - `transport/telegram/`: Interactive UI (`workflow.go`, `view.go`) embedded directly into the card import review flow.
- **Invariants**:
  - Edits do not mutate original Excel files; they update staging records prior to transfer pipeline initialization.

## 4. `cardprepare` (WB Schema Adaptation & Validation)
- **Role**: Validates cards against target category rules and generates WB-compliant upload proposals.
- **Key Subsystems**:
  - `service/preparation/catalog.go` & `catalog_cache.go`: Queries WB category rules, required characteristics, and constraint dictionaries, caching responses in memory.
  - `service/preparation/upload_validation.go`: Validates product characteristics, dimensions, and barcodes against target cabinet category constraints.
  - `service/workflow/processor.go`: Implements `Processor()` called by transfer poller during `PhasePreparing`.
  - `repository/postgres/`: Tables `wb.card_preparations` and `wb.card_preparation_items`.
- **Invariants**:
  - Items failing validation are marked `rejected` or `unresolved` with detailed error codes, preventing malformed payloads from reaching WB Content API.

## 5. `cardpublication` (Batch Dispatch, Media & Error Feed)
- **Role**: Slices prepared cards into publication batches, sends requests to Content API, tracks media visibility, ingests error feeds, and handles manual resolutions.
- **Detailed Reference**: See dedicated memory `mem:features/cardpublication`.
- **Key Components**: `planner.go`, `product_dispatcher.go`, `media_dispatcher.go`, `media_visibility.go`, `error_feed.go`, `manual.go`.

## 6. `cabinetcopy` (Cross-Cabinet Catalog Replication)
- **Role**: Clones existing product catalogs from one WB cabinet to others.
- **Key Subsystems**:
  - `transport/wb/reader.go`: Paginates through source cabinet's active catalog via `/content/v2/get/cards/list`.
  - `service/service.go`: Sanitizes catalog data — strips cabinet-specific identifiers (NMID, internal barcodes, seller IDs) while retaining characteristics, descriptions, and media links.
  - `repository/postgres/`: Tables `wb.cabinet_copy_sessions` and `wb.cabinet_copy_items`.
  - `transport/telegram/`: UI for selecting source cabinet, filtering products, and targeting destination cabinets.
- **Invariants**:
  - Successfully exported catalogs are transformed directly into standard `cardimport` batches, seamlessly feeding into the `transfer` pipeline.

## 7. `transfer` (Central Pipeline Orchestrator)
- **Role**: Master State Machine coordinating the end-to-end multi-cabinet transfer lifecycle.
- **Detailed Reference**: See dedicated memory `mem:features/transfer`.
- **Key Components**: `workflow/service.go`, `polling.go`, `authorization/automatic.go`, `preparation/result.go`, `publication/planning.go`, `publication/execution.go`.

## 8. `statistics` (Metrics, Reporting & Attention Resolution)
- **Role**: Real-time progress tracking, user alerting, and post-transfer analytics.
- **Key Subsystems**:
  - `service/service.go`: Computes aggregate progress, success/failure counts, and terminal outcome projections.
  - `transport/telegram/`: Generates and sends completion alerts with attached `.xlsx` summary reports.
  - `RunAutoAttentionResolver`: Background worker polling every 10s to auto-resolve stale attention items.
  - `repository/postgres/`: Queries aggregations across `transfers`, `transfer_item_targets`, and `publication_error_correlations`.

## 9. `users` (RBAC & Cabinet Access Control)
- **Role**: Manages Telegram user identities, permissions, and cabinet ownership.
- **Key Subsystems**:
  - `service/service.go`: User CRUD, role assignment, authentication checks.
  - `repository/postgres/`: Tables `wb.users` and `wb.cabinet_identity_bindings`.
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
