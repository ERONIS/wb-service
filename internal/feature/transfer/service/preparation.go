package transfer_service

import "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"

type PreparationSourceItem = preparation.PreparationSourceItem
type PreparationPendingGroup = preparation.PreparationPendingGroup
type PreparationSource = preparation.PreparationSource
type PreparationArtifact = preparation.PreparationArtifact
type ApplyPreparationResultCommand = preparation.ApplyPreparationResultCommand
type PreparationResultRepository = preparation.PreparationResultRepository

const (
	PreparationResultSucceeded  = preparation.PreparationResultSucceeded
	PreparationResultRejected   = preparation.PreparationResultRejected
	PreparationResultUnresolved = preparation.PreparationResultUnresolved
)

var ErrPreparationResultConflict = preparation.ErrPreparationResultConflict
