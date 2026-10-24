package cardpublication_service

import (
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/resolution"
)

const (
	MaxManualResolutionIdempotencyKeyLength = resolution.MaxManualResolutionIdempotencyKeyLength
	ManualMarkRemotePresent                 = resolution.ManualMarkRemotePresent
	ManualMarkRejected                      = resolution.ManualMarkRejected
	ManualCloseUnresolvedNoRetry            = resolution.ManualCloseUnresolvedNoRetry
)

var ErrManualResolutionConflict = resolution.ErrManualResolutionConflict
var ErrManualEvidenceInvalid = resolution.ErrManualEvidenceInvalid

func NewManualResolver(
	repository resolution.ManualResolutionRepository,
	transfer resolution.ManualTransferResultCorrector,
	uow core_postgres_transaction.UnitOfWork,
) *resolution.ManualResolver {
	return resolution.NewManualResolver(repository, transfer, uow)
}

func NewVerifiedCardResolver(
	repository resolution.VerifiedCardRepository,
	transfer resolution.ManualTransferResultCorrector,
	uow core_postgres_transaction.UnitOfWork,
) *resolution.VerifiedCardResolver {
	return resolution.NewVerifiedCardResolver(repository, transfer, uow)
}
