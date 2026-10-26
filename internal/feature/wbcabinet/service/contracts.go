package wbcabinet_service

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type PreparedCredential interface {
	Generation() domain.ClientGeneration
	Activate() error
}

type Verifier interface {
	Prepare(context.Context, domain.SellerID, string, string) (PreparedCredential, error)
	Remove(domain.SellerID)
}

type Repository interface {
	OperationContext(context.Context) (context.Context, context.CancelFunc)
	FindByOwnerAndName(context.Context, int64, string) (Cabinet, bool, error)
	GetByOwner(context.Context, int64, domain.SellerID) (Cabinet, error)
	UpsertVerified(context.Context, core_postgres_transaction.DBTX, VerifiedCredential) (Cabinet, error)
	ListByOwner(context.Context, int64) ([]Cabinet, error)
	ListStored(context.Context) ([]StoredCabinet, error)
	ListTargetsByOwnerRole(context.Context, domain.UserRole) ([]domain.CabinetAccess, error)
	ListAllTargets(context.Context) ([]domain.CabinetAccess, error)
	MarkStatus(context.Context, domain.SellerID, Status) error
	DeleteByOwner(context.Context, int64, domain.SellerID) error
}
