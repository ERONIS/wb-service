package statistics_service

import (
	"context"
	"errors"
)

type Service struct {
	reader Reader
}

func New(reader Reader) *Service {
	if reader == nil {
		panic("statistics reader is nil")
	}
	return &Service{reader: reader}
}

func (service *Service) ListOperations(
	ctx context.Context,
	filter OperationFilter,
) ([]OperationRow, error) {
	if ctx == nil {
		return nil, errors.New("statistics context is nil")
	}
	filter, err := filter.Normalize()
	if err != nil {
		return nil, err
	}
	return service.reader.ListOperations(ctx, filter)
}

func (service *Service) GetOperation(
	ctx context.Context,
	transferID int64,
) (OperationDetails, error) {
	if ctx == nil {
		return OperationDetails{}, errors.New("statistics context is nil")
	}
	if transferID <= 0 {
		return OperationDetails{}, ErrInvalidFilter
	}
	return service.reader.GetOperation(ctx, transferID)
}

func (service *Service) GetAction(
	ctx context.Context,
	actionID int64,
) (ActionDetails, error) {
	if ctx == nil {
		return ActionDetails{}, errors.New("statistics context is nil")
	}
	if actionID <= 0 {
		return ActionDetails{}, ErrInvalidFilter
	}
	return service.reader.GetAction(ctx, actionID)
}

func (service *Service) ListCabinetProgress(
	ctx context.Context,
	transferID int64,
) ([]CabinetProgressRow, error) {
	if ctx == nil {
		return nil, errors.New("statistics context is nil")
	}
	if transferID <= 0 {
		return nil, ErrInvalidFilter
	}
	return service.reader.ListCabinetProgress(ctx, transferID)
}

func (service *Service) ListCabinetTasks(
	ctx context.Context,
	filter CabinetTaskFilter,
) (CabinetTaskPage, error) {
	if ctx == nil {
		return CabinetTaskPage{}, errors.New("statistics context is nil")
	}
	filter, err := filter.Normalize()
	if err != nil {
		return CabinetTaskPage{}, err
	}
	return service.reader.ListCabinetTasks(ctx, filter)
}

func (service *Service) AggregateTransfers(
	ctx context.Context,
	filter AggregateFilter,
) (TransferTotals, error) {
	filter, err := normalizeAggregate(ctx, filter)
	if err != nil {
		return TransferTotals{}, err
	}
	return service.reader.AggregateTransfers(ctx, filter)
}

func (service *Service) AggregateItems(
	ctx context.Context,
	filter AggregateFilter,
) (ItemTotals, error) {
	filter, err := normalizeAggregate(ctx, filter)
	if err != nil {
		return ItemTotals{}, err
	}
	return service.reader.AggregateItems(ctx, filter)
}

func (service *Service) AggregateActions(
	ctx context.Context,
	filter AggregateFilter,
) (ActionTotals, error) {
	filter, err := normalizeAggregate(ctx, filter)
	if err != nil {
		return ActionTotals{}, err
	}
	return service.reader.AggregateActions(ctx, filter)
}

func (service *Service) AggregateErrors(
	ctx context.Context,
	filter AggregateFilter,
) ([]ErrorGroup, error) {
	filter, err := normalizeAggregate(ctx, filter)
	if err != nil {
		return nil, err
	}
	return service.reader.AggregateErrors(ctx, filter)
}

func (service *Service) ListAttention(
	ctx context.Context,
	filter AttentionFilter,
) ([]AttentionRow, error) {
	if ctx == nil {
		return nil, errors.New("statistics context is nil")
	}
	filter, err := filter.Normalize()
	if err != nil {
		return nil, err
	}
	return service.reader.ListAttention(ctx, filter)
}

func normalizeAggregate(
	ctx context.Context,
	filter AggregateFilter,
) (AggregateFilter, error) {
	if ctx == nil {
		return AggregateFilter{}, errors.New("statistics context is nil")
	}
	return filter.Normalize()
}

var _ Reader = (*Service)(nil)
