package statistics_telegram_transport

import (
	"fmt"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"

	tele "gopkg.in/telebot.v3"
)

func presentError(ctx tele.Context, err error) error {
	notification := core_transport_telegram.MatchError(
		err,
		core_transport_telegram.OnError(nil, "statistics.read_failed", "❌ Не удалось прочитать статистику. Попробуйте позже."),
		core_transport_telegram.OnError(statistics_service.ErrOperationNotFound, "statistics.not_found", "⚠️ Операция не найдена. Обновите статистику."),
		core_transport_telegram.OnError(statistics_service.ErrActionNotFound, "statistics.not_found", "⚠️ Операция не найдена. Обновите статистику."),
		core_transport_telegram.OnError(statistics_service.ErrInvalidFilter, "statistics.stale_filter", "⚠️ Фильтр или кнопка устарели. Обновите статистику."),
		core_transport_telegram.OnError(cardpublication_service.ErrManualResolutionConflict, "statistics.resolution_conflict", "⚠️ Состояние уже изменилось. Откройте карточку заново."),
		core_transport_telegram.OnError(cardpublication_service.ErrManualEvidenceInvalid, "statistics.invalid_evidence", "⚠️ Не удалось подтвердить выбранный результат. Обновите карточку."),
	)
	if responseErr := core_transport_telegram.Notify(ctx, notification.Key, notification.Text); responseErr != nil {
		return fmt.Errorf("present statistics error: %v: %w", responseErr, err)
	}
	return fmt.Errorf("present statistics error: %w", err)
}
