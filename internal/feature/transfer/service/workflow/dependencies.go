package workflow

import (
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
)

const (
	PhaseInitializing = model.PhaseInitializing
)

var ErrCapacityExceeded = model.ErrCapacityExceeded

const MaxPublicationPlanningPageSize = publication.MaxPublicationPlanningPageSize
