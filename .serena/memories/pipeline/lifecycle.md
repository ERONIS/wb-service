# Card Transfer & Publication Pipeline

## End-to-End Pipeline Phases
Every transfer workflow transitions through strictly ordered phases (`internal/feature/transfer/service/model`):

1. **Staging / Import**:
   - Source: Excel file upload via `cardimport` or cabinet-to-cabinet copy via `cabinetcopy`.
   - Result: Aggregated, validated rows stored as immutable `Card` payloads in `wb.card_batches`.
2. **Transfer Initialization (`PhaseInitializing`)**:
   - Selection of target cabinets.
   - Calculation of canonical `TargetSetRoot` hash combining cabinet IDs, seller keys, revisions, and capabilities.
3. **Card Preparation (`PhasePreparing`)**:
   - `cardprepare` feature checks category rules, mandatory characteristics, size schemas, and barcodes against WB requirements.
   - Generates preparation artifacts and proposal DTOs.
4. **Publication Planning & Authorization (`PhaseAwaitingAuthorization`)**:
   - `cardpublication` partitions prepared cards into upload actions.
   - `transfer` verifies live authorization of target credentials (`transfer_live_authorizations`).
5. **Publication Dispatch (`PhasePublishing`)**:
   - Actions dispatched to WB Content API `/content/v2/cards/upload`.
   - Synchronous WB response yields upload task ID; status recorded in `wb.publication_attempts`.
6. **Media Polling (`PhaseMedia`)**:
   - Background worker `RunMediaPolling` polls until cards are registered by WB, then submits photos and videos via `/content/v2/cards/upload/add`.
7. **Reconciliation & Error Correlation (`PhaseReconciling`)**:
   - Error feed poller queries WB `/content/v2/cards/error/list`.
   - Errors matched to items in `wb.publication_error_correlations`.
   - Items requiring intervention trigger manual resolution flow (`wb.publication_manual_resolutions`).
8. **Finalization (`PhaseFinished`)**:
   - All item targets projected into terminal states (`terminal`) with outcome class: `success`, `skipped`, `rejected`, `partial`, `unresolved`, `failed`.
   - Transfer outcome set: `succeeded`, `partial`, `rejected`, `failed`, or `unresolved`.

## Related Memories
- Architecture: `mem:architecture/overview`
- Database tables: `mem:database/schema`
- Feature modules: `mem:features/modules`
