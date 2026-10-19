package wbcabinet_service

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
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
	FindByOwnerAndName(context.Context, int64, string) (Cabinet, bool, error)
	GetByOwner(context.Context, int64, SellerID) (Cabinet, error)
	UpsertVerified(context.Context, VerifiedCredential) (Cabinet, error)
	ListByOwner(context.Context, int64) ([]Cabinet, error)
	ListStored(context.Context) ([]StoredCabinet, error)
	ListTargets(context.Context, int64) ([]TargetCredential, error)
	ListTargetsByOwnerRole(context.Context, domain.UserRole) ([]TargetCredential, error)
	ListAllTargets(context.Context) ([]TargetCredential, error)
	MarkStatus(context.Context, SellerID, Status) error
	DeleteByOwner(context.Context, int64, SellerID) error
}
