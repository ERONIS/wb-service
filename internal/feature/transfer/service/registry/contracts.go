package registry

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
)

type TargetTransport interface {
	Credentials(context.Context, int64) ([]TargetCredential, error)
	CredentialsByOwnerRole(context.Context, domain.UserRole) ([]TargetCredential, error)
	AllCredentials(context.Context) ([]TargetCredential, error)
}

type TargetRegistry interface {
	MutationSnapshot(context.Context, int64) (MutationTargetSnapshot, error)
	FanoutMutationSnapshot(context.Context) (MutationTargetSnapshot, error)
	MutationSnapshotForOwner(
		context.Context,
		int64,
		[]CabinetID,
	) (MutationTargetSnapshot, error)
	AllTargets(context.Context) ([]MutationTarget, error)
}
