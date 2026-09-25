package registry

import "context"

type TargetTransport interface {
	Credentials(context.Context, int64) ([]TargetCredential, error)
	FanoutCredentials(context.Context) ([]TargetCredential, error)
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
