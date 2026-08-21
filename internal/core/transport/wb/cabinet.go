package wb

import (
	"context"
	"errors"

	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

// CabinetClient is a credential-bound generic executor. Endpoint DTO and
// operation selection belong to a feature transport.
type CabinetClient struct {
	id       config.CabinetID
	name     string
	executor client.Executor
}

func (client *CabinetClient) ID() config.CabinetID {
	if client == nil {
		return ""
	}

	return client.id
}

func (client *CabinetClient) Name() string {
	if client == nil {
		return ""
	}

	return client.name
}

func (cabinet *CabinetClient) Execute(
	ctx context.Context,
	operation policy.Operation,
	query any,
	body any,
	target any,
) error {
	if cabinet == nil || cabinet.executor == nil {
		return errors.New("WB cabinet executor is required")
	}
	return cabinet.executor.Execute(ctx, operation, query, body, target)
}

var _ client.Executor = (*CabinetClient)(nil)
