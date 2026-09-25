# Functional Business Modules (`internal/feature`)

## 1. `wbcabinet`
- Manages Wildberries cabinets and credentials.
- Encrypts API tokens using AES-GCM before saving to `wb.api_cabinets`.
- Verifies tokens by checking JWT claims, calling Content `/ping`, and fetching seller profile via General `/api/v1/seller-info`.
- Bootstraps initial cabinets from `.env.wb` on startup and dynamically activates verified candidates in `wbClientset`.

## 2. `cardimport`
- Ingests product cards from Excel files (`.xlsx`) via `excelize/v2`.
- Validates row-level headers, required fields, and data types against schema definitions.
- Groups variants into unified `Card` structs, computes batch checksums, and saves to `wb.card_batches`.

## 3. `cardedit`
- Interactive Telegram-based card editor.
- Allows users to review, inspect, and modify card properties, barcodes, or prices prior to initiating a transfer.

## 4. `cardprepare`
- Transforms normalized cards into WB-compatible publication payloads.
- Validates category requirements, characteristics, and dimension constraints against WB metadata.

## 5. `cardpublication`
- Plans publication batches respecting WB batch limits.
- Dispatches cards to WB Content API, manages media uploads, tracks error feeds, and handles manual resolutions for failed items.

## 6. `cabinetcopy`
- Automates card migration between Wildberries cabinets.
- Reads catalog of existing cards from source cabinet, sanitizes cabinet-specific identifiers, and creates an import batch for target cabinets.

## 7. `transfer`
- Central state machine orchestrating end-to-end card migration across target cabinets.
- Implements background poller `RunPolling` coordinating preparation, authorization, publication, and completion.

## 8. `statistics`
- Computes aggregated transfer execution metrics (total, processed, success, failure rates).
- Generates Excel summary reports and sends completion alerts to Telegram users.

## 9. `users`
- User access control, Telegram identity mapping, and role management (`admin`, `manager`, `partner`).

## Related Memories
- Pipeline workflow: `mem:pipeline/lifecycle`
- WB Client: `mem:core/wb_client`
- Database schema: `mem:database/schema`
