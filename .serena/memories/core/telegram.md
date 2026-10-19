# Telegram Transport (`internal/core/transport/telegram`)

## Package Boundaries
- No root package or forwarding facade. Composition root and feature Telegram adapters import the narrow packages they use.
- `menu`: bot `Handler`, command/callback registration, text/document flow ownership, main menu, active-screen persistence, reply keyboard, notifications, and managed `tele.Context`. Its `MenuStore` contract is implemented by the PostgreSQL Telegram view repository.
- `callback`: callback argument and page parsing, compact Base-36 ID encoding.
- `identity`: `ParseTelegramID`, `SenderID`, and `UserFromContext`; constructs `domain.User` with a validated fallback name.
- `state`: generic `StateStore[T]`, `StateLease[T]`, and claim outcomes for interactive feature flows.
- `ui`: pagination, progress/percentage, datetime formatting, and rune-safe display truncation.
- `presenter`: localized error matching and `NotifyServiceError`; depends on `menu.Notify` for delivery.
- `middleware`: role access and structured Telegram event logging. `server`: webhook/long-polling lifecycle.
- Dependency direction: feature Telegram adapters -> narrow core packages; `presenter` -> `menu`; `identity` -> `ui`; `menu` -> `middleware`; no core Telegram package imports a feature.

## Menu Ownership and Persistence
- `menu/handler.go` registers commands/callbacks; `menu/flow.go` owns interactive input routing; `menu/host.go` owns chat state and middleware; `menu/render.go` owns active-screen rendering and the 1500 ms update interval; `menu/keyboard.go` owns persistent reply keyboard delivery; `menu/notification.go` owns temporary notifications and duplicate suppression; `menu/context.go` wraps `tele.Context`; `menu/text.go` enforces Telegram's 4096 UTF-16-unit text limit.
- Active user screens persist through `MenuStore` to `wb.telegram_active_screens`. Rendering edits the active message when possible and falls back to sending a new one; this preserves the single active screen behavior across restarts.
- `menu.Register` wires role access, menu middleware, the main menu, and input fallback. Feature modules attach their buttons and flows through `*menu.Handler`.
- State lease IDs prevent an old flow from finishing a replacement flow; retry refreshes the lease expiry. Menu text limiting counts UTF-16 units, strips markup when truncating, then escapes the truncated text and appends the marker.

## Callback and Error Contracts
- Base-36 callback IDs protect Telegram's 64-byte callback-data limit.
- `presenter.NotifyServiceError` matches feature-specific cases before common core errors, then a standard fallback.

## Related Memories
- Overall architecture: `mem:architecture/overview`
- User management and roles: `mem:features/modules`
- Database schema: `mem:database/schema`
