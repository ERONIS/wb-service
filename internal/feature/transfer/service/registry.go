package transfer_service

import "github.com/ERONIS/wb-service/internal/feature/transfer/service/registry"

type TargetTransport = registry.TargetTransport
type TargetCredential = registry.TargetCredential
type MutationTargetRegistry = registry.MutationTargetRegistry

func NewMutationTargetRegistry(
	transport TargetTransport,
	cohortName string,
) *MutationTargetRegistry {
	return registry.NewMutationTargetRegistry(transport, cohortName)
}
