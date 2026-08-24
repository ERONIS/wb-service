package transfer_service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

var ErrCapacityExceeded = errors.New("transfer capacity exceeded")

type TransferID int64
type Digest [32]byte
type CabinetID string

type Phase string

const (
	PhaseInitializing          Phase = "initializing"
	PhasePreparing             Phase = "preparing"
	PhaseAwaitingAuthorization Phase = "awaiting_authorization"
	PhasePublishing            Phase = "publishing"
	PhaseReconciling           Phase = "reconciling"
	PhaseMedia                 Phase = "media"
	PhaseFinished              Phase = "finished"
)

func (phase Phase) IsValid() bool {
	switch phase {
	case PhaseInitializing,
		PhasePreparing,
		PhaseAwaitingAuthorization,
		PhasePublishing,
		PhaseReconciling,
		PhaseMedia,
		PhaseFinished:
		return true
	default:
		return false
	}
}

type Outcome string

const (
	OutcomeRunning    Outcome = "running"
	OutcomeSucceeded  Outcome = "succeeded"
	OutcomePartial    Outcome = "partial"
	OutcomeRejected   Outcome = "rejected"
	OutcomeUnresolved Outcome = "unresolved"
	OutcomeFailed     Outcome = "failed"
	OutcomeCancelled  Outcome = "cancelled"
)

func (outcome Outcome) IsValid() bool {
	switch outcome {
	case OutcomeRunning,
		OutcomeSucceeded,
		OutcomePartial,
		OutcomeRejected,
		OutcomeUnresolved,
		OutcomeFailed,
		OutcomeCancelled:
		return true
	default:
		return false
	}
}

type ResultClass string

const (
	ResultSuccess       ResultClass = "success"
	ResultSkipped       ResultClass = "skipped"
	ResultRejected      ResultClass = "rejected"
	ResultPartial       ResultClass = "partial"
	ResultUnresolved    ResultClass = "unresolved"
	ResultInternalError ResultClass = "internal_error"
)

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

type ItemTargetState string

const (
	ItemTargetPending  ItemTargetState = "pending"
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
	case ItemTargetPending:
		if projection.OutcomeClass != "" || projection.OutcomeCode != "" ||
			projection.NMID != 0 || projection.SourceActionID != 0 ||
			projection.AttentionClosedAt != nil || projection.StartedAt != nil ||
			projection.FinishedAt != nil {
			return invalidTransfer("pending item-target has result")
		}
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

type Transfer struct {
	ID                        TransferID
	BatchID                   cardimport_service.BatchID
	Phase                     Phase
	Outcome                   Outcome
	AttentionCode             string
	Revision                  int64
	BatchSchemaVersion        int
	BatchNormalizationVersion int
	BatchChecksum             cardimport_service.Digest
	ItemsCount                int
	GroupsCount               int
	CohortName                string
	TargetSnapshotRevision    Digest
	TargetSetRoot             Digest
	TargetsCount              int
	ItemTargetsCount          int64
	GroupTargetsCount         int64
	CreatedAt                 time.Time
	StartedAt                 time.Time
	FinishedAt                *time.Time
	UpdatedAt                 time.Time
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
	case transfer.Revision < 0:
		return invalidTransfer("revision must not be negative")
	case transfer.BatchSchemaVersion <= 0:
		return invalidTransfer("batch schema version must be positive")
	case transfer.BatchNormalizationVersion <= 0:
		return invalidTransfer("batch normalization version must be positive")
	case transfer.ItemsCount <= 0:
		return invalidTransfer("items count must be positive")
	case transfer.GroupsCount <= 0 || transfer.GroupsCount > transfer.ItemsCount:
		return invalidTransfer("groups count is outside allowed bounds")
	case strings.TrimSpace(transfer.CohortName) != transfer.CohortName ||
		transfer.CohortName == "":
		return invalidTransfer("cohort name is empty or not normalized")
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
	case isZeroDigest(transfer.TargetSnapshotRevision):
		return invalidTransfer("target snapshot revision is empty")
	case isZeroDigest(transfer.TargetSetRoot):
		return invalidTransfer("target set root is empty")
	case transfer.BatchChecksum == (cardimport_service.Digest{}):
		return invalidTransfer("batch checksum is empty")
	default:
		return nil
	}
}

type MutationTargetSnapshot struct {
	CohortName string
	Revision   Digest
	Targets    []MutationTarget
}

type MutationTarget struct {
	Position            int
	CabinetID           CabinetID
	SellerKey           Digest
	ClientGeneration    ClientGeneration
	BindingRevision     int64
	CapabilityRevision  int64
	ContentRead         bool
	ContentWrite        bool
	CredentialExpiresAt time.Time
}

func (snapshot MutationTargetSnapshot) Validate(now time.Time) error {
	if now.IsZero() {
		return invalidTargetSnapshot("validation time is empty")
	}
	if strings.TrimSpace(snapshot.CohortName) != snapshot.CohortName ||
		snapshot.CohortName == "" || len(snapshot.CohortName) > 128 {
		return invalidTargetSnapshot("cohort name is invalid")
	}
	if isZeroDigest(snapshot.Revision) {
		return invalidTargetSnapshot("snapshot revision is empty")
	}
	if len(snapshot.Targets) == 0 {
		return invalidTargetSnapshot("target list is empty")
	}

	cabinets := make(map[CabinetID]struct{}, len(snapshot.Targets))
	sellers := make(map[Digest]struct{}, len(snapshot.Targets))
	for index, target := range snapshot.Targets {
		switch {
		case target.Position != index+1:
			return invalidTargetSnapshot("target positions are not contiguous")
		case strings.TrimSpace(string(target.CabinetID)) != string(target.CabinetID) ||
			target.CabinetID == "" || len(target.CabinetID) > 128:
			return invalidTargetSnapshot("cabinet ID is invalid")
		case isZeroDigest(target.SellerKey):
			return invalidTargetSnapshot("seller key is empty")
		case target.ClientGeneration == (ClientGeneration{}):
			return invalidTargetSnapshot("client generation is empty")
		case target.BindingRevision <= 0:
			return invalidTargetSnapshot("binding revision must be positive")
		case target.CapabilityRevision <= 0:
			return invalidTargetSnapshot("capability revision must be positive")
		case !target.ContentRead || !target.ContentWrite:
			return invalidTargetSnapshot("target lacks required Content capability")
		case target.CredentialExpiresAt.IsZero() ||
			!target.CredentialExpiresAt.After(now):
			return invalidTargetSnapshot("target credential is expired")
		}
		if _, exists := cabinets[target.CabinetID]; exists {
			return invalidTargetSnapshot("cabinet ID is duplicated")
		}
		cabinets[target.CabinetID] = struct{}{}
		if _, exists := sellers[target.SellerKey]; exists {
			return invalidTargetSnapshot("seller key is duplicated")
		}
		sellers[target.SellerKey] = struct{}{}
	}

	return nil
}

func isZeroDigest(digest Digest) bool {
	return digest == Digest{}
}

func invalidTransfer(message string) error {
	return fmt.Errorf("invalid transfer: %s", message)
}

func invalidTargetSnapshot(message string) error {
	return fmt.Errorf("invalid mutation target snapshot: %s", message)
}
