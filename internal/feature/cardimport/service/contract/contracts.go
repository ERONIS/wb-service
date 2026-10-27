package cardimport_contract

import (
	"context"
	"io"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"
)

type RecoverableFile struct {
	AuthorTelegramID int64
	File             cardimport_model.File
}

type FinalizedSourceFile struct {
	ID        cardimport_model.FileID
	SessionID cardimport_model.SessionID
}

type FinalizedSession struct {
	Session     cardimport_model.Session
	SourceFiles []FinalizedSourceFile
	Items       []cardimport_model.BatchItem
	GroupsCount int
	User        domain.User
	Command     FinalizeCommand
}

type FileParser interface {
	Parse(fileID cardimport_model.FileID, reader io.Reader) (cardimport_model.ParsedFile, error)
}

type Repository interface {
	HasAuthor(ctx context.Context, authorTelegramID int64) (bool, error)
	PersistFinalized(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		finalized FinalizedSession,
	) (cardimport_model.BatchHeader, error)
	GetBatch(ctx context.Context, batchID cardpipeline.BatchID) (cardimport_model.BatchHeader, error)
}
