package statistics_telegram_transport

import (
	"fmt"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"

	tele "gopkg.in/telebot.v3"
)

func (handler *Handler) ShowFinalizedSession(
	ctx tele.Context,
	batch cardimport_service.BatchHeader,
) error {
	return handler.renderFinalizedSession(ctx, batch)
}

func (handler *Handler) openCurrentSession(ctx tele.Context) error {
	batchID, err := parsePositiveInt64(ctx.Args(), 0, 1)
	if err != nil {
		return staleCallback(ctx)
	}
	batch, err := handler.batches.GetBatch(handler.ctx, cardimport_service.BatchID(batchID))
	if err != nil {
		return presentError(ctx, err)
	}
	return handler.renderFinalizedSession(ctx, batch)
}

func (handler *Handler) renderFinalizedSession(
	ctx tele.Context,
	batch cardimport_service.BatchHeader,
) error {
	operations, err := handler.statistics.ListOperations(handler.ctx, statistics_service.OperationFilter{
		BatchID: int64(batch.ID),
		Limit:   1,
	})
	if err != nil {
		return presentError(ctx, err)
	}
	if len(operations) > 0 {
		return handler.renderOperation(ctx, operations[0].TransferID, "")
	}

	text := fmt.Sprintf(
		"📊 <b>Статистика сеанса №%d</b>\n\n"+
			"✅ Файлы приняты\n"+
			"Карточки: %d\n"+
			"Статус: <b>Подготавливаем отправку в WB</b>\n\n"+
			"После подготовки карточки будут отправлены автоматически.",
		batch.SourceSessionID,
		batch.ItemsCount,
	)
	markup := handler.bot.NewMarkup()
	markup.Inline(
		markup.Row(markup.Data(
			buttonCurrentSession.Text,
			buttonCurrentSession.Unique,
			callbackInt(int64(batch.ID)),
		)),
		markup.Row(markup.Data(
			"Все сеансы",
			buttonStatistics.Unique,
			callbackInt(int64(batch.ID)),
		)),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)
	return ctx.EditOrSend(text, markup)
}

var _ interface {
	ShowFinalizedSession(tele.Context, cardimport_service.BatchHeader) error
} = (*Handler)(nil)
