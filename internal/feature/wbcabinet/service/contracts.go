package wbcabinet_service

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type PreparedCredential interface {
	Generation() ClientGeneration
	Activate() error
}

type Verifier interface {
	Prepare(context.Context, SellerID, string, string) (PreparedCredential, error)
	Remove(SellerID)
}

type Repository interface {
	OperationContext(context.Context) (context.Context, context.CancelFunc)
	FindByOwnerAndName(context.Context, int64, string) (Cabinet, bool, error)
	GetByOwner(context.Context, int64, SellerID) (Cabinet, error)
	UpsertVerified(context.Context, core_postgres_transaction.DBTX, VerifiedCredential) (Cabinet, error)
	ListByOwner(context.Context, int64) ([]Cabinet, error)
	ListStored(context.Context) ([]StoredCabinet, error)
	ListTargets(context.Context, int64) ([]TargetCredential, error)
	ListTargetsByOwnerRole(context.Context, domain.UserRole) ([]TargetCredential, error)
	ListAllTargets(context.Context) ([]TargetCredential, error)
	MarkStatus(context.Context, SellerID, Status) error
	DeleteByOwner(context.Context, int64, SellerID) error
}
