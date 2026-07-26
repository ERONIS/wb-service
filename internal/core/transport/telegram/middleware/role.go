package core_tg_middleware

import (
	"context"
	"errors"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	tele "gopkg.in/telebot.v3"
)

type RoleProvider interface {
	// GetRole возвращает роль пользователя по его ID.
	GetRole(
		ctx context.Context,
		telegramID int64,
	) (domain.UserRole, error)
}

type RoleAccess struct {
	appCtx       context.Context
	roleProvider RoleProvider
}

func NewRoleAccess(
	appCtx context.Context,
	roleProvider RoleProvider,
) *RoleAccess {
	if appCtx == nil {
		panic("telegram role access context is nil")
	}
	if roleProvider == nil {
		panic("telegram role provider is nil")
	}

	return &RoleAccess{
		appCtx:       appCtx,
		roleProvider: roleProvider,
	}
}
func (a *RoleAccess) GetRole(
	ctx tele.Context,
) (domain.UserRole, bool, error) {
	telegramID, err := telegramSenderID(ctx)
	if errors.Is(err, errTelegramSenderUnavailable) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	return a.getRole(telegramID)
}

var errTelegramSenderUnavailable = errors.New(
	"Telegram sender is unavailable",
)

func telegramSenderID(ctx tele.Context) (int64, error) {
	if ctx == nil {
		return 0, fmt.Errorf("telegram context is nil")
	}

	sender := ctx.Sender()
	if sender == nil || sender.ID <= 0 {
		return 0, errTelegramSenderUnavailable
	}

	return sender.ID, nil
}

func (a *RoleAccess) getRole(
	telegramID int64,
) (domain.UserRole, bool, error) {
	role, err := a.roleProvider.GetRole(
		a.appCtx,
		telegramID,
	)
	if err != nil {
		if errors.Is(err, core_errors.ErrNotFound) {
			return "", false, nil
		}

		return "", false, fmt.Errorf(
			"get role for TelegramID='%d': %w",
			telegramID,
			err,
		)
	}

	if !role.IsValid() {
		return "", false, fmt.Errorf(
			"invalid role '%s' for TelegramID='%d'",
			role,
			telegramID,
		)
	}

	return role, true, nil
}

func HasMinimumRole(
	actualRole domain.UserRole,
	minimumRole domain.UserRole,
) bool {
	switch minimumRole {

	case domain.RoleUser:
		return actualRole == domain.RoleUser ||
			actualRole == domain.RoleAdmin
	case domain.RoleAdmin:
		return actualRole == domain.RoleAdmin

	default:
		return false
	}

}

func (a *RoleAccess) Require(
	minimumRole domain.UserRole,
) tele.MiddlewareFunc {
	if !minimumRole.IsValid() {
		panic(fmt.Sprintf(
			"invalid minimum Telegram role '%s'",
			minimumRole,
		))
	}

	return func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(ctx tele.Context) error {
			telegramID, err := telegramSenderID(ctx)
			if err != nil {
				responseErr := respondRoleMessage(
					ctx,
					"Не удалось определить Telegram-пользователя.",
				)
				if responseErr != nil {
					return fmt.Errorf(
						"respond to missing Telegram sender: %v: %w",
						responseErr,
						err,
					)
				}

				return nil
			}

			actualRole, found, err := a.getRole(telegramID)
			if err != nil {
				responseErr := respondRoleMessage(
					ctx,
					"❌ Не удалось проверить права. Попробуйте позже.",
				)
				if responseErr != nil {
					return fmt.Errorf(
						"respond to role check error: %v: %w",
						responseErr,
						err,
					)
				}

				return fmt.Errorf(
					"check Telegram user role: %w",
					err,
				)
			}

			if !found ||
				!HasMinimumRole(actualRole, minimumRole) {
				return respondRoleMessage(
					ctx,
					"⛔ Недостаточно прав.",
				)
			}

			return next(ctx)
		}
	}
}

func respondRoleMessage(
	ctx tele.Context,
	message string,
) error {
	if ctx == nil {
		return fmt.Errorf("telegram context is nil")
	}

	if ctx.Callback() != nil {
		return ctx.RespondAlert(message)
	}

	return ctx.EditOrSend(message)
}
