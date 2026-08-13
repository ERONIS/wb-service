package cardimport_service

import (
	"context"
	"io"

	platform_transaction "github.com/ERONIS/wb-service/internal/platform/transaction"
)

type StoredContent struct {
	Bytes  []byte
	SHA256 [32]byte
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
		tx platform_transaction.DBTX,
		actor TrustedActor,
		command FinalizeCommand,
		commandDigest Digest,
	) (BatchHeader, error)

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

type BatchConsumer interface {
	Start(ctx context.Context, batchID BatchID) error
}
