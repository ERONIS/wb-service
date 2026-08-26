package statistics_telegram_transport

import (
	"errors"
	"fmt"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"

	tele "gopkg.in/telebot.v3"
)

func presentError(ctx tele.Context, err error) error {
	key := "statistics.read_failed"
	message := "❌ Не удалось прочитать статистику. Попробуйте позже."
	switch {
	case errors.Is(err, statistics_service.ErrOperationNotFound),
		errors.Is(err, statistics_service.ErrActionNotFound):
		key = "statistics.not_found"
		message = "⚠️ Операция не найдена. Обновите статистику."
	case errors.Is(err, statistics_service.ErrInvalidFilter):
		key = "statistics.stale_filter"
		message = "⚠️ Фильтр или кнопка устарели. Обновите статистику."
	case errors.Is(err, cardpublication_service.ErrManualResolutionConflict):
		key = "statistics.resolution_conflict"
		message = "⚠️ Состояние уже изменилось. Откройте карточку заново."
	case errors.Is(err, cardpublication_service.ErrManualEvidenceInvalid):
		key = "statistics.invalid_evidence"
		message = "⚠️ Не удалось подтвердить выбранный результат. Обновите карточку."
	}
	if responseErr := core_transport_telegram.Notify(ctx, key, message); responseErr != nil {
		return fmt.Errorf("present statistics error: %v: %w", responseErr, err)
	}
	return fmt.Errorf("present statistics error: %w", err)
}
