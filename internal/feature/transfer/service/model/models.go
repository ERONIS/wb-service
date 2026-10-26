package model

import (
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

var ErrCapacityExceeded = core_errors.CapacityExceeded("transfer capacity exceeded")

type TransferID int64

type Phase string

const (
	PhaseInitializing Phase = "initializing"
	PhaseActive       Phase = "active"
	PhaseFinished     Phase = "finished"
)

type Outcome string

const (
	OutcomeRunning    Outcome = "running"
	OutcomeSucceeded  Outcome = "succeeded"
	OutcomePartial    Outcome = "partial"
	OutcomeRejected   Outcome = "rejected"
	OutcomeUnresolved Outcome = "unresolved"
	OutcomeFailed     Outcome = "failed"
)

type ResultClass string

const (
	ResultSuccess       ResultClass = "success"
	ResultSkipped       ResultClass = "skipped"
	ResultRejected      ResultClass = "rejected"
	ResultPartial       ResultClass = "partial"
	ResultUnresolved    ResultClass = "unresolved"
	ResultInternalError ResultClass = "internal_error"
)

type ItemTargetState string

const (
	ItemTargetRunning  ItemTargetState = "running"
	ItemTargetTerminal ItemTargetState = "terminal"
)

type ItemTargetProjection struct {
	ID                int64
	TransferID        TransferID
	GroupTargetID     int64
	TransferItemID    int64
	Revision          int64
	State             ItemTargetState
	OutcomeClass      ResultClass
	OutcomeCode       string
	NMID              int64
	SourceActionID    int64
	AttentionClosedAt *time.Time
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	UpdatedAt         time.Time
}

type Transfer struct {
	ID                TransferID
	BatchID           cardpipeline.BatchID
	Phase             Phase
	Outcome           Outcome
	AttentionCode     string
	ItemsCount        int
	GroupsCount       int
	TargetsCount      int
	ItemTargetsCount  int64
	GroupTargetsCount int64
	CreatedAt         time.Time
	StartedAt         time.Time
	FinishedAt        *time.Time
	UpdatedAt         time.Time
}

type MutationTarget struct {
	Position            int
	SellerID            domain.SellerID
	ClientGeneration    domain.ClientGeneration
	BindingRevision     int64
	CapabilityRevision  int64
	ContentRead         bool
	ContentWrite        bool
	CredentialExpiresAt time.Time
}

func (phase Phase) IsValid() bool {
	switch phase {
	case PhaseInitializing,
		PhaseActive,
		PhaseFinished:
		return true
	default:
		return false
	}
}

// AllowsGroupWork сообщает, можно ли продолжать подготовку и планирование отдельных групп.
func (phase Phase) AllowsGroupWork() bool {
	return phase == PhaseActive
}

func (outcome Outcome) IsValid() bool {
	switch outcome {
	case OutcomeRunning,
		OutcomeSucceeded,
		OutcomePartial,
		OutcomeRejected,
		OutcomeUnresolved,
		OutcomeFailed:
		return true
	default:
		return false
	}
}

func (result ResultClass) IsValid() bool {
	switch result {
	case ResultSuccess,
		ResultSkipped,
		ResultRejected,
		ResultPartial,
		ResultUnresolved,
		ResultInternalError:
		return true
	default:
		return false
	}
}

func (projection ItemTargetProjection) Validate() error {
	if projection.ID <= 0 || projection.TransferID <= 0 ||
		projection.GroupTargetID <= 0 || projection.TransferItemID <= 0 ||
		projection.Revision < 0 || projection.CreatedAt.IsZero() ||
		projection.UpdatedAt.IsZero() ||
		projection.UpdatedAt.Before(projection.CreatedAt) ||
		strings.TrimSpace(projection.OutcomeCode) != projection.OutcomeCode ||
		len(projection.OutcomeCode) > 128 || projection.NMID < 0 ||
		projection.SourceActionID < 0 {
		return invalidTransfer("item-target projection is invalid")
	}
	switch projection.State {
	case ItemTargetRunning:
		if projection.OutcomeClass != "" || projection.OutcomeCode != "" ||
			projection.AttentionClosedAt != nil || projection.StartedAt == nil ||
			projection.FinishedAt != nil {
			return invalidTransfer("running item-target has invalid result")
		}
	case ItemTargetTerminal:
		if !projection.OutcomeClass.IsValid() || projection.OutcomeCode == "" ||
			projection.StartedAt == nil || projection.FinishedAt == nil {
			return invalidTransfer("terminal item-target has incomplete result")
		}
		if projection.AttentionClosedAt != nil &&
			projection.OutcomeClass != ResultUnresolved &&
			projection.OutcomeClass != ResultInternalError {
			return invalidTransfer("resolved item-target has closed attention")
		}
	default:
		return invalidTransfer("item-target state is invalid")
	}
	if projection.StartedAt != nil &&
		projection.StartedAt.Before(projection.CreatedAt) {
		return invalidTransfer("item-target started before creation")
	}
	if projection.FinishedAt != nil && projection.StartedAt != nil &&
		projection.FinishedAt.Before(*projection.StartedAt) {
		return invalidTransfer("item-target finished before start")
	}
	if projection.AttentionClosedAt != nil && projection.FinishedAt != nil &&
		projection.AttentionClosedAt.Before(*projection.FinishedAt) {
		return invalidTransfer("item-target attention closed before finish")
	}
	return nil
}

func (transfer Transfer) Validate() error {
	expectedItemTargets, itemTargetsOK := checkedMultiply(
		int64(transfer.ItemsCount),
		int64(transfer.TargetsCount),
	)
	expectedGroupTargets, groupTargetsOK := checkedMultiply(
		int64(transfer.GroupsCount),
		int64(transfer.TargetsCount),
	)

	switch {
	case transfer.ID <= 0:
		return invalidTransfer("transfer ID must be positive")
	case transfer.BatchID <= 0:
		return invalidTransfer("batch ID must be positive")
	case !transfer.Phase.IsValid():
		return invalidTransfer("unsupported transfer phase")
	case !transfer.Outcome.IsValid():
		return invalidTransfer("unsupported transfer outcome")
	case strings.TrimSpace(transfer.AttentionCode) != transfer.AttentionCode ||
		len(transfer.AttentionCode) > 128:
		return invalidTransfer("attention code is invalid")
	case transfer.ItemsCount <= 0:
		return invalidTransfer("items count must be positive")
	case transfer.GroupsCount <= 0 || transfer.GroupsCount > transfer.ItemsCount:
		return invalidTransfer("groups count is outside allowed bounds")
	case transfer.TargetsCount <= 0:
		return invalidTransfer("targets count must be positive")
	case transfer.ItemTargetsCount <= 0 || transfer.GroupTargetsCount <= 0:
		return invalidTransfer("matrix counts must be positive")
	case !itemTargetsOK || transfer.ItemTargetsCount != expectedItemTargets:
		return invalidTransfer("item-target count differs from frozen dimensions")
	case !groupTargetsOK || transfer.GroupTargetsCount != expectedGroupTargets:
		return invalidTransfer("group-target count differs from frozen dimensions")
	case transfer.Phase == PhaseFinished && transfer.Outcome == OutcomeRunning:
		return invalidTransfer("finished transfer has running outcome")
	case transfer.Phase != PhaseFinished && transfer.Outcome != OutcomeRunning:
		return invalidTransfer("active transfer has terminal outcome")
	case transfer.Outcome == OutcomeRunning && transfer.AttentionCode != "":
		return invalidTransfer("running transfer requires no operator attention")
	case transfer.Outcome == OutcomeFailed &&
		transfer.AttentionCode == "":
		return invalidTransfer("attention outcome has no attention code")
	case transfer.Phase == PhaseFinished && transfer.FinishedAt == nil:
		return invalidTransfer("finished transfer has no finished time")
	case transfer.Phase != PhaseFinished && transfer.FinishedAt != nil:
		return invalidTransfer("active transfer has finished time")
	case transfer.CreatedAt.IsZero() || transfer.StartedAt.IsZero() ||
		transfer.UpdatedAt.IsZero():
		return invalidTransfer("timestamps are empty")
	case transfer.StartedAt.Before(transfer.CreatedAt):
		return invalidTransfer("started time precedes created time")
	case transfer.FinishedAt != nil && transfer.FinishedAt.Before(transfer.StartedAt):
		return invalidTransfer("finished time precedes started time")
	case transfer.UpdatedAt.Before(transfer.CreatedAt):
		return invalidTransfer("updated time precedes created time")
	default:
		return nil
	}
}

func ValidateMutationTargets(targets []MutationTarget, now time.Time) error {
	if now.IsZero() {
		return invalidMutationTargets("validation time is empty")
	}
	if len(targets) == 0 {
		return invalidMutationTargets("target list is empty")
	}

	sellers := make(map[domain.SellerID]struct{}, len(targets))
	for index, target := range targets {
		switch {
		case target.Position != index+1:
			return invalidMutationTargets("target positions are not contiguous")
		case target.SellerID.Validate() != nil:
			return invalidMutationTargets("seller ID is invalid")
		case target.ClientGeneration.IsZero():
			return invalidMutationTargets("client generation is empty")
		case target.BindingRevision <= 0:
			return invalidMutationTargets("binding revision must be positive")
		case target.CapabilityRevision <= 0:
			return invalidMutationTargets("capability revision must be positive")
		case !target.ContentRead || !target.ContentWrite:
			return invalidMutationTargets("target lacks required Content capability")
		case target.CredentialExpiresAt.IsZero() ||
			!target.CredentialExpiresAt.After(now):
			return invalidMutationTargets("target credential is expired")
		}
		if _, exists := sellers[target.SellerID]; exists {
			return invalidMutationTargets("seller ID is duplicated")
		}
		sellers[target.SellerID] = struct{}{}
	}

	return nil
}

func invalidTransfer(message string) error {
	return core_errors.InvalidArgument("invalid transfer: %s", message)
}

func invalidMutationTargets(message string) error {
	return core_errors.InvalidArgument("invalid mutation targets: %s", message)
}
