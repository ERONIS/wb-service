# Architecture & Design Invariants

## Modular Structure
- Two distinct layers:
  - `internal/core`: Process-wide infrastructure, external API clients, DB pool, base domain models, hashing primitives, logger.
  - `internal/feature`: Autonomous business domain modules (`users`, `wbcabinet`, `cardimport`, `cardedit`, `cardprepare`, `cardpublication`, `transfer`, `cabinetcopy`, `statistics`).

## Key Invariants
- **Credential Isolation**: Feature modules NEVER receive raw API tokens, raw HTTP clients, or arbitrary URL paths. They only interact with WB through `internal/core/transport/wb` executors and strongly typed operations.
- **Dynamic Registry**: WB cabinets are registered dynamically via `CredentialCandidate`. The candidate is isolated until `wbcabinet` verifies JWT claims, seller identity, and permissions.
- **Transaction Boundaries**: Mutation operations must pass through Unit of Work (`internal/core/repository/postgres/transaction`), ensuring atomic state transitions in PostgreSQL.
- **Idempotency & Hashing**: All domain mutations and batches generate canonical SHA-256 hashes (`internal/core/canonicalhash`) with length-prefixed encoding. Used to prevent duplicate executions and track state modifications.
- **Composition Root**: Cross-feature wiring occurs exclusively in `cmd/wb-service/main.go`. Features do not import each other cyclically; they expose contracts and interfaces.

## Domain References
- WB Client details: `mem:core/wb_client`
- Card transfer lifecycle: `mem:pipeline/lifecycle`
- Functional modules: `mem:features/modules`
