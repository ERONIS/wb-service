# Telegram Transport Engine (`internal/core/transport/telegram`)

## Core Components
- Powered by `gopkg.in/telebot.v3`.
- `server/server.go`: Bot lifecycle management, webhook/long-polling executor, graceful shutdown.
- `menu_manager.go`: Screen renderer, inline keyboards, message update vs send decision logic.
- `state_store.go`: State machine management for multi-step interactive flows.
- `error_presenter.go`: Translates domain and system errors into user-friendly localized messages.

## Middlewares
- `RoleMiddleware`: Restricts handlers according to `wb.users` roles (`admin`, `manager`, `partner`).
- `CallbackMiddleware`: Prevents duplicate callback executions and ensures prompt callback answering.
- `LoggerMiddleware`: Emits structured request/response trace logs for Telegram events.

## Screen Persistence Invariant
- Active user screen state is persisted to database table `wb.telegram_active_screens`.
- Survives service restarts; users resume on their last active screen without orphaned inline keyboards.

## Related Memories
- Overall architecture: `mem:architecture/overview`
- User management and roles: `mem:features/modules`
- Database schema: `mem:database/schema`
