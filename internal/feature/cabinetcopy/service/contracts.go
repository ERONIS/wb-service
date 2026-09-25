package cabinetcopy_service

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type Reader interface {
	Cabinets() []Cabinet
	ListTags(context.Context, CabinetID) ([]Tag, error)
	ReadCards(
		context.Context,
		CabinetID,
		[]int64,
		int,
	) ([]PreparedItem, error)
}

// OwnerCabinetReader is implemented by runtime readers that can filter the
// shared WB client registry through the durable Telegram ownership binding.
type OwnerCabinetReader interface {
	CabinetsForOwner(context.Context, int64) ([]Cabinet, error)
}

type Repository interface {
	GetOrCreate(context.Context, int64) (Session, error)
	Get(context.Context, int64, SessionID) (Session, error)
	GetActive(context.Context, int64) (Session, error)
	SetSource(context.Context, int64, SessionID, int64, CabinetID) (Session, error)
	SetTarget(context.Context, int64, SessionID, int64, CabinetID) (Session, error)
	RequestCountInput(context.Context, int64, SessionID, int64) (Session, error)
	SetCount(context.Context, int64, SessionID, int64, int) (Session, error)
	SetTags(context.Context, int64, SessionID, int64, []Tag) (Session, error)
	SavePrepared(
		context.Context,
		core_postgres_transaction.DBTX,
		int64,
		SessionID,
		int64,
		[]PreparedItem,
	) (Session, error)
	ListPrepared(context.Context, int64, SessionID) ([]PreparedItem, error)
	AttachBatch(context.Context, int64, SessionID, int64, cardimport_service.BatchID) (Session, error)
	MarkSubmitted(
		context.Context,
		int64,
		SessionID,
		cardimport_service.BatchID,
		transfer_service.TransferID,
	) (Session, error)
	Cancel(context.Context, int64, SessionID, int64) (Session, error)
}

type BatchWriter interface {
	CreateExternalBatch(
		context.Context,
		cardimport_service.TrustedActor,
		cardimport_service.CreateExternalBatchCommand,
	) (cardimport_service.BatchHeader, error)
	GetBatch(
		context.Context,
		cardimport_service.BatchID,
	) (cardimport_service.BatchHeader, error)
}

type TransferStarter interface {
	StartToCabinet(
		context.Context,
		cardimport_service.BatchID,
		transfer_service.CabinetID,
	) (transfer_service.TransferID, error)
}
