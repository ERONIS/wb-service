package transfer_service

import (
	"context"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
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

// TargetTransport exposes one raw WB operation per method. The service owns
// cohort validation and the multi-cabinet verification sequence.
type TargetTransport interface {
	Credentials(now time.Time) ([]TargetCredential, error)
	ProbeCardsList(
		ctx context.Context,
		cabinetID CabinetID,
		generation ClientGeneration,
	) (contentapi.CardsListResponse, error)
}

type TargetBindingStore interface {
	SyncTargetBindings(
		ctx context.Context,
		verifiedAt time.Time,
		targets []VerifiedTarget,
	) ([]TargetBinding, error)
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
}

type PreparationResultRepository interface {
	ApplyPreparationResult(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command ApplyPreparationResultCommand,
	) error
}
