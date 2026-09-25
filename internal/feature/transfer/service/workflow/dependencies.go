package workflow

import (
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
)

type TransferID = model.TransferID
type Digest = model.Digest
type CabinetID = model.CabinetID
type Phase = model.Phase
type Transfer = model.Transfer
type MutationTarget = model.MutationTarget
type MutationTargetSnapshot = model.MutationTargetSnapshot
type CapacityPolicy = model.CapacityPolicy
type Capacity = model.Capacity

const (
	PhaseInitializing = model.PhaseInitializing
)

var ErrCapacityExceeded = model.ErrCapacityExceeded

func TargetSetRoot(snapshot MutationTargetSnapshot) Digest {
	return model.TargetSetRoot(snapshot)
}

type PreparationSource = preparation.PreparationSource
type PublicationPlanningSource = publication.PublicationPlanningSource

const MaxPublicationPlanningPageSize = publication.MaxPublicationPlanningPageSize
