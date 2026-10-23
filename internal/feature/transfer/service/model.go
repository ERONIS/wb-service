package transfer_service

import "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"

type TransferID = model.TransferID
type Digest = model.Digest
type SellerID = model.SellerID
type Phase = model.Phase
type ResultClass = model.ResultClass
type Transfer = model.Transfer
type MutationTarget = model.MutationTarget
type CapacityPolicy = model.CapacityPolicy

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
