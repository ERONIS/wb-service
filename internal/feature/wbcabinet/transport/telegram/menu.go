package wbcabinet_telegram_transport

import (
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"

	tele "gopkg.in/telebot.v3"
)

const maxProfileCabinets = 8

type permissionLabel struct {
	permission wbcabinet_service.TokenProperties
	label      string
}

var permissionLabels = []permissionLabel{
	{wbcabinet_service.PermissionContent, "Контент"},
	{wbcabinet_service.PermissionAnalytics, "Аналитика"},
	{wbcabinet_service.PermissionPrices, "Цены и скидки"},
	{wbcabinet_service.PermissionMarketplace, "Маркетплейс"},
	{wbcabinet_service.PermissionStatistics, "Статистика"},
	{wbcabinet_service.PermissionPromotion, "Продвижение"},
	{wbcabinet_service.PermissionFeedbacks, "Вопросы и отзывы"},
	{wbcabinet_service.PermissionBuyersChat, "Чат с покупателями"},
	{wbcabinet_service.PermissionSupplies, "Поставки"},
	{wbcabinet_service.PermissionBuyersReturns, "Возвраты покупателей"},
	{wbcabinet_service.PermissionDocuments, "Документы"},
	{wbcabinet_service.PermissionFinance, "Финансы"},
	{wbcabinet_service.PermissionUsers, "Пользователи"},
}

func (handler *Handler) list(ctx tele.Context) error {
	sender := ctx.Sender()
	if sender == nil {
		return tele.ErrBadContext
	}
	handler.clearPending(sender.ID)
	return handler.showProfile(ctx, "")
}

func (handler *Handler) showProfile(ctx tele.Context, prefix string) error {
	sender := ctx.Sender()
	if sender == nil {
		return tele.ErrBadContext
	}
	profile, err := handler.profiles.GetProfile(handler.ctx, sender.ID)
	if err != nil {
		return core_transport_telegram.Notify(
			ctx,
			"wbcabinet.profile",
			"❌ Не удалось загрузить профиль.",
		)
	}
	cabinets, err := handler.service.ListByOwner(handler.ctx, sender.ID)
	if err != nil {
		return core_transport_telegram.Notify(
			ctx,
			"wbcabinet.list",
			"❌ Не удалось загрузить кабинеты.",
		)
	}
	return ctx.EditOrSend(
		profileText(profile, profileUsername(ctx, profile), cabinets, prefix),
		profileMarkup(len(cabinets) > 0),
	)
}

func profileText(
	profile domain.User,
	username string,
	cabinets []wbcabinet_service.Cabinet,
	prefix string,
) string {
	var message strings.Builder
	if prefix != "" {
		message.WriteString(prefix)
		message.WriteString("\n\n")
	}
	fmt.Fprintf(
		&message,
		"👤 <b>Профиль</b>\n\n"+
			"Имя: <b>%s</b>\n"+
			"TG ID: %d\n"+
			"Username: %s\n"+
			"Роль: %s\n"+
			"Кабинетов: %d",
		html.EscapeString(strings.TrimSpace(profile.FullName)),
		profile.TelegramID,
		html.EscapeString(username),
		roleText(profile.Role),
		len(cabinets),
	)
	message.WriteString("\n\n🔑 <b>WB кабинеты</b>\n")
	if len(cabinets) == 0 {
		message.WriteString("Кабинетов пока нет. Добавьте API-кабинет кнопкой ниже.")
		return message.String()
	}

	shown := len(cabinets)
	if shown > maxProfileCabinets {
		shown = maxProfileCabinets
	}
	for index, cabinet := range cabinets[:shown] {
		status := cabinet.Status
		if status == wbcabinet_service.StatusActive &&
			!cabinet.CredentialExpiresAt.After(time.Now()) {
			status = wbcabinet_service.StatusExpired
		}
		fmt.Fprintf(
			&message,
			"\n%d. <b>%s</b>\n"+
				"Статус: %s\n"+
				"API: %s\n"+
				"Доступ: %s\n"+
				"Токен до: %s\n",
			index+1,
			html.EscapeString(cabinet.Name),
			statusText(status),
			permissionsText(cabinet.TokenProperties),
			accessModeText(cabinet.TokenProperties),
			cabinet.CredentialExpiresAt.Local().Format("02.01.2006 15:04"),
		)
	}
	if len(cabinets) > shown {
		fmt.Fprintf(&message, "\n… и ещё %d", len(cabinets)-shown)
	}
	return strings.TrimSpace(message.String())
}

func profileUsername(ctx tele.Context, profile domain.User) string {
	if sender := ctx.Sender(); sender != nil {
		if username := strings.TrimSpace(sender.Username); username != "" {
			return "@" + strings.TrimPrefix(username, "@")
		}
	}
	if profile.TelegramNickname != nil {
		if username := strings.TrimSpace(*profile.TelegramNickname); username != "" {
			return "@" + strings.TrimPrefix(username, "@")
		}
	}
	return "—"
}

func roleText(role domain.UserRole) string {
	switch role {
	case domain.RoleAdmin:
		return "администратор"
	case domain.RolePartner:
		return "партнёр"
	default:
		return "пользователь"
	}
}

func permissionsText(properties wbcabinet_service.TokenProperties) string {
	permissions := make([]string, 0, len(permissionLabels))
	for _, item := range permissionLabels {
		if properties.Has(item.permission) {
			permissions = append(permissions, item.label)
		}
	}
	if len(permissions) == 0 {
		return "не определены"
	}
	return strings.Join(permissions, ", ")
}

func accessModeText(properties wbcabinet_service.TokenProperties) string {
	if properties == 0 {
		return "не определён"
	}
	if properties.Has(wbcabinet_service.PermissionReadOnly) {
		return "только чтение"
	}
	return "чтение и запись"
}

func statusText(status wbcabinet_service.Status) string {
	switch status {
	case wbcabinet_service.StatusActive:
		return "✅ активен"
	case wbcabinet_service.StatusExpired:
		return "⌛ токен истёк"
	case wbcabinet_service.StatusIdentityMismatch:
		return "⛔ продавец не совпадает"
	default:
		return "⚠️ требуется повторная проверка"
	}
}

func profileMarkup(hasCabinets bool) *tele.ReplyMarkup {
	markup := &tele.ReplyMarkup{}
	rows := []tele.Row{
		markup.Row(markup.Data(buttonAdd.Text, buttonAdd.Unique)),
	}
	if hasCabinets {
		rows = append(rows, markup.Row(markup.Data(
			buttonDeleteSelect.Text,
			buttonDeleteSelect.Unique,
		)))
	}
	rows = append(rows, markup.Row(core_transport_telegram.MainMenuButton()))
	markup.Inline(rows...)
	return markup
}

func addPromptMarkup() *tele.ReplyMarkup {
	markup := &tele.ReplyMarkup{}
	markup.Inline(markup.Row(markup.Data(buttonCancel.Text, buttonCancel.Unique)))
	return markup
}

func serviceErrorText(err error) string {
	switch {
	case errors.Is(err, wbcabinet_service.ErrInvalidCredential):
		return "⚠️ Токен имеет неверный формат. Отправьте корректный API-токен."
	case errors.Is(err, wbcabinet_service.ErrCredentialExpired):
		return "⌛ Срок действия токена истёк. Создайте новый токен в кабинете WB."
	case errors.Is(err, wbcabinet_service.ErrContentAccess):
		return "⛔ Токену нужны права Content на чтение и запись."
	case errors.Is(err, wbcabinet_service.ErrIdentityMismatch):
		return "⛔ Токен принадлежит другому WB-продавцу. Старый кабинет не изменён."
	case errors.Is(err, wbcabinet_service.ErrDuplicateSeller):
		return "⚠️ Этот WB-продавец уже зарегистрирован в другом кабинете."
	case errors.Is(err, wbcabinet_service.ErrCabinetNameTaken):
		return "⚠️ Кабинет с таким названием уже существует."
	case errors.Is(err, wbcabinet_service.ErrVerificationRateLimited):
		return "⏳ WB временно ограничил проверку токенов. Подождите 30 секунд и отправьте токен снова."
	case errors.Is(err, wbcabinet_service.ErrVerification):
		return "❌ WB не подтвердил токен. Проверьте токен или повторите позже."
	default:
		return "❌ Не удалось добавить кабинет. Попробуйте позже."
	}
}
