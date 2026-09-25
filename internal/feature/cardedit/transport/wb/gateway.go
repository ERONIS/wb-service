package cardedit_wb_transport

import (
	"context"

	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardedit_service "github.com/ERONIS/wb-service/internal/feature/cardedit/service"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"
)

type CabinetSource interface {
	Targets(context.Context, int64) ([]wbcabinet_service.TargetCredential, error)
}

type Gateway struct {
	executors core_wb.PinnedExecutorRegistry
	cabinets  CabinetSource
}

func NewGateway(
	executors core_wb.PinnedExecutorRegistry,
	cabinets CabinetSource,
) *Gateway {
	if executors == nil || cabinets == nil {
		panic("cardedit WB gateway dependency is nil")
	}
	return &Gateway{executors: executors, cabinets: cabinets}
}

func (gateway *Gateway) Cabinets(
	ctx context.Context,
	ownerTelegramID int64,
) ([]cardedit_service.Cabinet, error) {
	targets, err := gateway.cabinets.Targets(ctx, ownerTelegramID)
	if err != nil {
		return nil, err
	}
	result := make([]cardedit_service.Cabinet, len(targets))
	for index, target := range targets {
		result[index] = cardedit_service.Cabinet{
			ID:                 target.CabinetID,
			Name:               target.Name,
			BindingRevision:    target.BindingRevision,
			CapabilityRevision: target.CapabilityRevision,
			ClientGeneration:   target.ClientGeneration,
		}
	}
	return result, nil
}

func (gateway *Gateway) CardsList(
	ctx context.Context,
	cabinetID wbcabinet_service.CabinetID,
	query contentapi.CardsListQuery,
	request contentapi.CardsListRequest,
) (contentapi.CardsListResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.CardsListResponse](
		ctx, gateway.executors, cabinetID,
		contentapi.CardsListOperation(), query, request,
	)
}

func (gateway *Gateway) SubjectCharacteristics(
	ctx context.Context,
	cabinetID wbcabinet_service.CabinetID,
	subjectID int64,
	query contentapi.SubjectCharacteristicsQuery,
) (contentapi.SubjectCharacteristicsResponse, error) {
	operation, err := contentapi.SubjectCharacteristicsOperation(subjectID)
	if err != nil {
		return contentapi.SubjectCharacteristicsResponse{}, err
	}
	return core_wb.ExecuteForCabinet[contentapi.SubjectCharacteristicsResponse](
		ctx, gateway.executors, cabinetID, operation, query, nil,
	)
}

func (gateway *Gateway) CardsErrorList(
	ctx context.Context,
	cabinetID wbcabinet_service.CabinetID,
	query contentapi.CardsErrorListQuery,
	request contentapi.CardsErrorListRequest,
) (contentapi.CardsErrorListResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.CardsErrorListResponse](
		ctx, gateway.executors, cabinetID,
		contentapi.CardsErrorListOperation(), query, request,
	)
}

func (gateway *Gateway) UpdateCards(
	ctx context.Context,
	cabinetID wbcabinet_service.CabinetID,
	generation wbcabinet_service.ClientGeneration,
	request contentapi.UpdateCardsRequest,
) (contentapi.UpdateCardsResponse, error) {
	return core_wb.ExecutePinnedForCabinet[contentapi.UpdateCardsResponse](
		ctx, gateway.executors, cabinetID, generation,
		contentapi.UpdateCardsOperation(), nil, request,
	)
}

func (gateway *Gateway) SaveMediaByLinks(
	ctx context.Context,
	cabinetID wbcabinet_service.CabinetID,
	generation wbcabinet_service.ClientGeneration,
	request contentapi.SaveMediaByLinksRequest,
) (contentapi.SaveMediaByLinksResponse, error) {
	return core_wb.ExecutePinnedForCabinet[contentapi.SaveMediaByLinksResponse](
		ctx, gateway.executors, cabinetID, generation,
		contentapi.SaveMediaByLinksOperation(), nil, request,
	)
}

var _ cardedit_service.Gateway = (*Gateway)(nil)
