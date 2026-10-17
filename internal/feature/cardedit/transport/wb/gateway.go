package cardedit_wb_transport

import (
	"context"

	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	cardedit_service "github.com/ERONIS/wb-service/internal/feature/cardedit/service"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"
)

type CabinetSource interface {
	Targets(context.Context, int64) ([]wbcabinet_service.TargetCredential, error)
}

type Gateway struct {
	*core_wb.ContentClient
	cabinets CabinetSource
}

func NewGateway(
	executors core_wb.PinnedExecutorRegistry,
	cabinets CabinetSource,
) *Gateway {
	if executors == nil || cabinets == nil {
		panic("cardedit WB gateway dependency is nil")
	}
	return &Gateway{ContentClient: core_wb.NewContentClient(executors), cabinets: cabinets}
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

var _ cardedit_service.Gateway = (*Gateway)(nil)
