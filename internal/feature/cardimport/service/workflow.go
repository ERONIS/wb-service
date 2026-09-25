package cardimport_service

import (
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"github.com/ERONIS/wb-service/internal/feature/cardimport/service/workflow"
)

type StoredContent = workflow.StoredContent
type RecoverableFile = workflow.RecoverableFile
type FileParser = workflow.FileParser
type Repository = workflow.Repository
type BatchReader = workflow.BatchReader
type BeginCommand = workflow.BeginCommand
type SessionCommand = workflow.SessionCommand
type ContinueCommand = workflow.ContinueCommand
type ReserveFileCommand = workflow.ReserveFileCommand
type StoreFileCommand = workflow.StoreFileCommand
type ParseFileCommand = workflow.ParseFileCommand
type FinalizeCommand = workflow.FinalizeCommand
type BatchSourceKind = workflow.BatchSourceKind
type CreateExternalBatchCommand = workflow.CreateExternalBatchCommand
type ExternalBatchDraft = workflow.ExternalBatchDraft
type Service = workflow.Service

const (
	MaxFinalizeIdempotencyKeyLength = workflow.MaxFinalizeIdempotencyKeyLength
	BatchSourceWBCabinet            = workflow.BatchSourceWBCabinet
)

func New(
	repository Repository,
	parser FileParser,
	uow core_postgres_transaction.UnitOfWork,
) *Service {
	return workflow.New(repository, parser, uow)
}
