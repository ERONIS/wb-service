package wb

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

type ExecutorRegistry interface {
	ExecutorForCabinet(domain.SellerID) (client.Executor, error)
}

type PinnedExecutorRegistry interface {
	ExecutorRegistry
	PinnedExecutor(domain.SellerID, ClientGeneration) (client.Executor, error)
}

func ExecuteForCabinet[Response any](
	ctx context.Context,
	executors ExecutorRegistry,
	sellerID domain.SellerID,
	operation policy.Operation,
	query any,
	body any,
) (Response, error) {
	if executors == nil {
		var response Response
		return response, client.ExecutorUnavailableError(
			fmt.Errorf("get WB cabinet executor: registry is nil"),
		)
	}
	executor, err := executors.ExecutorForCabinet(sellerID)
	if err != nil {
		var response Response
		return response, client.ExecutorUnavailableError(
			fmt.Errorf("get WB cabinet executor: %w", err),
		)
	}
	return client.ExecuteResponse[Response](ctx, executor, operation, query, body)
}

func ExecutePinnedForCabinet[Response any](
	ctx context.Context,
	executors PinnedExecutorRegistry,
	sellerID domain.SellerID,
	generation ClientGeneration,
	operation policy.Operation,
	query any,
	body any,
) (Response, error) {
	if executors == nil {
		var response Response
		return response, client.ExecutorUnavailableError(
			fmt.Errorf("get pinned WB cabinet executor: registry is nil"),
		)
	}
	executor, err := executors.PinnedExecutor(sellerID, generation)
	if err != nil {
		var response Response
		return response, client.ExecutorUnavailableError(
			fmt.Errorf("get pinned WB cabinet executor: %w", err),
		)
	}
	return client.ExecuteResponse[Response](ctx, executor, operation, query, body)
}
