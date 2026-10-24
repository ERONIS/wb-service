package transfer_service

import "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"

const (
	PreparationResultSucceeded  = preparation.PreparationResultSucceeded
	PreparationResultRejected   = preparation.PreparationResultRejected
	PreparationResultUnresolved = preparation.PreparationResultUnresolved
)

var ErrPreparationResultConflict = preparation.ErrPreparationResultConflict
