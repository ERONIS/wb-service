package client

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ERONIS/wb-service/internal/core/domain"
	flowcontrol "github.com/ERONIS/wb-service/internal/core/transport/wb/flowcontrol"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	"go.uber.org/zap"
)

// Executor — узкий контракт выполнения для типизированных клиентов.
type Executor interface {
	Execute(
		ctx context.Context,
		operation policy.Operation,
		query any,
		body any,
		target any,
	) error
}

// APIClient содержит общие зависимости для выполнения WB-запросов.
type APIClient struct {
	sellerID     domain.SellerID
	cabinetName  string
	token        string
	httpClient   *http.Client
	rateLimiters *flowcontrol.Registry
	backoff      flowcontrol.Backoff
	logger       *zap.Logger
}

var _ Executor = (*APIClient)(nil)

// NewAPIClient создаёт внутренний клиент WB API.
func NewAPIClient(
	sellerID domain.SellerID,
	cabinetName string,
	token string,
	httpClient *http.Client,
	rateLimiters *flowcontrol.Registry,
	logger *zap.Logger,
) (*APIClient, error) {
	if err := validateAPIClientDependencies(
		sellerID,
		cabinetName,
		httpClient,
		rateLimiters,
		logger,
	); err != nil {
		return nil, err
	}

	backoff, err := flowcontrol.NewBackoff(flowcontrol.DefaultRetryBaseDelay)
	if err != nil {
		return nil, fmt.Errorf(
			"create WB API retry backoff: %w",
			err,
		)
	}

	httpClientCopy := *httpClient

	return &APIClient{
		sellerID:     sellerID,
		cabinetName:  cabinetName,
		token:        token,
		httpClient:   &httpClientCopy,
		rateLimiters: rateLimiters,
		backoff:      backoff,
		logger:       logger,
	}, nil
}

func validateAPIClientDependencies(
	sellerID domain.SellerID,
	cabinetName string,
	httpClient *http.Client,
	rateLimiters *flowcontrol.Registry,
	logger *zap.Logger,
) error {
	switch {
	case sellerID == "":
		return errAPISellerIDRequired

	case cabinetName == "" || strings.TrimSpace(cabinetName) != cabinetName:
		return errAPICabinetNameRequired

	case httpClient == nil:
		return errAPIHTTPClientRequired

	case httpClient.Transport == nil:
		return errAPIRoundTripperRequired

	case httpClient.Timeout <= 0:
		return errAPIHTTPClientTimeoutRequired

	case rateLimiters == nil:
		return errAPIRateLimitersRequired

	case logger == nil:
		return errAPILoggerRequired
	}

	return nil
}
