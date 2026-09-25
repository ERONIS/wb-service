package domain

import (
	"fmt"
	"strings"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

const MaxActorDisplayNameLength = 100

// Actor is an authenticated human identity captured at a domain boundary.
// It intentionally does not depend on a Telegram transport type.
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
		return fmt.Errorf(
			"invalid actor Telegram ID: %w",
			core_errors.ErrInvalidArgument,
		)
	case actor.DisplayName == "":
		return fmt.Errorf(
			"actor display name is empty: %w",
			core_errors.ErrInvalidArgument,
		)
	case len([]rune(actor.DisplayName)) > MaxActorDisplayNameLength:
		return fmt.Errorf(
			"actor display name is too long: %w",
			core_errors.ErrInvalidArgument,
		)
	default:
		return nil
	}
}
