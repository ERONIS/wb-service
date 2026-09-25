# Card Publication Feature (`internal/feature/cardpublication`)

## Architectural Role
Orchestrates the actual submission of product cards to Wildberries Content API, monitors asynchronous processing, uploads media, tracks errors from the WB error feed, and provides manual resolution.

## Subsystems & Layout
1. **Planning (`service/planning/`)**:
   - `planner.go` & `processor.go`: Slices prepared card proposals into discrete upload actions grouped into `wb.publication_plans` and `wb.publication_actions`, enforcing WB batch limits (e.g., max items per request).
   - Incremental planning ensures new proposals can be planned as they finish preparation without waiting for the whole transfer.
2. **Product Execution (`service/execution/`)**:
   - `product_dispatcher.go`: Transmits card upload payloads to `/content/v2/cards/upload`.
   - `product_classifier.go`: Classifies WB responses into `accepted`, `rejected_proven`, or `uncertain`.
   - `product_reconciliation.go`: Correlates WB upload task IDs and maps barcodes/vendorCodes to WB NMID in `wb.product_identities`.
   - **Empty-Response Safe Retry**: Tracks dropped connections or unacknowledged responses in `wb.publication_product_retries` with delivery classification before retry.
3. **Media Pipeline (`service/execution/media_*`)**:
   - **Visibility Check (`media_visibility.go`)**: Queries WB to verify newly created cards are visible in the catalog before sending media (`wb.publication_media_visibility`).
   - `media_dispatcher.go`: Dispatches photos and videos to `/content/v2/cards/upload/add`.
   - Daemon `RunMediaPolling`: Continuously polls visibility and triggers media uploads.
4. **Error Monitoring & Feed (`service/monitoring/`)**:
   - `error_feed.go`: Periodically polls WB `/content/v2/cards/error/list`.
   - Correlates asynchronous errors reported by WB to specific transfer items in `wb.publication_error_correlations`.
5. **Resolution (`service/resolution/`)**:
   - `manual.go`: Provides `ManualResolver` for cards rejected by WB or flagged with schema discrepancies, enabling user override/re-dispatch.

## Database Tables (`wb` namespace)
- `wb.publication_plans`: High-level upload plans per transfer.
- `wb.publication_actions`: Individual API batches.
- `wb.publication_attempts`: Historical record of every HTTP attempt to WB Content API.
- `wb.product_identities`: Mapping between internal items and WB NMID / chrtID.
- `wb.publication_product_retries`: Tracking table for uncertain / empty response retries.
- `wb.publication_media_visibility`: Per-card catalog presence verification state.
- `wb.publication_error_correlations`: Normalized mapping of WB error feed records to transfer items.
- `wb.publication_manual_resolutions`: User intervention logs and resolution decisions.

## Invariants & Database Triggers
- `reject_publication_fact_delete`: `DELETE` statements on attempts, correlations, and identities are rejected at database level.
- `protect_publication_product_retry`: Identity, payload, and timestamps are immutable once finalized.
