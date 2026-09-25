package cardedit_telegram_transport

import (
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardedit_service "github.com/ERONIS/wb-service/internal/feature/cardedit/service"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	cardimport_xlsx_transport "github.com/ERONIS/wb-service/internal/feature/cardimport/transport/xlsx"

	tele "gopkg.in/telebot.v3"
)

func (handler *Handler) start(ctx tele.Context) error {
	telegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return sendError(ctx, err)
	}
	handler.setSession(telegramID, session{
		step:      stepVendorCode,
		expiresAt: time.Now().Add(handler.flowTTL),
	})
	handler.menu.BeginTextFlow(telegramID, flowKey)
	return ctx.EditOrSend(
		"✏️ <b>Редактирование карточки</b>\n\nВведите артикул продавца:",
		handler.cancelMarkup(),
	)
}

func (handler *Handler) receiveText(ctx tele.Context) (bool, error) {
	telegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return true, sendError(ctx, err)
	}
	current, found := handler.getSession(telegramID)
	if !found {
		handler.clearSession(telegramID)
		return true, ctx.EditOrSend("⏳ Сессия редактирования устарела. Запустите /editcard заново.", handler.backMarkup())
	}
	if current.step == stepDocument {
		return true, core_transport_telegram.Notify(ctx, "cardedit.await_document", "⚠️ Теперь отправьте Excel-файл документом.")
	}
	if current.step != stepVendorCode {
		return true, core_transport_telegram.Notify(ctx, "cardedit.await_confirm", "⚠️ Сначала подтвердите найденные карточки кнопкой.")
	}
	vendorCode := strings.TrimSpace(ctx.Text())
	if vendorCode == "" {
		return true, core_transport_telegram.Notify(ctx, "cardedit.vendor_empty", "⚠️ Артикул продавца не может быть пустым.")
	}
	if err := ctx.EditOrSend("🔎 Ищу карточку в ваших активных кабинетах…", handler.cancelMarkup()); err != nil {
		return true, err
	}
	targets, err := handler.service.Find(handler.ctx, telegramID, vendorCode)
	if err != nil {
		return true, sendError(ctx, err)
	}
	if len(targets) == 0 {
		handler.clearSession(telegramID)
		return true, ctx.EditOrSend(
			fmt.Sprintf("❌ Карточка с артикулом %s не найдена в ваших активных кабинетах.", html.EscapeString(vendorCode)),
			handler.backMarkup(),
		)
	}
	handler.setSession(telegramID, session{
		step:       stepConfirm,
		expiresAt:  time.Now().Add(handler.flowTTL),
		vendorCode: vendorCode,
		targets:    targets,
	})
	return true, ctx.EditOrSend(foundText(vendorCode, targets), handler.confirmMarkup())
}

func (handler *Handler) confirm(ctx tele.Context) error {
	telegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return sendError(ctx, err)
	}
	current, found := handler.getSession(telegramID)
	if !found || current.step != stepConfirm {
		return core_transport_telegram.Notify(ctx, "cardedit.confirm_expired", "⏳ Подтверждение устарело. Запустите /editcard заново.")
	}
	current.step = stepDocument
	current.expiresAt = time.Now().Add(handler.flowTTL)
	handler.setSession(telegramID, current)
	handler.menu.BeginTextFlow(telegramID, flowKey)
	return ctx.EditOrSend(
		fmt.Sprintf(
			"✅ Выбрано карточек: %d.\n\nОтправьте Excel-файл с данными артикула %s. Размеры и баркоды будут сохранены из текущей карточки WB.",
			len(current.targets),
			html.EscapeString(current.vendorCode),
		),
		handler.cancelMarkup(),
	)
}

func (handler *Handler) continueEdit(ctx tele.Context) error {
	telegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return sendError(ctx, err)
	}
	current, found := handler.getSession(telegramID)
	if !found || current.step != stepDocument {
		return core_transport_telegram.Notify(ctx, "cardedit.continue_expired", "⏳ Сессия редактирования устарела. Запустите /editcard заново.")
	}
	handler.keepDocumentSession(telegramID, current)
	return ctx.EditOrSend(
		fmt.Sprintf(
			"➡️ Отправьте исправленный Excel-файл с данными артикула %s. Размеры и баркоды будут сохранены из текущей карточки WB.",
			html.EscapeString(current.vendorCode),
		),
		handler.cancelMarkup(),
	)
}

func (handler *Handler) cancel(ctx tele.Context) error {
	telegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return sendError(ctx, err)
	}
	handler.clearSession(telegramID)
	return ctx.EditOrSend("Редактирование карточки отменено.", handler.backMarkup())
}

func (handler *Handler) receiveDocument(ctx tele.Context) (bool, error) {
	telegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return true, sendError(ctx, err)
	}
	current, found := handler.getSession(telegramID)
	if !found || current.step != stepDocument {
		return false, nil
	}
	message := ctx.Message()
	if message == nil || message.Document == nil {
		return handler.documentError(ctx, telegramID, current, "⚠️ Отправьте Excel-файл документом.")
	}
	document := message.Document
	if !cardimport_xlsx_transport.IsFile(document.FileName, document.MIME) {
		return handler.documentError(ctx, telegramID, current, "⚠️ Ожидается файл формата .xlsx.")
	}
	reader, err := handler.bot.File(&document.File)
	if err != nil {
		return handler.documentError(ctx, telegramID, current, "❌ Не удалось скачать файл.")
	}
	parsed, parseErr := handler.parser.Parse(cardimport_service.FileID(1), reader)
	_ = reader.Close()
	if parseErr != nil {
		return handler.documentError(ctx, telegramID, current, "❌ Не удалось разобрать Excel-файл. Проверьте, что это шаблон карточек WB.")
	}
	aggregated := cardimport_service.AggregateParsedFile(parsed)
	if aggregated.HasErrors() {
		handler.keepDocumentSession(telegramID, current)
		return true, ctx.EditOrSend(parseIssuesText(aggregated.Issues), handler.retryMarkup())
	}
	var editedCard *cardimport_service.AggregatedCard
	for index := range aggregated.Cards {
		if strings.EqualFold(strings.TrimSpace(aggregated.Cards[index].Variant.VendorCode), current.vendorCode) {
			editedCard = &aggregated.Cards[index]
			break
		}
	}
	if editedCard == nil {
		return handler.documentError(
			ctx,
			telegramID,
			current,
			fmt.Sprintf("❌ Артикул %s не найден в Excel-файле.", html.EscapeString(current.vendorCode)),
		)
	}
	err = handler.service.Submit(cardedit_service.Job{
		OwnerTelegramID: telegramID,
		VendorCode:      current.vendorCode,
		Card:            *editedCard,
		Targets:         current.targets,
	})
	if err != nil {
		switch {
		case errors.Is(err, cardedit_service.ErrAlreadyActive):
			return handler.documentError(ctx, telegramID, current, "⚠️ Эта карточка уже редактируется. Дождитесь результата и попробуйте продолжить.")
		case errors.Is(err, cardedit_service.ErrQueueFull):
			return handler.documentError(ctx, telegramID, current, "⚠️ Очередь редактирования заполнена. Попробуйте продолжить позже.")
		default:
			return handler.documentError(ctx, telegramID, current, "❌ Не удалось запустить редактирование. Попробуйте продолжить позже.")
		}
	}
	handler.clearSession(telegramID)
	return true, ctx.EditOrSend(
		fmt.Sprintf(
			"📨 Карточка %s отправлена на редактирование в %d кабинет(а). Результат придёт отдельным сообщением.",
			html.EscapeString(current.vendorCode),
			len(current.targets),
		),
		handler.backMarkup(),
	)
}

func (handler *Handler) keepDocumentSession(telegramID int64, current session) {
	current.step = stepDocument
	current.expiresAt = time.Now().Add(handler.flowTTL)
	handler.setSession(telegramID, current)
	handler.menu.BeginTextFlow(telegramID, flowKey)
}

func (handler *Handler) documentError(
	ctx tele.Context,
	telegramID int64,
	current session,
	message string,
) (bool, error) {
	handler.keepDocumentSession(telegramID, current)
	return true, ctx.EditOrSend(
		message+"\n\nСессия сохранена. Нажмите «Продолжить» и отправьте исправленный файл.",
		handler.retryMarkup(),
	)
}

func sendError(ctx tele.Context, err error) error {
	return core_transport_telegram.NotifyError(
		ctx,
		err,
		core_transport_telegram.OnError(nil, "cardedit.internal", "❌ Не удалось выполнить редактирование. Попробуйте позже."),
		core_transport_telegram.OnError(core_errors.ErrForbidden, "cardedit.forbidden", "⛔ Недостаточно прав."),
		core_transport_telegram.OnError(core_errors.ErrInvalidArgument, "cardedit.invalid", "⚠️ Проверьте введённые данные."),
	)
}
