package statistics_service

import (
	"context"
	"errors"

	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type Service struct {
	reader   Reader
	resolver VerifiedCardResolver
}

func New(reader Reader, resolver VerifiedCardResolver) *Service {
	if reader == nil || resolver == nil {
		panic("statistics dependency is nil")
	}
	return &Service{reader: reader, resolver: resolver}
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

func (service *Service) ResolveVerifiedCard(
	ctx context.Context,
	item AttentionRow,
	info CardVerificationInfo,
) error {
	if ctx == nil {
		return errors.New("statistics context is nil")
	}
	if item.ItemTargetID <= 0 || info.NMID <= 0 || info.NMID != info.Card.NMID {
		return errors.New("invalid arguments for resolve verified card")
	}
	return service.resolver.Resolve(ctx, cardpublication_service.VerifiedCardCommand{
		TransferID:             transfer_service.TransferID(item.TransferID),
		ActionID:               item.ActionID,
		ActionMemberID:         item.ActionMemberID,
		ExpectedActionRevision: item.ActionRevision,
		TransferItemTargetID:   item.ItemTargetID,
		AttemptID:              item.AttemptID,
		SellerID:               item.SellerID,
		VendorCode:             item.VendorCode,
	}, cardpublication_service.VerifiedCardEvidence{
		Card:       info.Card,
		ObservedAt: info.ObservedAt,
	})
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
