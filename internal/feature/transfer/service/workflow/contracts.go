package workflow

import (
	"context"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
	transfer_publication "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
)

type BatchHeaderReader interface {
	GetReadyBatchHeader(
		ctx context.Context,
		batchID cardpipeline.BatchID,
	) (cardpipeline.ReadyBatchHeader, error)
}

type BatchReader interface {
	BatchHeaderReader

	ListReadyBatches(
		ctx context.Context,
		after *cardpipeline.BatchCursor,
		limit int,
	) ([]cardpipeline.ReadyBatchHeader, error)

	GetReadyBatch(
		ctx context.Context,
		batchID cardpipeline.BatchID,
	) (cardpipeline.ReadyBatch, error)
}

type TargetRegistry interface {
	MutationSnapshot(ctx context.Context, ownerTelegramID int64) (transfer_model.MutationTargetSnapshot, error)
	FanoutMutationSnapshot(context.Context) (transfer_model.MutationTargetSnapshot, error)
	MutationSnapshotForOwner(
		ctx context.Context,
		ownerTelegramID int64,
		sellerIDs []core_domain.SellerID,
	) (transfer_model.MutationTargetSnapshot, error)
	AllTargets(context.Context) ([]transfer_model.MutationTarget, error)
}

type CreateTransfer struct {
	Batch    cardpipeline.ReadyBatchHeader
	Snapshot transfer_model.MutationTargetSnapshot
	Capacity transfer_model.Capacity
}

type Repository interface {
	FindByID(
		ctx context.Context,
		transferID transfer_model.TransferID,
	) (transfer_model.Transfer, error)

	FindByBatch(
		ctx context.Context,
		batchID cardpipeline.BatchID,
	) (transfer_model.Transfer, error)

	ListTargetSellerIDs(
		ctx context.Context,
		transferID transfer_model.TransferID,
	) ([]core_domain.SellerID, error)

	ListTargetSellerIDsWithin(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		transferID transfer_model.TransferID,
	) ([]core_domain.SellerID, error)

	Create(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command CreateTransfer,
	) (transfer transfer_model.Transfer, created bool, err error)

	ListInitializing(
		ctx context.Context,
		afterID transfer_model.TransferID,
		limit int,
	) ([]transfer_model.Transfer, error)

	ListPreparing(
		ctx context.Context,
		afterID transfer_model.TransferID,
		limit int,
	) ([]transfer_model.Transfer, error)

	ListAwaitingAuthorization(
		ctx context.Context,
		afterID transfer_model.TransferID,
		limit int,
	) ([]transfer_model.Transfer, error)

	Initialize(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command InitializeTransferCommand,
	) error

	MarkInitializationFailed(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		transferID transfer_model.TransferID,
		failureCode string,
	) error

	BeginPreparation(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		transferID transfer_model.TransferID,
	) error

	ListPendingPreparationGroups(
		ctx context.Context,
		transferID transfer_model.TransferID,
		limit int,
		after transfer_preparation.PreparationPendingGroup,
	) ([]transfer_preparation.PreparationPendingGroup, error)

	LoadPreparationSource(
		ctx context.Context,
		transferID transfer_model.TransferID,
		groupTargetID int64,
	) (transfer_preparation.PreparationSource, error)

	LoadPublicationPlanningSource(
		ctx context.Context,
		transferID transfer_model.TransferID,
	) (transfer_publication.PublicationPlanningSource, error)
}
