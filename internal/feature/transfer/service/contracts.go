package transfer_service

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

type BatchHeaderReader interface {
	GetBatch(
		ctx context.Context,
		batchID cardimport_service.BatchID,
	) (cardimport_service.BatchHeader, error)
}

type BatchReader interface {
	BatchHeaderReader

	ListFinalizedBatches(
		ctx context.Context,
		after *cardimport_service.BatchCursor,
		limit int,
	) ([]cardimport_service.BatchHeader, error)

	ListBatchItems(
		ctx context.Context,
		batchID cardimport_service.BatchID,
		afterPosition int,
		limit int,
	) ([]cardimport_service.BatchItem, error)
}

type TargetRegistry interface {
	MutationSnapshot(ctx context.Context) (MutationTargetSnapshot, error)
}

// TargetTransport exposes the WB Core verified identity snapshot. The
// transfer feature only applies its own read/write cohort requirements.
type TargetTransport interface {
	Credentials() ([]TargetCredential, error)
}

type CreateTransfer struct {
	Batch         cardimport_service.BatchHeader
	Snapshot      MutationTargetSnapshot
	TargetSetRoot Digest
	Capacity      Capacity
}

type Repository interface {
	FindByID(
		ctx context.Context,
		transferID TransferID,
	) (Transfer, error)

	FindByBatch(
		ctx context.Context,
		batchID cardimport_service.BatchID,
	) (Transfer, error)

	Create(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command CreateTransfer,
	) (transfer Transfer, created bool, err error)

	ListInitializing(
		ctx context.Context,
		afterID TransferID,
		limit int,
	) ([]Transfer, error)

	ListPreparing(
		ctx context.Context,
		afterID TransferID,
		limit int,
	) ([]Transfer, error)

	ListAwaitingAuthorization(
		ctx context.Context,
		afterID TransferID,
		limit int,
	) ([]Transfer, error)

	Initialize(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command InitializeTransferCommand,
	) error

	MarkInitializationFailed(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		transferID TransferID,
		failureCode string,
	) error

	BeginPreparation(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		transferID TransferID,
	) error

	LoadPreparationSource(
		ctx context.Context,
		transferID TransferID,
		groupTargetID int64,
	) (PreparationSource, error)

	LoadPublicationPlanningSource(
		ctx context.Context,
		transferID TransferID,
	) (PublicationPlanningSource, error)
}

type PreparationResultRepository interface {
	ApplyPreparationResult(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command ApplyPreparationResultCommand,
	) error
}

type PublicationPlanResultRepository interface {
	ApplyPublicationPlanResult(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command ApplyPublicationPlanResultCommand,
	) error
}
