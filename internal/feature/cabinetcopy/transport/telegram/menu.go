package cabinetcopy_telegram_transport

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cabinetcopy_service "github.com/ERONIS/wb-service/internal/feature/cabinetcopy/service"

	tele "gopkg.in/telebot.v3"
)

const tagPageSize = 8

func (handler *Handler) begin(ctx tele.Context) error {
	author, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	session, err := handler.service.Begin(handler.ctx, author)
	if err != nil {
		return presentError(ctx, err)
	}
	return handler.render(ctx, session, 1)
}

func (handler *Handler) render(ctx tele.Context, session cabinetcopy_service.Session, page int) error {
	switch session.Step {
	case cabinetcopy_service.StepSource:
		return handler.renderSource(ctx, session)
	case cabinetcopy_service.StepTarget:
		return handler.renderTarget(ctx, session)
	case cabinetcopy_service.StepCount:
		return handler.renderCount(ctx, session)
	case cabinetcopy_service.StepCountInput:
		return handler.renderCountInput(ctx, session)
	case cabinetcopy_service.StepTags:
		return handler.renderTags(ctx, session, page)
	case cabinetcopy_service.StepReview:
		return handler.renderReview(ctx, session)
	case cabinetcopy_service.StepCancelled:
		return handler.renderCancelled(ctx)
	default:
		return presentError(ctx, core_errors.ErrConflict)
	}
}

func (handler *Handler) renderSource(ctx tele.Context, session cabinetcopy_service.Session) error {
	cabinets, err := handler.service.CabinetsForOwner(handler.ctx, session.AuthorTelegramID)
	if err != nil {
		return presentError(ctx, err)
	}
	markup := handler.bot.NewMarkup()
	rows := make([]tele.Row, 0, len(cabinets)+2)
	for index, cabinet := range cabinets {
		rows = append(rows, markup.Row(markup.Data(
			"🏬 "+cabinet.Name,
			buttonSource.Unique,
			callbackValues(session, strconv.Itoa(index))...,
		)))
	}
	rows = append(rows, markup.Row(markup.Data(buttonCancel.Text, buttonCancel.Unique, callbackValues(session)...)))
	rows = append(rows, markup.Row(core_transport_telegram.MainMenuButton()))
	markup.Inline(rows...)
	return ctx.EditOrSend("🔁 <b>Копирование между кабинетами</b>\n\nВыберите кабинет-источник:", markup)
}

func (handler *Handler) renderTarget(ctx tele.Context, session cabinetcopy_service.Session) error {
	cabinets, err := handler.service.CabinetsForOwner(handler.ctx, session.AuthorTelegramID)
	if err != nil {
		return presentError(ctx, err)
	}
	markup := handler.bot.NewMarkup()
	rows := make([]tele.Row, 0, len(cabinets)+2)
	for index, cabinet := range cabinets {
		if cabinet.ID == session.SourceCabinetID {
			continue
		}
		rows = append(rows, markup.Row(markup.Data(
			"🏬 "+cabinet.Name,
			buttonTarget.Unique,
			callbackValues(session, strconv.Itoa(index))...,
		)))
	}
	rows = append(rows, markup.Row(markup.Data(buttonCancel.Text, buttonCancel.Unique, callbackValues(session)...)))
	rows = append(rows, markup.Row(core_transport_telegram.MainMenuButton()))
	markup.Inline(rows...)
	text := fmt.Sprintf("🔁 <b>Копирование между кабинетами</b>\n\nИсточник: <b>%s</b>\n\nВыберите кабинет назначения:", escaped(cabinetName(cabinets, session.SourceCabinetID)))
	return ctx.EditOrSend(text, markup)
}

func (handler *Handler) renderCount(ctx tele.Context, session cabinetcopy_service.Session) error {
	markup := handler.bot.NewMarkup()
	values := []int{10, 25, 50, 100}
	buttons := make([]tele.Btn, 0, len(values))
	seen := make(map[int]struct{})
	for _, value := range values {
		if value > handler.service.MaxCards() {
			continue
		}
		seen[value] = struct{}{}
		buttons = append(buttons, markup.Data(strconv.Itoa(value), buttonCount.Unique, callbackValues(session, strconv.Itoa(value))...))
	}
	if _, exists := seen[handler.service.MaxCards()]; !exists && len(buttons) == 0 {
		buttons = append(buttons, markup.Data(strconv.Itoa(handler.service.MaxCards()), buttonCount.Unique, callbackValues(session, strconv.Itoa(handler.service.MaxCards()))...))
	}
	rows := make([]tele.Row, 0, 4)
	for start := 0; start < len(buttons); start += 2 {
		end := start + 2
		if end > len(buttons) {
			end = len(buttons)
		}
		rows = append(rows, markup.Row(buttons[start:end]...))
	}
	rows = append(rows, markup.Row(markup.Data(buttonCustomCount.Text, buttonCustomCount.Unique, callbackValues(session)...)))
	rows = append(rows, markup.Row(markup.Data(buttonCancel.Text, buttonCancel.Unique, callbackValues(session)...)))
	rows = append(rows, markup.Row(core_transport_telegram.MainMenuButton()))
	markup.Inline(rows...)
	return ctx.EditOrSend(fmt.Sprintf("🔢 <b>Количество карточек</b>\n\nВыберите количество от 1 до %d:", handler.service.MaxCards()), markup)
}

func (handler *Handler) renderCountInput(ctx tele.Context, session cabinetcopy_service.Session) error {
	markup := handler.bot.NewMarkup()
	markup.Inline(
		markup.Row(markup.Data(buttonCancel.Text, buttonCancel.Unique, callbackValues(session)...)),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)
	return ctx.EditOrSend(fmt.Sprintf("✍️ Отправьте одним сообщением количество карточек от 1 до %d.", handler.service.MaxCards()), markup)
}

func (handler *Handler) renderTags(ctx tele.Context, session cabinetcopy_service.Session, page int) error {
	author := session.AuthorTelegramID
	tags, err := handler.service.ListTags(handler.ctx, author, session.ID)
	if err != nil {
		return presentError(ctx, err)
	}
	if page < 1 {
		page = 1
	}
	totalPages := (len(tags) + tagPageSize - 1) / tagPageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	selected := make(map[int64]struct{}, len(session.SelectedTagIDs))
	for _, id := range session.SelectedTagIDs {
		selected[id] = struct{}{}
	}
	markup := handler.bot.NewMarkup()
	rows := make([]tele.Row, 0, tagPageSize+4)
	start := (page - 1) * tagPageSize
	end := start + tagPageSize
	if end > len(tags) {
		end = len(tags)
	}
	for _, tag := range tags[start:end] {
		prefix := "▫️ "
		if _, exists := selected[tag.ID]; exists {
			prefix = "✅ "
		}
		rows = append(rows, markup.Row(markup.Data(
			prefix+tag.Name,
			buttonTag.Unique,
			callbackValues(session, strconv.FormatInt(tag.ID, 10), strconv.Itoa(page))...,
		)))
	}
	if totalPages > 1 {
		paging := make([]tele.Btn, 0, 2)
		if page > 1 {
			paging = append(paging, markup.Data("⬅️", buttonTagPage.Unique, callbackValues(session, strconv.Itoa(page-1))...))
		}
		if page < totalPages {
			paging = append(paging, markup.Data("➡️", buttonTagPage.Unique, callbackValues(session, strconv.Itoa(page+1))...))
		}
		rows = append(rows, markup.Row(paging...))
	}
	continueText := "Продолжить без фильтра"
	if len(selected) > 0 {
		continueText = fmt.Sprintf("Продолжить · тегов: %d", len(selected))
	}
	rows = append(rows, markup.Row(markup.Data(continueText, buttonPrepare.Unique, callbackValues(session)...)))
	rows = append(rows, markup.Row(markup.Data(buttonCancel.Text, buttonCancel.Unique, callbackValues(session)...)))
	rows = append(rows, markup.Row(core_transport_telegram.MainMenuButton()))
	markup.Inline(rows...)
	text := fmt.Sprintf("🏷 <b>Фильтр по тегам</b>\n\nКоличество: <b>%d</b>\nВыберите теги или продолжите без фильтра.", session.RequestedCount)
	if len(tags) == 0 {
		text += "\n\nУ исходного кабинета нет тегов."
	}
	return ctx.EditOrSend(text, markup)
}

func (handler *Handler) renderReview(ctx tele.Context, session cabinetcopy_service.Session) error {
	cabinets, err := handler.service.CabinetsForOwner(handler.ctx, session.AuthorTelegramID)
	if err != nil {
		return presentError(ctx, err)
	}
	tags := "Без фильтра"
	if len(session.SelectedTagNames) > 0 {
		tags = escaped(strings.Join(session.SelectedTagNames, ", "))
	}
	text := fmt.Sprintf(
		"🔍 <b>Проверка копирования</b>\n\nИсточник: <b>%s</b>\nНазначение: <b>%s</b>\nКарточки: <b>%d</b>\nТеги: %s\n\nЦены и медиа получены. Исходные баркоды не переносятся.",
		escaped(cabinetName(cabinets, session.SourceCabinetID)),
		escaped(cabinetName(cabinets, session.TargetCabinetID)),
		session.PreparedCount,
		tags,
	)
	markup := handler.bot.NewMarkup()
	rows := []tele.Row{
		markup.Row(markup.Data(buttonSubmit.Text, buttonSubmit.Unique, callbackValues(session)...)),
	}
	if session.BatchID == 0 {
		rows = append(rows, markup.Row(markup.Data(buttonCancel.Text, buttonCancel.Unique, callbackValues(session)...)))
	} else {
		text += "\n\nПакет уже зафиксирован. Для безопасного продолжения повторно нажмите «Запустить»."
	}
	rows = append(rows, markup.Row(core_transport_telegram.MainMenuButton()))
	markup.Inline(rows...)
	return ctx.EditOrSend(text, markup)
}

func (handler *Handler) renderCancelled(ctx tele.Context) error {
	markup := handler.bot.NewMarkup()
	markup.Inline(markup.Row(core_transport_telegram.MainMenuButton()))
	return ctx.EditOrSend("Копирование карточек отменено.", markup)
}

func (handler *Handler) selectSource(ctx tele.Context) error {
	id, revision, err := sessionArgs(ctx.Args(), 3)
	if err != nil {
		return presentError(ctx, core_errors.ErrConflict)
	}
	author, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	cabinets, err := handler.service.CabinetsForOwner(handler.ctx, author)
	if err != nil {
		return presentError(ctx, err)
	}
	cabinet, err := decodeCabinet(ctx.Args()[2], cabinets)
	if err != nil {
		return presentError(ctx, core_errors.ErrInvalidArgument)
	}
	session, err := handler.service.SelectSource(handler.ctx, author, id, revision, cabinet)
	if err != nil {
		return presentError(ctx, err)
	}
	return handler.render(ctx, session, 1)
}

func (handler *Handler) selectTarget(ctx tele.Context) error {
	id, revision, err := sessionArgs(ctx.Args(), 3)
	if err != nil {
		return presentError(ctx, core_errors.ErrConflict)
	}
	author, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	cabinets, err := handler.service.CabinetsForOwner(handler.ctx, author)
	if err != nil {
		return presentError(ctx, err)
	}
	cabinet, err := decodeCabinet(ctx.Args()[2], cabinets)
	if err != nil {
		return presentError(ctx, core_errors.ErrInvalidArgument)
	}
	session, err := handler.service.SelectTarget(handler.ctx, author, id, revision, cabinet)
	if err != nil {
		return presentError(ctx, err)
	}
	return handler.render(ctx, session, 1)
}

func (handler *Handler) selectCount(ctx tele.Context) error {
	id, revision, err := sessionArgs(ctx.Args(), 3)
	if err != nil {
		return presentError(ctx, core_errors.ErrConflict)
	}
	countValue, err := core_transport_telegram.ParseInt64Argument(
		ctx.Args(),
		2,
		3,
		10,
		1,
	)
	if err != nil {
		return presentError(ctx, core_errors.ErrInvalidArgument)
	}
	count := int(countValue)
	author, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	session, err := handler.service.SelectCount(handler.ctx, author, id, revision, count)
	if err != nil {
		return presentError(ctx, err)
	}
	return handler.render(ctx, session, 1)
}

func (handler *Handler) requestCustomCount(ctx tele.Context) error {
	id, revision, err := sessionArgs(ctx.Args(), 2)
	if err != nil {
		return presentError(ctx, core_errors.ErrConflict)
	}
	author, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	session, err := handler.service.RequestCountInput(handler.ctx, author, id, revision)
	if err != nil {
		return presentError(ctx, err)
	}
	handler.textFlows.BeginTextFlow(author, countTextFlow)
	return handler.render(ctx, session, 1)
}

func (handler *Handler) receiveCount(ctx tele.Context) (bool, error) {
	author, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return false, nil
	}
	session, err := handler.service.GetActive(handler.ctx, author)
	if errors.Is(err, core_errors.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return true, presentError(ctx, err)
	}
	if session.Step != cabinetcopy_service.StepCountInput {
		return false, nil
	}
	count, err := strconv.Atoi(strings.TrimSpace(ctx.Text()))
	if err != nil || count <= 0 || count > handler.service.MaxCards() {
		return true, core_transport_telegram.Notify(ctx, "cabcopy.count", fmt.Sprintf("⚠️ Отправьте целое число от 1 до %d.", handler.service.MaxCards()))
	}
	session, err = handler.service.SelectCount(handler.ctx, author, session.ID, session.Revision, count)
	if err != nil {
		return true, presentError(ctx, err)
	}
	handler.textFlows.EndTextFlow(author, countTextFlow)
	return true, handler.render(ctx, session, 1)
}

func (handler *Handler) toggleTag(ctx tele.Context) error {
	id, revision, err := sessionArgs(ctx.Args(), 4)
	if err != nil {
		return presentError(ctx, core_errors.ErrConflict)
	}
	tagID, err := core_transport_telegram.ParseInt64Argument(
		ctx.Args(),
		2,
		4,
		10,
		1,
	)
	if err != nil {
		return presentError(ctx, core_errors.ErrInvalidArgument)
	}
	pageValue, err := core_transport_telegram.ParseInt64Argument(
		ctx.Args(),
		3,
		4,
		10,
		1,
	)
	if err != nil {
		return presentError(ctx, core_errors.ErrInvalidArgument)
	}
	page := int(pageValue)
	author, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	session, err := handler.service.ToggleTag(handler.ctx, author, id, revision, tagID)
	if err != nil {
		return presentError(ctx, err)
	}
	return handler.render(ctx, session, page)
}

func (handler *Handler) openTagPage(ctx tele.Context) error {
	id, revision, err := sessionArgs(ctx.Args(), 3)
	if err != nil {
		return presentError(ctx, core_errors.ErrConflict)
	}
	pageValue, err := core_transport_telegram.ParseInt64Argument(
		ctx.Args(),
		2,
		3,
		10,
		1,
	)
	if err != nil {
		return presentError(ctx, core_errors.ErrInvalidArgument)
	}
	page := int(pageValue)
	author, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	session, err := handler.service.Get(handler.ctx, author, id)
	if err != nil || session.Revision != revision || session.Step != cabinetcopy_service.StepTags {
		return presentError(ctx, core_errors.ErrConflict)
	}
	return handler.render(ctx, session, page)
}

func (handler *Handler) prepare(ctx tele.Context) error {
	id, revision, err := sessionArgs(ctx.Args(), 2)
	if err != nil {
		return presentError(ctx, core_errors.ErrConflict)
	}
	markup := handler.bot.NewMarkup()
	markup.Inline(markup.Row(core_transport_telegram.MainMenuButton()))
	if err := ctx.EditOrSend("⏳ Получаем карточки и цены из WB…", markup); err != nil {
		return err
	}
	author, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	session, err := handler.service.Prepare(handler.ctx, author, id, revision)
	if err != nil {
		return presentError(ctx, err)
	}
	return handler.render(ctx, session, 1)
}

func (handler *Handler) submit(ctx tele.Context) error {
	id, revision, err := sessionArgs(ctx.Args(), 2)
	if err != nil {
		return presentError(ctx, core_errors.ErrConflict)
	}
	actor, err := core_transport_telegram.ActorFromContext(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	submission, err := handler.service.Submit(handler.ctx, actor, id, revision)
	if err != nil {
		return presentError(ctx, err)
	}
	handler.processing.NotifyFinalized()
	return handler.completion.ShowFinalizedSession(ctx, submission.Batch)
}

func (handler *Handler) cancel(ctx tele.Context) error {
	id, revision, err := sessionArgs(ctx.Args(), 2)
	if err != nil {
		return presentError(ctx, core_errors.ErrConflict)
	}
	author, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	session, err := handler.service.Cancel(handler.ctx, author, id, revision)
	if err != nil {
		return presentError(ctx, err)
	}
	handler.textFlows.EndTextFlow(author, countTextFlow)
	return handler.render(ctx, session, 1)
}
