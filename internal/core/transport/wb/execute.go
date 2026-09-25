package wb

import (
	"context"
	"fmt"

	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

type ExecutorRegistry interface {
	ExecutorForCabinet(config.CabinetID) (client.Executor, error)
}

type PinnedExecutorRegistry interface {
	ExecutorRegistry
	PinnedExecutor(config.CabinetID, ClientGeneration) (client.Executor, error)
}

func ExecuteForCabinet[Response any](
	ctx context.Context,
	executors ExecutorRegistry,
	cabinetID config.CabinetID,
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
	cabinetID config.CabinetID,
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
