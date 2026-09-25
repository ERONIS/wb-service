package cabinetcopy_telegram_transport

import (
	"errors"
	"html"
	"strconv"
	"strings"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cabinetcopy_service "github.com/ERONIS/wb-service/internal/feature/cabinetcopy/service"

	tele "gopkg.in/telebot.v3"
)

func sessionArgs(args []string, exact int) (cabinetcopy_service.SessionID, int64, error) {
	if exact < 2 {
		return 0, 0, errors.New("invalid cabinet copy callback")
	}
	id, err := core_transport_telegram.ParseInt64Argument(args, 0, exact, 10, 1)
	if err != nil {
		return 0, 0, errors.New("invalid cabinet copy session ID")
	}
	revision, err := core_transport_telegram.ParseInt64Argument(args, 1, exact, 10, 0)
	if err != nil {
		return 0, 0, errors.New("invalid cabinet copy revision")
	}
	return cabinetcopy_service.SessionID(id), revision, nil
}

func decodeCabinet(value string, cabinets []cabinetcopy_service.Cabinet) (cabinetcopy_service.CabinetID, error) {
	index, err := strconv.Atoi(value)
	if err != nil || index < 0 || index >= len(cabinets) {
		return "", errors.New("invalid cabinet selection")
	}
	return cabinets[index].ID, nil
}

func callbackValues(session cabinetcopy_service.Session, extra ...string) []string {
	values := []string{
		strconv.FormatInt(int64(session.ID), 10),
		strconv.FormatInt(session.Revision, 10),
	}
	return append(values, extra...)
}

func cabinetName(cabinets []cabinetcopy_service.Cabinet, id cabinetcopy_service.CabinetID) string {
	for _, cabinet := range cabinets {
		if cabinet.ID == id {
			return cabinet.Name
		}
	}
	return string(id)
}

func escaped(value string) string { return html.EscapeString(strings.TrimSpace(value)) }

func presentError(ctx tele.Context, err error) error {
	return core_transport_telegram.NotifyError(
		ctx,
		err,
		core_transport_telegram.OnError(nil, "cabcopy.internal", "❌ Не удалось выполнить действие. Попробуйте ещё раз."),
		core_transport_telegram.OnError(cabinetcopy_service.ErrNotEnoughCards, "cabcopy.not_enough", "⚠️ По выбранным тегам найдено меньше карточек, чем указано. Начните заново с меньшим количеством."),
		core_transport_telegram.OnError(core_errors.ErrConflict, "cabcopy.conflict", "⚠️ Экран устарел. Откройте функцию заново."),
		core_transport_telegram.OnError(core_errors.ErrInvalidArgument, "cabcopy.invalid", "⚠️ Проверьте выбранные данные."),
		core_transport_telegram.OnError(core_errors.ErrNotFound, "cabcopy.not_found", "⚠️ Сеанс не найден. Откройте функцию заново."),
	)
}
