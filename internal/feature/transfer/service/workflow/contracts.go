package workflow

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
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
	FanoutTargets(context.Context) ([]transfer_model.MutationTarget, error)
}

type CreateTransfer struct {
	Batch    cardpipeline.ReadyBatchHeader
	Targets  []transfer_model.MutationTarget
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

	Create(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command CreateTransfer,
	) (transfer_model.Transfer, error)

	ListInitializing(
		ctx context.Context,
		afterID transfer_model.TransferID,
		limit int,
	) ([]transfer_model.Transfer, error)

	Initialize(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command InitializeTransferCommand,
	) (bool, error)

	MarkInitializationFailed(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		transferID transfer_model.TransferID,
		failureCode string,
	) error
}
