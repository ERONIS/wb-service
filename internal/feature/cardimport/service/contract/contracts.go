package cardimport_contract

import (
	"context"
	"io"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_batch "github.com/ERONIS/wb-service/internal/feature/cardimport/service/batch"
	cardimport_intake "github.com/ERONIS/wb-service/internal/feature/cardimport/service/intake"
)

type RecoverableFile struct {
	AuthorTelegramID int64
	File             cardimport_intake.File
}

type FinalizedSession struct {
	Session     cardimport_intake.Session
	Files       []cardimport_intake.File
	Items       []cardimport_batch.BatchItem
	GroupsCount int
	User        domain.User
	Command     FinalizeCommand
}

type FileParser interface {
	Parse(fileID cardimport_intake.FileID, reader io.Reader) (cardimport_intake.ParsedFile, error)
}

type Repository interface {
	HasAuthor(ctx context.Context, authorTelegramID int64) (bool, error)
	PersistFinalized(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		finalized FinalizedSession,
	) (cardimport_batch.BatchHeader, error)
	BatchReader
}

type BatchReader interface {
	ListFinalizedBatches(ctx context.Context, after *cardpipeline.BatchCursor, limit int) ([]cardimport_batch.BatchHeader, error)
	GetBatch(ctx context.Context, batchID cardpipeline.BatchID) (cardimport_batch.BatchHeader, error)
	ListBatchItems(ctx context.Context, batchID cardpipeline.BatchID, afterPosition int, limit int) ([]cardimport_batch.BatchItem, error)
}
