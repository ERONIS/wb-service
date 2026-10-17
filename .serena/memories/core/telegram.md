# Telegram Transport Engine (`internal/core/transport/telegram`)

## Core Components
- Powered by `gopkg.in/telebot.v3`.
- `server/server.go`: Bot lifecycle management, webhook/long-polling executor, graceful shutdown.
- `menu_manager.go`: Screen renderer, inline keyboards, message update vs send decision logic.
- `state_store.go`: State machine management for multi-step interactive flows (`StateStore[T]`). Standardized across all interactive modules (`wbcabinet`, `users`, `cardedit`).
- `pagination.go`: Unified inline keyboard pagination (`BuildPaginationRow`, `CalculateTotalPages`, `PaginationConfig`).
- `ui_helpers.go`: Shared UI components including progress bar (`FormatProgressBar`), percentage (`CalcPercent`), and datetime formatters (`FormatDateTime`, `FormatDateTimeUTC`).
- `helpers.go`: Callback argument packing/unpacking with Base-36 ID encoding (`EncodeID`, `ParseID`, `ParseBase36Argument`), ID+Revision parser (`ParseIDAndRevision`), and page parser (`ParsePageNumber`), eliminating duplicated parsing boilerplate across feature modules.
- `error_presenter.go`: Translates domain and system errors into user-friendly localized messages (`NotifyServiceError`).

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
