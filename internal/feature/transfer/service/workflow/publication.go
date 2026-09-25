package workflow

import (
	"context"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

func (service *Service) ListAwaitingAuthorization(
	ctx context.Context,
	afterID TransferID,
	limit int,
) ([]Transfer, error) {
	if ctx == nil {
		return nil, errors.New("list awaiting-authorization transfers: context is nil")
	}
	if afterID < 0 || limit <= 0 || limit > MaxPublicationPlanningPageSize {
		return nil, core_errors.ErrInvalidArgument
	}
	return service.repository.ListAwaitingAuthorization(ctx, afterID, limit)
}

func (service *Service) PublicationTargets(ctx context.Context) ([]MutationTarget, error) {
	if ctx == nil {
		return nil, errors.New("load publication targets: context is nil")
	}
	targets, err := service.targetRegistry.AllTargets(ctx)
	if err != nil {
		return nil, fmt.Errorf("load publication targets: %w", err)
	}
	return targets, nil
}

func (service *Service) LoadPublicationPlanningSource(
	ctx context.Context,
	transferID TransferID,
) (PublicationPlanningSource, error) {
	if ctx == nil {
		return PublicationPlanningSource{}, errors.New(
			"load publication planning source: context is nil",
		)
	}
	if transferID <= 0 {
		return PublicationPlanningSource{}, core_errors.ErrInvalidArgument
	}
	source, err := service.repository.LoadPublicationPlanningSource(ctx, transferID)
	if err != nil {
		return PublicationPlanningSource{}, err
	}
	if err := source.Validate(); err != nil {
		return PublicationPlanningSource{}, fmt.Errorf(
			"validate publication planning source: %w",
			err,
		)
	}
	return source, nil
}
