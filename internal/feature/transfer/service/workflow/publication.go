package workflow

import (
	"context"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_publication "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
)

func (service *Service) ListAwaitingAuthorization(
	ctx context.Context,
	afterID transfer_model.TransferID,
	limit int,
) ([]transfer_model.Transfer, error) {
	if ctx == nil {
		return nil, errors.New("list awaiting-authorization transfers: context is nil")
	}
	if afterID < 0 || limit <= 0 || limit > MaxPublicationPlanningPageSize {
		return nil, core_errors.ErrInvalidArgument
	}
	return service.repository.ListAwaitingAuthorization(ctx, afterID, limit)
}

func (service *Service) PublicationTargets(ctx context.Context) ([]transfer_model.MutationTarget, error) {
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
	transferID transfer_model.TransferID,
) (transfer_publication.PublicationPlanningSource, error) {
	if ctx == nil {
		return transfer_publication.PublicationPlanningSource{}, errors.New(
			"load publication planning source: context is nil",
		)
	}
	if transferID <= 0 {
		return transfer_publication.PublicationPlanningSource{}, core_errors.ErrInvalidArgument
	}
	source, err := service.repository.LoadPublicationPlanningSource(ctx, transferID)
	if err != nil {
		return transfer_publication.PublicationPlanningSource{}, err
	}
	if err := source.Validate(); err != nil {
		return transfer_publication.PublicationPlanningSource{}, fmt.Errorf(
			"validate publication planning source: %w",
			err,
		)
	}
	return source, nil
}
