package workflow

import (
	"context"
	"io"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type StoredContent struct {
	Bytes  []byte
	SHA256 [32]byte
}

// RecoverableFile is an unfinished file together with the owner required to
// resume its durable workflow after a Telegram timeout or process restart.
type RecoverableFile struct {
	AuthorTelegramID int64
	File             File
}

type FileParser interface {
	Parse(fileID FileID, reader io.Reader) (ParsedFile, error)
}

type Repository interface {
	GetOrCreateCollectingSession(
		ctx context.Context,
		authorTelegramID int64,
		purpose Purpose,
	) (Session, error)

	GetSession(
		ctx context.Context,
		authorTelegramID int64,
		sessionID SessionID,
	) (Session, error)

	GetCollectingSessionView(
		ctx context.Context,
		authorTelegramID int64,
	) (SessionView, error)

	GetSessionView(
		ctx context.Context,
		authorTelegramID int64,
		sessionID SessionID,
	) (SessionView, error)

	CancelSession(
		ctx context.Context,
		authorTelegramID int64,
		sessionID SessionID,
	) (Session, error)

	ContinueAfterErrors(
		ctx context.Context,
		command ContinueCommand,
	) (Session, error)

	ReserveFile(
		ctx context.Context,
		command ReserveFileCommand,
		maxFiles int,
	) (File, error)

	StoreFile(
		ctx context.Context,
		command StoreFileCommand,
		content StoredContent,
	) (File, error)

	ListRecoverableFiles(
		ctx context.Context,
		staleParsingBefore time.Time,
		reparsePriceErrorsBefore time.Time,
		limit int,
	) ([]RecoverableFile, error)

	ClaimFileForParsing(
		ctx context.Context,
		command ParseFileCommand,
	) ([]byte, error)

	SaveParsedFile(
		ctx context.Context,
		command ParseFileCommand,
		result AggregatedFile,
	) (File, error)

	Finalize(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		actor TrustedActor,
		command FinalizeCommand,
		commandDigest Digest,
	) (BatchHeader, error)

	CreateExternalBatch(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		draft ExternalBatchDraft,
	) (BatchHeader, error)

	ListFinalizedBatches(
		ctx context.Context,
		after *BatchCursor,
		limit int,
	) ([]BatchHeader, error)

	GetBatch(
		ctx context.Context,
		batchID BatchID,
	) (BatchHeader, error)

	ListBatchItems(
		ctx context.Context,
		batchID BatchID,
		afterPosition int,
		limit int,
	) ([]BatchItem, error)
}

type BatchReader interface {
	ListFinalizedBatches(
		ctx context.Context,
		after *BatchCursor,
		limit int,
	) ([]BatchHeader, error)

	GetBatch(
		ctx context.Context,
		batchID BatchID,
	) (BatchHeader, error)

	ListBatchItems(
		ctx context.Context,
		batchID BatchID,
		afterPosition int,
		limit int,
	) ([]BatchItem, error)
}
