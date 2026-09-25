# Card Transfer & Publication Pipeline

## End-to-End Pipeline Phases
Every transfer workflow transitions through strictly ordered phases (`internal/feature/transfer/service/model`):

```
[Import / Staging] (Excel upload or Cabinet Copy)
       │
       ▼
PhaseInitializing ──► PhasePreparing ──► PhaseAwaitingAuthorization ──► PhasePublishing
                                                                              │
                                                                              ▼
PhaseFinished ◄──────── PhaseReconciling ◄──────── PhaseMedia ◄───────────────┘
```

### 1. Ingestion & Staging (`cardimport` / `cabinetcopy`)
- **Sources**: Uploaded Excel file (`cardimport`) or existing cabinet catalog clone (`cabinetcopy`).
- **Validation**: Headers, mandatory fields, barcode uniqueness, variant grouping.
- **Output**: Canonical immutable batch in `wb.card_batches` with `wb.card_batch_items`.

### 2. Initialization (`PhaseInitializing`)
- **Cohort Selection**: Target cabinets chosen by user or automated policy.
- **Root Hash**: Generates `TargetSetRoot` (canonical SHA-256 combining cabinet IDs, seller keys, revisions, capabilities).
- **Projections**: Seeds `wb.transfers`, `wb.transfer_targets`, and initial `pending` projections in `wb.transfer_group_targets` and `wb.transfer_item_targets`.

### 3. Card Preparation (`PhasePreparing`)
- **Validator**: `cardprepare.Processor()` evaluates items against live WB category requirements, characteristics, and dimension constraints.
- **Artifacts**: Saves proposal DTOs and preparation artifacts into `wb.card_preparations` and `wb.card_preparation_items`.
- **Transitions**: Cards failing validation transition to `rejected` or `unresolved`; valid cards become `succeeded` and ready for publication planning.

### 4. Live Authorization (`PhaseAwaitingAuthorization`)
- **Locking**: Verifies active API tokens and capabilities for all target cabinets before submitting data.
- **Lease**: Creates `wb.transfer_live_authorizations` bound to the transfer and publication plan ID. Prevents token revocation mid-flight.

### 5. Publication Dispatch (`PhasePublishing`)
- **Incremental Planning**: `cardpublication.Processor()` groups prepared cards into publication plans (`wb.publication_plans`) and actions (`wb.publication_actions`) respecting WB batch limits.
- **Dispatch**: `cardpublication.ProductDispatcher()` dispatches payloads to WB Content API `/content/v2/cards/upload`.
- **Identity Registration**: On synchronous task acceptance, records attempt ID and maps vendor codes / barcodes to NMIDs in `wb.product_identities`.
- **Resilience**: Ambiguous network drops or unacknowledged responses are captured in `wb.publication_product_retries` with classification before retry.

### 6. Media Upload & Visibility Polling (`PhaseMedia`)
- **Visibility Polling**: `cardpublicationFeature.RunMediaPolling()` queries WB to verify that newly created cards are visible in the catalog (`wb.publication_media_visibility`).
- **Media Upload**: Once cards are registered, dispatches photos and videos to `/content/v2/cards/upload/add`.

### 7. Error Reconciliation (`PhaseReconciling`)
- **Feed Poller**: Queries WB `/content/v2/cards/error/list` to capture asynchronous processing errors reported by Wildberries.
- **Correlation**: `cardpublicationFeature.ErrorFeed()` correlates raw WB errors to transfer items via `wb.publication_error_correlations`.
- **Manual Intervention**: Items requiring user correction are escalated to `wb.publication_manual_resolutions` for user review via Telegram.

### 8. Finalization & Projection (`PhaseFinished`)
- **Projections Evaluation**: All item targets reach terminal state (`ItemTargetTerminal`) with a defined `ResultClass`:
  - `success`: Card created, media attached, no blocking errors.
  - `skipped`: Omitted by configuration or failed prerequisite.
  - `rejected`: Explicitly rejected by WB schema or business rule.
  - `partial`: Base card published but media or secondary attributes failed.
  - `unresolved`: Awaiting manual user intervention or indeterminate state.
  - `internal_error` / `failed`: System or fatal transport error.
- **Master Transfer Outcome**: Overall transfer marked as `succeeded`, `partial`, `rejected`, `failed`, or `unresolved`.
- **Notifications**: `statistics` module generates an Excel execution report and alerts the user in Telegram.

---

## Related Memories
- Architecture and invariants: `mem:architecture/overview`
- Transfer feature internals: `mem:features/transfer`
- Planned transfer simplification & state consolidation: `mem:architecture/transfer_simplification_plan`
- Feature modules breakdown: `mem:features/modules`
- Database schema: `mem:database/schema`
- WB Client: `mem:core/wb_client`
