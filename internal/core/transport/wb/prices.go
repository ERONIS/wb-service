package wb

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	pricesapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/prices/v2"
	"github.com/ERONIS/wb-service/internal/core/transport/wb/client"
)

type PricesExecutorRegistry interface {
	PricesExecutorForCabinet(domain.CabinetID) (client.Executor, error)
}

// PricesClient предоставляет типизированные операции API цен.
type PricesClient struct{ executors PricesExecutorRegistry }

func NewPricesClient(executors PricesExecutorRegistry) *PricesClient {
	if executors == nil {
		panic("WB prices executor registry is nil")
	}
	return &PricesClient{executors: executors}
}

func (prices *PricesClient) GoodsByNM(ctx context.Context, cabinetID domain.CabinetID, request pricesapi.GoodsByNMRequest) (pricesapi.GoodsResponse, error) {
	executor, err := prices.executors.PricesExecutorForCabinet(cabinetID)
	if err != nil {
		return pricesapi.GoodsResponse{}, fmt.Errorf("get WB prices executor: %w", err)
	}
	return client.ExecuteResponse[pricesapi.GoodsResponse](ctx, executor, pricesapi.GoodsByNMOperation(), nil, request)
}
