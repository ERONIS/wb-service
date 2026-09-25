# Memory Maintenance

## Discovery Model

- Core principle: progressive discovery through references, building a graph of memories.
- Initially, agents are provided with the list of all memories (names only).
- Agents should read `mem:core` as the top-level entry point (graph root).
  This memory should contain references to other memories covering major project domains.
  The referenced memories shall, in turn, contain references to even more specific memories, and so on.
  The depth of the graph shall depend on the project complexity.
- Use topics/folders to group related memories in order to make the content structure explicit.
  Folders can mirror project structure (e.g. modules like frontend/backend) or topics like debugging, architecture, etc.
- Memory references must use a mem: prefix inside backticks, e.g. `mem:frontend/core`.
  The surrounding text should clearly indicate when to read the memory/which content to expect.
  The text should provide more precise guidance than the memory name alone, 
  i.e. avoid a reference like "frontend debugging: `mem:frontend/debugging` and instead make clear which aspects of frontend debugging are covered.
- Memories themselves should not contain information about when to read them; this is the responsibility of the referring memory.

## Style

Dense agent notes, not prose docs. Prefer invariants, terse bullets. 
Avoid obvious context, rationale, and examples unless they prevent likely mistakes. 
Keep guidance durable and generalizable, not task-local.

## Add/update threshold

Add or update memories only with stable, non-obvious project conventions that avoid complex rediscovery in the future.
Do not add: quick-read facts; generic language/framework knowledge; one-off task notes; volatile line-level details; behavior likely to change soon.

## Mandatory Task & Post-Edit Workflow

- **Start of Task**: Activate project, list memories, read `mem:core`, and read all relevant linked memories before touching code.
- **Code Edits & Refactorings**: Use Serena's symbol tools (`replace_symbol_body`, `rename_symbol`, `safe_delete_symbol`, `replace_content`).
- **Code Comments Language**: All source code comments, godoc comments, docstrings, and inline code explanations MUST be written strictly in Russian. Non-Russian comments in source code are forbidden.
- **Mandatory Memory Sync After ANY Code Modification**:
  Whenever code is edited, added, refactored, or removed (or DB migrations/configs change), the agent MUST call `write_memory` / `edit_memory` BEFORE concluding the turn or replying.
  - New/changed structs, functions, interfaces -> update `mem:architecture/code_patterns` or relevant `mem:features/<module>`.
  - Database schema, table, migration changes -> update `mem:database/schema`.
  - State machine or phase updates -> update `mem:pipeline/lifecycle` or `mem:features/transfer`.
  - External API client / rate limit updates -> update `mem:core/wb_client`.
- **Completion Gate**: No code-editing task is considered done without a successful Serena memory sync.
- **Multi-Agent Single Source of Truth**: Codex, Gemini, and any other agents share this knowledge base. Keeping Serena up-to-date ensures seamless cross-window collaboration.

## Maintenance Actions

- Renaming memories: References are updated automatically if handled via Serena's memory rename tool.
- Checking for stale memories (e.g. after deletion): Call `serena memories check` for a report.
