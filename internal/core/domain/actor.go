package domain

import (
	"strings"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

const MaxActorDisplayNameLength = 100

type Actor struct {
	TelegramUserID int64
	DisplayName    string
}

func (actor Actor) Normalized() Actor {
	actor.DisplayName = strings.TrimSpace(actor.DisplayName)
	return actor
}

func (actor Actor) Validate() error {
	switch {
	case actor.TelegramUserID <= 0:
		return core_errors.InvalidArgument("invalid actor Telegram ID")
	case actor.DisplayName == "":
		return core_errors.InvalidArgument("actor display name is empty")
	case len([]rune(actor.DisplayName)) > MaxActorDisplayNameLength:
		return core_errors.InvalidArgument("actor display name is too long")
	default:
		return nil
	}
}
