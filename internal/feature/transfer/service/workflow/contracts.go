package workflow

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type BatchHeaderReader interface {
	GetBatch(
		ctx context.Context,
		batchID cardpipeline.BatchID,
	) (cardpipeline.BatchHeader, error)
}

type BatchReader interface {
	BatchHeaderReader

	ListFinalizedBatches(
		ctx context.Context,
		after *cardpipeline.BatchCursor,
		limit int,
	) ([]cardpipeline.BatchHeader, error)

	ListBatchItems(
		ctx context.Context,
		batchID cardpipeline.BatchID,
		afterPosition int,
		limit int,
	) ([]cardpipeline.BatchItem, error)
}

type TargetRegistry interface {
	MutationSnapshot(ctx context.Context, ownerTelegramID int64) (MutationTargetSnapshot, error)
	FanoutMutationSnapshot(context.Context) (MutationTargetSnapshot, error)
	MutationSnapshotForOwner(
		ctx context.Context,
		ownerTelegramID int64,
		cabinetIDs []CabinetID,
	) (MutationTargetSnapshot, error)
	AllTargets(context.Context) ([]MutationTarget, error)
}

type CreateTransfer struct {
	Batch         cardpipeline.BatchHeader
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
		batchID cardpipeline.BatchID,
	) (Transfer, error)

	ListTargetCabinetIDs(
		ctx context.Context,
		transferID TransferID,
	) ([]CabinetID, error)

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
