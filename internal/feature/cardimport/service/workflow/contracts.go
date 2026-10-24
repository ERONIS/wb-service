package workflow

import (
	"context"
	"io"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type StoredContent struct {
	Bytes  []byte
	SHA256 [32]byte
}

type RecoverableFile struct {
	AuthorTelegramID int64
	File             File
}

type FinalizedSession struct {
	Session       Session
	Files         []File
	Items         []BatchItem
	GroupsCount   int
	Checksum      Digest
	User          domain.User
	Command       FinalizeCommand
	CommandDigest Digest
}

type FileParser interface {
	Parse(fileID FileID, reader io.Reader) (ParsedFile, error)
}

type Repository interface {
	HasAuthor(ctx context.Context, authorTelegramID int64) (bool, error)
	PersistFinalized(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		finalized FinalizedSession,
	) (BatchHeader, error)
	BatchReader
}

type BatchReader interface {
	ListFinalizedBatches(ctx context.Context, after *BatchCursor, limit int) ([]BatchHeader, error)
	GetBatch(ctx context.Context, batchID BatchID) (BatchHeader, error)
	ListBatchItems(ctx context.Context, batchID BatchID, afterPosition int, limit int) ([]BatchItem, error)
}
