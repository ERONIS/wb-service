# Wildberries Core Client (`internal/core/transport/wb`)

## Architecture & Responsibilities
- Single process-wide client manages connections to Wildberries Content, General, and Prices APIs:
  - Content API: `content-api.wildberries.ru`
  - Common / General API: `common-api.wildberries.ru`
  - Prices / Discounts API: `discounts-prices-api.wildberries.ru`
- Package layout follows client-go style:
  - `clientset.go`: Registry of cabinet clients, shared connection pool, candidate creation.
  - `cabinet.go`: Per-cabinet scoped executor binding authorization headers and flow control.
  - `flowcontrol/`: Token bucket rate limiting, rate admission, header observation (`X-Ratelimit-*`).
  - `policy/`: Safe retry policies with exponential backoff.
  - `transport/`: Underlying HTTP roundtripper, metrics, connection pooling.

## Operational Rules
- **Safety Policy**: Automated retries are permitted ONLY for idempotent read/GET requests. Mutations (POST/PUT/DELETE) fail immediately on transient network/delivery ambiguity to avoid double-processing.
- **Rate Admission**: Calls pass through `flowcontrol` token buckets before hitting network.
- **Bounded Reading**: Response reading is strictly bounded by size limits to prevent memory exhaustion from oversized WB payloads.
- **Unified Logging**: Exactly one structured Zap log entry per logical request capturing attempt count, status, latency, and delivery state.

## Related Memories
- Overall architecture: `mem:architecture/overview`
- Cabinet management & verification: `mem:features/modules`
- Transfer pipeline execution: `mem:pipeline/lifecycle`
