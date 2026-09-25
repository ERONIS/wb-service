package transfer_service

import (
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/workflow"
	"go.uber.org/zap"
)

type BatchHeaderReader = workflow.BatchHeaderReader
type BatchReader = workflow.BatchReader
type TargetRegistry = workflow.TargetRegistry
type CreateTransfer = workflow.CreateTransfer
type Repository = workflow.Repository
type InitializationGroup = workflow.InitializationGroup
type InitializationItem = workflow.InitializationItem
type InitializeTransferCommand = workflow.InitializeTransferCommand
type Service = workflow.Service

const MaxPreparingPageSize = workflow.MaxPreparingPageSize

var ErrInitializationInvalid = workflow.ErrInitializationInvalid
var ErrInitializationConflict = workflow.ErrInitializationConflict

func New(
	repository Repository,
	batchReader BatchReader,
	targetRegistry TargetRegistry,
	uow core_postgres_transaction.UnitOfWork,
	capacity CapacityPolicy,
	loggers ...*zap.Logger,
) *Service {
	return workflow.New(repository, batchReader, targetRegistry, uow, capacity, loggers...)
}
