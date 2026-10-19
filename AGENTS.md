# Project Agent Instructions

## Serena is Mandatory & Continuous Single Source of Truth

- **Project Activation**: Before any repository task, read Serena's initial instructions and activate this project (`wb-service`).
- **Pre-Task Memory Inspection**: At the start of a task, inspect available memories, read `mem:core`, then read every memory relevant to the area being changed.
- **Symbol-Aware Navigation**: Prefer Serena's symbol-aware navigation, reference search, diagnostics, and editing tools for source-code work when they fit the task.

### Mandatory Post-Code-Edit Synchronization (Non-Negotiable)
- **Automatic Memory Checkpoint After ANY Code Modification**: Whenever you (Gemini, Antigravity, Codex, or any agent) create, edit, refactor, or delete code, modify database migrations, add CLI targets, or update configurations, you **MUST IMMEDIATELY synchronize Serena's memories** before completing the turn or responding to the user.
- **Multi-Agent Parity**: Parallel agents (e.g. Codex in another window and Gemini in Antigravity) share this codebase and Serena memories. Failure to update Serena after a code change causes cross-agent hallucinations and context drift.
- **What Must Be Synchronized**:
  - New or modified function signatures, structs, contracts, and interfaces (record in `mem:architecture/code_patterns` or relevant `mem:features/<module>`).
  - State machine changes, phase transitions, outcome codes, or pipeline lifecycle changes (record in `mem:pipeline/lifecycle` or `mem:features/transfer`).
  - Database schema alterations, new tables, triggers, indexes, and migrations (record in `mem:database/schema`).
  - External API interactions, flow control, rate limiting, and retry policies (record in `mem:core/wb_client`).
  - Environment variables, operational scripts, or make targets (record in `mem:operations/run_and_build`).
- **Completion Gate**: Do NOT consider any code editing task finished until the corresponding Serena memory tool (`write_memory` / `edit_memory`) has been called and succeeded.
- **Security & Hygiene**: Never store secrets, credentials, tokens, personal data, raw environment values, transient task status, one-off debugging output, or obvious/volatile line-level implementation details in memories.
- **Hierarchy & Deduplication**: Update an existing memory instead of duplicating it. When adding a new topic, link it from `mem:core` or the nearest parent memory using a `mem:` reference. Follow `mem:memory_maintenance` for structure and style.

## Code Quality & Language Standards

- **Code Comments Exclusively in Russian**: All source code comments, docstrings, godoc comments, and inline code explanations MUST be written strictly in Russian. English or non-Russian comments in source code are forbidden.
- **Go Declaration Order**: Place package-level constants, variables, aliases, types, structs, and interfaces before every function and method in each Go source file.
