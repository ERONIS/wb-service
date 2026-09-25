package wb

import (
	"context"
	"fmt"

	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

// ExecutorRegistry resolves the current credential-bound Content executor.
type ExecutorRegistry interface {
	ExecutorForCabinet(config.CabinetID) (client.Executor, error)
}

// PinnedExecutorRegistry additionally resolves an executor only when its
// credential generation still matches a previously frozen workflow snapshot.
type PinnedExecutorRegistry interface {
	ExecutorRegistry
	PinnedExecutor(config.CabinetID, ClientGeneration) (client.Executor, error)
}

// ExecuteForCabinet resolves the current cabinet executor and performs one
// closed WB operation. Feature transports remain responsible for selecting the
// operation and interpreting its response.
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

// ExecutePinnedForCabinet is the credential-generation-safe mutation variant
// of ExecuteForCabinet.
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
