package cardpublication_service

import (
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/resolution"
)

type ManualResolutionKind = resolution.ManualResolutionKind
type ManualTrustedActor = resolution.ManualTrustedActor
type ManualResolutionCommand = resolution.ManualResolutionCommand
type ManualResolution = resolution.ManualResolution
type ManualResolutionSubject = resolution.ManualResolutionSubject
type ManualObservationEvidence = resolution.ManualObservationEvidence
type ManualErrorEvidence = resolution.ManualErrorEvidence
type ManualResolutionDecision = resolution.ManualResolutionDecision
type ApplyManualResolutionCommand = resolution.ApplyManualResolutionCommand
type ManualResolutionRepository = resolution.ManualResolutionRepository
type ManualTransferResultCorrector = resolution.ManualTransferResultCorrector
type ManualResolver = resolution.ManualResolver

const (
	MaxManualResolutionIdempotencyKeyLength = resolution.MaxManualResolutionIdempotencyKeyLength
	ManualMarkRemotePresent                 = resolution.ManualMarkRemotePresent
	ManualMarkRejected                      = resolution.ManualMarkRejected
	ManualCloseUnresolvedNoRetry            = resolution.ManualCloseUnresolvedNoRetry
)

var ErrManualResolutionConflict = resolution.ErrManualResolutionConflict
var ErrManualEvidenceInvalid = resolution.ErrManualEvidenceInvalid

func NewManualResolver(
	repository ManualResolutionRepository,
	transfer ManualTransferResultCorrector,
	uow core_postgres_transaction.UnitOfWork,
) *ManualResolver {
	return resolution.NewManualResolver(repository, transfer, uow)
}
