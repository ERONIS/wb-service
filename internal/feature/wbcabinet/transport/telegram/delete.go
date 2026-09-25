package wbcabinet_telegram_transport

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"strings"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"

	tele "gopkg.in/telebot.v3"
)

const cabinetCallbackKeyBytes = 12

func (handler *Handler) startDelete(ctx tele.Context) error {
	ownerTelegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return tele.ErrBadContext
	}
	handler.clearPending(ownerTelegramID)
	cabinets, err := handler.service.ListByOwner(handler.ctx, ownerTelegramID)
	if err != nil {
		return core_transport_telegram.Notify(
			ctx,
			"wbcabinet.delete.list",
			"❌ Не удалось загрузить кабинеты.",
		)
	}
	if len(cabinets) == 0 {
		return handler.showProfile(ctx, "Кабинетов для удаления нет.")
	}
	return ctx.EditOrSend(
		"🗑 <b>Удаление WB кабинета</b>\n\nВыберите свой кабинет:",
		deleteSelectionMarkup(cabinets),
	)
}

func (handler *Handler) confirmDelete(ctx tele.Context) error {
	ownerTelegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return tele.ErrBadContext
	}
	cabinet, err := handler.resolveCabinet(
		ownerTelegramID,
		ctx.Args(),
	)
	if err != nil {
		return cabinetCallbackError(ctx, "wbcabinet.delete.select", err)
	}
	return ctx.EditOrSend(
		fmt.Sprintf(
			"⚠️ Удалить кабинет <b>%s</b>?\n\nТокен и доступ к кабинету будут удалены из бота. Действие необратимо.",
			html.EscapeString(cabinet.Name),
		),
		confirmDeleteMarkup(cabinet),
	)
}

func (handler *Handler) deleteCabinet(ctx tele.Context) error {
	ownerTelegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return tele.ErrBadContext
	}
	cabinet, err := handler.resolveCabinet(
		ownerTelegramID,
		ctx.Args(),
	)
	if err != nil {
		return cabinetCallbackError(ctx, "wbcabinet.delete.confirm", err)
	}
	if err := handler.service.Delete(
		handler.ctx,
		ownerTelegramID,
		cabinet.ID,
	); err != nil {
		return cabinetCallbackError(ctx, "wbcabinet.delete", err)
	}
	return handler.showProfile(
		ctx,
		fmt.Sprintf("✅ Кабинет <b>%s</b> удалён.", html.EscapeString(cabinet.Name)),
	)
}

func (handler *Handler) resolveCabinet(
	ownerTelegramID int64,
	arguments []string,
) (wbcabinet_service.Cabinet, error) {
	key, err := parseCabinetCallbackKey(arguments)
	if err != nil {
		return wbcabinet_service.Cabinet{}, err
	}
	cabinets, err := handler.service.ListByOwner(handler.ctx, ownerTelegramID)
	if err != nil {
		return wbcabinet_service.Cabinet{}, err
	}
	var result wbcabinet_service.Cabinet
	found := false
	for _, cabinet := range cabinets {
		if cabinetCallbackKey(cabinet.ID) != key {
			continue
		}
		if found {
			return wbcabinet_service.Cabinet{}, core_errors.ErrConflict
		}
		result = cabinet
		found = true
	}
	if !found {
		return wbcabinet_service.Cabinet{}, core_errors.ErrNotFound
	}
	return result, nil
}

func deleteSelectionMarkup(cabinets []wbcabinet_service.Cabinet) *tele.ReplyMarkup {
	markup := &tele.ReplyMarkup{}
	rows := make([]tele.Row, 0, len(cabinets)+1)
	for _, cabinet := range cabinets {
		name := core_transport_telegram.TruncateRunes(
			strings.TrimSpace(cabinet.Name),
			48,
		)
		rows = append(rows, markup.Row(markup.Data(
			"🗑 "+name,
			buttonDeletePick.Unique,
			cabinetCallbackKey(cabinet.ID),
		)))
	}
	rows = append(rows, markup.Row(markup.Data(
		"↩️ В профиль",
		buttonCabinets.Unique,
	)))
	markup.Inline(rows...)
	return markup
}

func confirmDeleteMarkup(cabinet wbcabinet_service.Cabinet) *tele.ReplyMarkup {
	markup := &tele.ReplyMarkup{}
	markup.Inline(
		markup.Row(markup.Data(
			buttonDelete.Text,
			buttonDelete.Unique,
			cabinetCallbackKey(cabinet.ID),
		)),
		markup.Row(markup.Data("↩️ Отмена", buttonDeleteSelect.Unique)),
	)
	return markup
}

func cabinetCallbackKey(cabinetID wbcabinet_service.CabinetID) string {
	digest := sha256.Sum256([]byte("wbcabinet-delete:v1:" + string(cabinetID)))
	return base64.RawURLEncoding.EncodeToString(digest[:cabinetCallbackKeyBytes])
}

func parseCabinetCallbackKey(arguments []string) (string, error) {
	if len(arguments) != 1 {
		return "", core_errors.ErrInvalidArgument
	}
	key := strings.TrimSpace(arguments[0])
	decoded, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil || len(decoded) != cabinetCallbackKeyBytes ||
		base64.RawURLEncoding.EncodeToString(decoded) != key {
		return "", core_errors.ErrInvalidArgument
	}
	return key, nil
}

func cabinetCallbackError(ctx tele.Context, key string, err error) error {
	if errors.Is(err, core_errors.ErrNotFound) {
		return core_transport_telegram.Notify(
			ctx,
			key+".not_found",
			"Кабинет не найден или уже удалён.",
		)
	}
	if errors.Is(err, core_errors.ErrInvalidArgument) {
		return core_transport_telegram.Notify(
			ctx,
			key+".invalid",
			"Кнопка устарела. Откройте профиль заново.",
		)
	}
	return core_transport_telegram.Notify(
		ctx,
		key,
		"❌ Не удалось удалить кабинет.",
	)
}
