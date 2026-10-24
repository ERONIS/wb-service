package transfer_service

import "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"

const (
	MaxPublicationPlanningPageSize = publication.MaxPublicationPlanningPageSize

	PublicationProjectionRunning  = publication.PublicationProjectionRunning
	PublicationProjectionSkipped  = publication.PublicationProjectionSkipped
	PublicationProjectionRejected = publication.PublicationProjectionRejected
)
