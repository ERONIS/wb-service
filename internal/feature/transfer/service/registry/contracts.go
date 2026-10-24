package registry

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type TargetTransport interface {
	Credentials(context.Context, int64) ([]domain.CabinetAccess, error)
	CredentialsByOwnerRole(context.Context, domain.UserRole) ([]domain.CabinetAccess, error)
	AllCredentials(context.Context) ([]domain.CabinetAccess, error)
}

type TargetRegistry interface {
	MutationSnapshot(context.Context, int64) (transfer_model.MutationTargetSnapshot, error)
	FanoutMutationSnapshot(context.Context) (transfer_model.MutationTargetSnapshot, error)
	MutationSnapshotForOwner(
		context.Context,
		int64,
		[]domain.SellerID,
	) (transfer_model.MutationTargetSnapshot, error)
	AllTargets(context.Context) ([]transfer_model.MutationTarget, error)
}
