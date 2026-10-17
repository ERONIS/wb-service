package wb

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

type ExecutorRegistry interface {
	ExecutorForCabinet(domain.CabinetID) (client.Executor, error)
}

type PinnedExecutorRegistry interface {
	ExecutorRegistry
	PinnedExecutor(domain.CabinetID, ClientGeneration) (client.Executor, error)
}

func ExecuteForCabinet[Response any](
	ctx context.Context,
	executors ExecutorRegistry,
	cabinetID domain.CabinetID,
	operation policy.Operation,
	query any,
	body any,
) (Response, error) {
	if executors == nil {
		var response Response
		return response, fmt.Errorf("get WB cabinet executor: registry is nil")
	}
	executor, err := executors.ExecutorForCabinet(cabinetID)
	if err != nil {
		var response Response
		return response, fmt.Errorf("get WB cabinet executor: %w", err)
	}
	return client.ExecuteResponse[Response](ctx, executor, operation, query, body)
}

func ExecutePinnedForCabinet[Response any](
	ctx context.Context,
	executors PinnedExecutorRegistry,
	cabinetID domain.CabinetID,
	generation ClientGeneration,
	operation policy.Operation,
	query any,
	body any,
) (Response, error) {
	if executors == nil {
		var response Response
		return response, fmt.Errorf("get pinned WB cabinet executor: registry is nil")
	}
	executor, err := executors.PinnedExecutor(cabinetID, generation)
	if err != nil {
		var response Response
		return response, fmt.Errorf("get pinned WB cabinet executor: %w", err)
	}
	return client.ExecuteResponse[Response](ctx, executor, operation, query, body)
}
