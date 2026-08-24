package transfer_telegram_transport

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	tele "gopkg.in/telebot.v3"
)

func (handler *Handler) showPlans(ctx tele.Context) error {
	actor, err := trustedActor(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	plans, err := handler.plans.ListAuthorizationPlans(
		handler.ctx,
		actor.TelegramUserID,
		20,
	)
	if err != nil {
		return presentError(ctx, err)
	}
	markup := handler.bot.NewMarkup()
	rows := make([]tele.Row, 0, len(plans)+1)
	for _, plan := range plans {
		text := fmt.Sprintf(
			"Transfer №%d · %d действий",
			plan.TransferID,
			plan.ActionsCount(),
		)
		rows = append(rows, markup.Row(markup.Data(
			text,
			buttonOpenPlan.Unique,
			strconv.FormatInt(int64(plan.TransferID), 10),
		)))
	}
	rows = append(rows, markup.Row(core_transport_telegram.MainMenuButton()))
	markup.Inline(rows...)
	text := "🚚 <b>Планы отправки в WB</b>\n\n"
	if len(plans) == 0 {
		text += "Планов, ожидающих вашего подтверждения, нет."
	} else {
		text += "Выберите подготовленный transfer. До подтверждения WB mutations не выполняются."
	}
	return ctx.EditOrSend(text, markup)
}

func (handler *Handler) openPlan(ctx tele.Context) error {
	actor, err := trustedActor(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	transferID, err := parseTransferID(ctx.Args())
	if err != nil {
		return ctx.RespondAlert("Кнопка устарела. Откройте список планов заново.")
	}
	plan, err := handler.plans.GetAuthorizationPlan(
		handler.ctx,
		actor.TelegramUserID,
		transferID,
	)
	if err != nil {
		return presentError(ctx, err)
	}
	if !handler.authorization.LiveEnabled() {
		markup := handler.bot.NewMarkup()
		markup.Inline(
			markup.Row(buttonTransfers),
			markup.Row(core_transport_telegram.MainMenuButton()),
		)
		return ctx.EditOrSend(
			planText(plan)+"\n\n⚠️ <code>TRANSFER_MODE</code> не равен <code>live</code>. Подтверждение отключено.",
			markup,
		)
	}
	authorization, err := handler.authorization.Request(
		handler.ctx,
		actor,
		transfer_service.RequestLiveCommand{
			TransferID: plan.TransferID,
			PlanDigest: plan.PlanDigest,
			IdempotencyKey: callbackIdempotencyKey(
				ctx,
				"request-live",
				int64(plan.TransferID),
			),
		},
	)
	if err != nil {
		return presentError(ctx, err)
	}
	markup := handler.bot.NewMarkup()
	if authorization.State == transfer_service.LiveAuthorizationAuthorized {
		markup.Inline(
			markup.Row(markup.Data(
				buttonRevoke.Text,
				buttonRevoke.Unique,
				strconv.FormatInt(int64(plan.TransferID), 10),
				strconv.FormatInt(int64(authorization.ID), 10),
				strconv.FormatInt(authorization.Revision, 10),
			)),
			markup.Row(buttonTransfers),
			markup.Row(core_transport_telegram.MainMenuButton()),
		)
		return ctx.EditOrSend(
			planText(plan)+fmt.Sprintf(
				"\n\n✅ План уже подтверждён authorization №%d до <code>%s UTC</code>.",
				authorization.ID,
				authorization.ExpiresAt.UTC().Format("2006-01-02 15:04:05"),
			),
			markup,
		)
	}
	markup.Inline(
		markup.Row(markup.Data(
			buttonApprove.Text,
			buttonApprove.Unique,
			strconv.FormatInt(int64(plan.TransferID), 10),
			strconv.FormatInt(int64(authorization.ID), 10),
			strconv.FormatInt(authorization.Revision, 10),
		)),
		markup.Row(buttonTransfers),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)
	return ctx.EditOrSend(
		planText(plan)+fmt.Sprintf(
			"\n\nРазрешение действует до <code>%s UTC</code>. Нажатие подтвердит только PlanDigest <code>%s</code>.",
			authorization.ExpiresAt.UTC().Format("2006-01-02 15:04:05"),
			hex.EncodeToString(plan.PlanDigest[:])[:12],
		),
		markup,
	)
}

func (handler *Handler) approvePlan(ctx tele.Context) error {
	actor, err := trustedActor(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	transferID, authorizationID, revision, err := parseApprove(ctx.Args())
	if err != nil {
		return ctx.RespondAlert("Кнопка устарела. Откройте план заново.")
	}
	plan, err := handler.plans.GetAuthorizationPlan(
		handler.ctx,
		actor.TelegramUserID,
		transferID,
	)
	if err != nil {
		return presentError(ctx, err)
	}
	authorization, err := handler.authorization.Approve(
		handler.ctx,
		actor,
		transfer_service.ApproveLiveCommand{
			TransferID:         transferID,
			AuthorizationID:    authorizationID,
			ExpectedRevision:   revision,
			ExpectedPlanDigest: plan.PlanDigest,
			IdempotencyKey: fmt.Sprintf(
				"telegram:approve-live:%d:%d",
				authorizationID,
				revision,
			),
		},
	)
	if err != nil {
		return presentError(ctx, err)
	}
	markup := handler.bot.NewMarkup()
	markup.Inline(
		markup.Row(markup.Data(
			buttonRevoke.Text,
			buttonRevoke.Unique,
			strconv.FormatInt(int64(transferID), 10),
			strconv.FormatInt(int64(authorization.ID), 10),
			strconv.FormatInt(authorization.Revision, 10),
		)),
		markup.Row(buttonTransfers),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)
	return ctx.EditOrSend(
		fmt.Sprintf(
			"✅ Transfer №%d подтверждён.\n\nAuthorization №%d связан с точным PlanDigest <code>%s</code>. Polling может отправить разрешённые действия в WB; повторное нажатие не создаст вторую попытку.",
			transferID,
			authorization.ID,
			hex.EncodeToString(authorization.PlanDigest[:])[:12],
		),
		markup,
	)
}

func (handler *Handler) revokePlan(ctx tele.Context) error {
	actor, err := trustedActor(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	transferID, authorizationID, revision, err := parseApprove(ctx.Args())
	if err != nil {
		return ctx.RespondAlert("Кнопка устарела. Откройте список планов заново.")
	}
	authorization, err := handler.authorization.Revoke(
		handler.ctx,
		actor,
		transfer_service.RevokeLiveCommand{
			TransferID:       transferID,
			AuthorizationID:  authorizationID,
			ExpectedRevision: revision,
			SafeReasonCode:   "telegram_actor_revoked",
			IdempotencyKey: fmt.Sprintf(
				"telegram:revoke-live:%d:%d",
				authorizationID,
				revision,
			),
		},
	)
	if err != nil {
		return presentError(ctx, err)
	}
	markup := handler.bot.NewMarkup()
	markup.Inline(
		markup.Row(buttonTransfers),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)
	return ctx.EditOrSend(
		fmt.Sprintf(
			"❌ Разрешение №%d для Transfer №%d отозвано. Неначатые WB-действия больше не могут использовать это разрешение.",
			authorization.ID,
			transferID,
		),
		markup,
	)
}

func trustedActor(ctx tele.Context) (transfer_service.LiveTrustedActor, error) {
	sender := ctx.Sender()
	if sender == nil || sender.ID <= 0 {
		return transfer_service.LiveTrustedActor{}, transfer_service.ErrLiveAuthorization
	}
	displayName := strings.TrimSpace(strings.Join(
		[]string{sender.FirstName, sender.LastName},
		" ",
	))
	if displayName == "" {
		displayName = strings.TrimSpace(sender.Username)
	}
	if displayName == "" {
		displayName = fmt.Sprintf("Telegram %d", sender.ID)
	}
	runes := []rune(displayName)
	if len(runes) > 100 {
		displayName = string(runes[:100])
	}
	return transfer_service.LiveTrustedActor{
		TelegramUserID: sender.ID,
		DisplayName:    displayName,
	}, nil
}

func parseTransferID(arguments []string) (transfer_service.TransferID, error) {
	if len(arguments) != 1 {
		return 0, errors.New("invalid transfer callback argument count")
	}
	value, err := strconv.ParseInt(arguments[0], 10, 64)
	if err != nil || value <= 0 {
		return 0, errors.New("invalid transfer callback ID")
	}
	return transfer_service.TransferID(value), nil
}

func parseApprove(arguments []string) (
	transfer_service.TransferID,
	transfer_service.LiveAuthorizationID,
	int64,
	error,
) {
	if len(arguments) != 3 {
		return 0, 0, 0, errors.New("invalid approve callback argument count")
	}
	transferValue, err := strconv.ParseInt(arguments[0], 10, 64)
	if err != nil || transferValue <= 0 {
		return 0, 0, 0, errors.New("invalid approve transfer ID")
	}
	authorizationValue, err := strconv.ParseInt(arguments[1], 10, 64)
	if err != nil || authorizationValue <= 0 {
		return 0, 0, 0, errors.New("invalid authorization ID")
	}
	revision, err := strconv.ParseInt(arguments[2], 10, 64)
	if err != nil || revision < 0 {
		return 0, 0, 0, errors.New("invalid authorization revision")
	}
	return transfer_service.TransferID(transferValue),
		transfer_service.LiveAuthorizationID(authorizationValue),
		revision,
		nil
}

func planText(plan transfer_service.AuthorizationPlanSummary) string {
	return fmt.Sprintf(
		"🚚 <b>Transfer №%d</b>\n\nСоздать группы: %d\nДобавить в группы: %d\nЗагрузить медиа: %d\nУже существуют: %d\nКонфликты: %d",
		plan.TransferID,
		plan.CreateActions,
		plan.AddActions,
		plan.MediaActions,
		plan.ExistingItems,
		plan.ConflictItems,
	)
}

func presentError(ctx tele.Context, err error) error {
	message := "❌ Не удалось выполнить операцию. Попробуйте позже."
	switch {
	case errors.Is(err, transfer_service.ErrLiveModeDisabled):
		message = "⚠️ Live-режим отключён."
	case errors.Is(err, transfer_service.ErrLiveExpired):
		message = "⌛ Разрешение истекло. Откройте план и подтвердите заново."
	case errors.Is(err, transfer_service.ErrLiveIdempotency):
		message = "⚠️ Эта кнопка конфликтует с уже выполненной командой. Откройте план заново."
	case errors.Is(err, transfer_service.ErrLiveAuthorization),
		errors.Is(err, transfer_service.ErrLivePlanUnavailable):
		message = "⚠️ План или разрешение устарели. Откройте список заново."
	}
	if ctx.Callback() != nil {
		if responseErr := ctx.RespondAlert(message); responseErr != nil {
			return fmt.Errorf("respond transfer error: %v: %w", responseErr, err)
		}
		return nil
	}
	return ctx.EditOrSend(html.EscapeString(message))
}

func callbackIdempotencyKey(
	ctx tele.Context,
	kind string,
	identity int64,
) string {
	callbackID := "no-callback"
	if callback := ctx.Callback(); callback != nil && callback.ID != "" {
		callbackID = callback.ID
	}
	digest := sha256.Sum256([]byte(callbackID))
	return fmt.Sprintf(
		"telegram:%s:%d:%s",
		kind,
		identity,
		hex.EncodeToString(digest[:8]),
	)
}
