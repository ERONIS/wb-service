package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	flowcontrol "github.com/ERONIS/wb-service/internal/core/transport/wb/flowcontrol"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	"go.uber.org/zap"
)

const maxReadAttemptsLimit = 5

// Executor — узкий контракт выполнения для typed-клиентов.
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
	cabinetID       config.CabinetID
	cabinetName     string
	baseURL         *url.URL
	httpClient      *http.Client
	rateLimiters    *flowcontrol.Registry
	backoff         flowcontrol.Backoff
	maxReadAttempts int
	logger          *zap.Logger
}

// NewAPIClient создаёт внутренний клиент WB API.
func NewAPIClient(
	cabinetID config.CabinetID,
	cabinetName string,
	baseURL *url.URL,
	httpClient *http.Client,
	rateLimiters *flowcontrol.Registry,
	retryBaseDelay time.Duration,
	maxReadAttempts int,
	logger *zap.Logger,
) (*APIClient, error) {
	if err := validateAPIClientDependencies(
		cabinetID,
		cabinetName,
		baseURL,
		httpClient,
		rateLimiters,
		maxReadAttempts,
		logger,
	); err != nil {
		return nil, err
	}

	backoff, err := flowcontrol.NewBackoff(retryBaseDelay)
	if err != nil {
		return nil, fmt.Errorf(
			"create WB API retry backoff: %w",
			err,
		)
	}

	baseURLCopy := *baseURL
	httpClientCopy := *httpClient

	return &APIClient{
		cabinetID:       cabinetID,
		cabinetName:     cabinetName,
		baseURL:         &baseURLCopy,
		httpClient:      &httpClientCopy,
		rateLimiters:    rateLimiters,
		backoff:         backoff,
		maxReadAttempts: maxReadAttempts,
		logger:          logger,
	}, nil
}

func validateAPIClientDependencies(
	cabinetID config.CabinetID,
	cabinetName string,
	baseURL *url.URL,
	httpClient *http.Client,
	rateLimiters *flowcontrol.Registry,
	maxReadAttempts int,
	logger *zap.Logger,
) error {
	switch {
	case cabinetID == "":
		return errAPICabinetIDRequired

	case cabinetName == "" || strings.TrimSpace(cabinetName) != cabinetName:
		return errAPICabinetNameRequired

	case baseURL == nil:
		return errAPIBaseURLRequired

	case httpClient == nil:
		return errAPIHTTPClientRequired

	case httpClient.Transport == nil:
		return errAPIRoundTripperRequired

	case httpClient.Timeout <= 0:
		return errAPIHTTPClientTimeoutRequired

	case rateLimiters == nil:
		return errAPIRateLimitersRequired

	case maxReadAttempts <= 0:
		return errAPIMaxReadAttemptsRequired

	case maxReadAttempts > maxReadAttemptsLimit:
		return fmt.Errorf(
			"WB API max read attempts %d exceeds limit %d",
			maxReadAttempts,
			maxReadAttemptsLimit,
		)

	case logger == nil:
		return errAPILoggerRequired
	}

	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return fmt.Errorf(
			"WB API base URL has unsupported scheme %q",
			baseURL.Scheme,
		)
	}
	if baseURL.Hostname() == "" {
		return fmt.Errorf("WB API base URL host is empty")
	}
	if baseURL.Opaque != "" {
		return fmt.Errorf("WB API base URL must not be opaque")
	}
	if baseURL.User != nil {
		return fmt.Errorf("WB API base URL must not contain user info")
	}
	if baseURL.Path != "" && baseURL.Path != "/" {
		return fmt.Errorf("WB API base URL must not contain a path")
	}
	if baseURL.RawPath != "" {
		return fmt.Errorf("WB API base URL must not contain a raw path")
	}
	if baseURL.RawQuery != "" || baseURL.ForceQuery {
		return fmt.Errorf("WB API base URL must not contain a query")
	}
	if baseURL.Fragment != "" || baseURL.RawFragment != "" {
		return fmt.Errorf("WB API base URL must not contain a fragment")
	}

	return nil
}

var _ Executor = (*APIClient)(nil)
