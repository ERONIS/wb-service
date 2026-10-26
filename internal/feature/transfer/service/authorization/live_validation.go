package authorization

import (
	"errors"
	"strings"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

func (state LiveAuthorizationState) IsValid() bool {
	switch state {
	case LiveAuthorizationRequested,
		LiveAuthorizationAuthorized,
		LiveAuthorizationRevoked,
		LiveAuthorizationExpired,
		LiveAuthorizationSuperseded,
		LiveAuthorizationClosed:
		return true
	default:
		return false
	}
}

func (actor LiveTrustedActor) normalized() LiveTrustedActor {
	actor.DisplayName = strings.TrimSpace(actor.DisplayName)
	return actor
}

func (actor LiveTrustedActor) validate() error {
	if actor.TelegramUserID <= 0 || actor.DisplayName == "" ||
		len([]rune(actor.DisplayName)) > 100 {
		return core_errors.InvalidArgument("trusted live actor is invalid")
	}
	return nil
}

func (summary AuthorizationPlanSummary) Validate() error {
	if summary.TransferID <= 0 || summary.PlanID <= 0 ||
		summary.PlanRevision < 0 || summary.LatestAuthorizationID < 0 ||
		summary.TargetsCount <= 0 || summary.CreateActions < 0 ||
		summary.AddActions < 0 || summary.MediaActions < 0 ||
		summary.CreateItems < 0 || summary.AddItems < 0 ||
		summary.ExistingItems < 0 || summary.ConflictItems < 0 ||
		summary.AuthorTelegramID <= 0 {
		return errors.New("authorization plan summary is invalid")
	}
	return nil
}

func (summary AuthorizationPlanSummary) ActionsCount() int {
	return summary.CreateActions + summary.AddActions + summary.MediaActions
}

func (authorization LiveAuthorization) Validate() error {
	if authorization.ID <= 0 || authorization.TransferID <= 0 ||
		authorization.PlanID <= 0 || authorization.Revision < 0 ||
		!authorization.State.IsValid() || authorization.TrustedActorID <= 0 ||
		strings.TrimSpace(authorization.TrustedActorName) == "" ||
		authorization.RequestedAt.IsZero() || authorization.ExpiresAt.IsZero() ||
		authorization.CreatedAt.IsZero() || authorization.UpdatedAt.IsZero() {
		return ErrLiveAuthorization
	}
	return nil
}

func (command RequestLiveCommand) normalized() RequestLiveCommand {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	return command
}

func (command RequestLiveCommand) validate() error {
	if command.TransferID <= 0 || command.PlanID <= 0 ||
		command.IdempotencyKey == "" ||
		len(command.IdempotencyKey) > MaxLiveIdempotencyKeyLength {
		return core_errors.InvalidArgument("request live command is invalid")
	}
	return nil
}

func (command ApproveLiveCommand) normalized() ApproveLiveCommand {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	return command
}

func (command ApproveLiveCommand) validate() error {
	if command.TransferID <= 0 || command.AuthorizationID <= 0 ||
		command.ExpectedRevision < 0 || command.ExpectedPlanID <= 0 ||
		command.IdempotencyKey == "" ||
		len(command.IdempotencyKey) > MaxLiveIdempotencyKeyLength {
		return core_errors.InvalidArgument("approve live command is invalid")
	}
	return nil
}

func (command ChangeLiveAuthorizationStateCommand) normalized() ChangeLiveAuthorizationStateCommand {
	command.SafeReasonCode = strings.TrimSpace(command.SafeReasonCode)
	return command
}

func (command ChangeLiveAuthorizationStateCommand) validate() error {
	if command.TransferID <= 0 || command.AuthorizationID <= 0 ||
		command.ExpectedRevision < 0 || command.ExpectedPlanID <= 0 ||
		command.SafeReasonCode == "" || len(command.SafeReasonCode) > 128 {
		return core_errors.InvalidArgument("change live authorization state command is invalid")
	}
	return nil
}

func (command RevokeLiveCommand) normalized() RevokeLiveCommand {
	command.SafeReasonCode = strings.TrimSpace(command.SafeReasonCode)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	return command
}

func (command RevokeLiveCommand) validate() error {
	if command.TransferID <= 0 || command.AuthorizationID <= 0 ||
		command.ExpectedRevision < 0 || command.SafeReasonCode == "" ||
		len(command.SafeReasonCode) > 128 || command.IdempotencyKey == "" ||
		len(command.IdempotencyKey) > MaxLiveIdempotencyKeyLength {
		return core_errors.InvalidArgument("revoke live command is invalid")
	}
	return nil
}

func (check LiveAuthorizationCheck) validate() error {
	if check.TransferID <= 0 || check.ActionID <= 0 || check.AuthorizationID <= 0 ||
		check.ExpectedAuthorizationRevision < 0 || check.PlanID <= 0 || check.TargetID <= 0 ||
		check.SellerID.Validate() != nil ||
		check.ClientGeneration.IsZero() {
		return ErrLiveAuthorization
	}
	return nil
}
