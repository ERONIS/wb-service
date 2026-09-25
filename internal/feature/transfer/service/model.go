package transfer_service

import "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"

type TransferID = model.TransferID
type Digest = model.Digest
type CabinetID = model.CabinetID
type SellerKey = model.SellerKey
type ClientGeneration = model.ClientGeneration
type Phase = model.Phase
type Outcome = model.Outcome
type ResultClass = model.ResultClass
type ItemTargetState = model.ItemTargetState
type ItemTargetProjection = model.ItemTargetProjection
type Transfer = model.Transfer
type MutationTargetSnapshot = model.MutationTargetSnapshot
type MutationTarget = model.MutationTarget
type CapacityPolicy = model.CapacityPolicy
type Capacity = model.Capacity

const (
	PhaseInitializing          = model.PhaseInitializing
	PhasePreparing             = model.PhasePreparing
	PhaseAwaitingAuthorization = model.PhaseAwaitingAuthorization
	PhasePublishing            = model.PhasePublishing
	PhaseReconciling           = model.PhaseReconciling
	PhaseMedia                 = model.PhaseMedia
	PhaseFinished              = model.PhaseFinished

	OutcomeRunning    = model.OutcomeRunning
	OutcomeSucceeded  = model.OutcomeSucceeded
	OutcomePartial    = model.OutcomePartial
	OutcomeRejected   = model.OutcomeRejected
	OutcomeUnresolved = model.OutcomeUnresolved
	OutcomeFailed     = model.OutcomeFailed
	OutcomeCancelled  = model.OutcomeCancelled

	ResultSuccess       = model.ResultSuccess
	ResultSkipped       = model.ResultSkipped
	ResultRejected      = model.ResultRejected
	ResultPartial       = model.ResultPartial
	ResultUnresolved    = model.ResultUnresolved
	ResultInternalError = model.ResultInternalError

	ItemTargetPending  = model.ItemTargetPending
	ItemTargetRunning  = model.ItemTargetRunning
	ItemTargetTerminal = model.ItemTargetTerminal
)

var ErrCapacityExceeded = model.ErrCapacityExceeded

func TargetSetRoot(snapshot MutationTargetSnapshot) Digest {
	return model.TargetSetRoot(snapshot)
}
