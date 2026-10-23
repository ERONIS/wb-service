package client

import (
	"net/http"
	"strings"

	"github.com/ERONIS/wb-service/internal/core/domain"
	flowcontrol "github.com/ERONIS/wb-service/internal/core/transport/wb/flowcontrol"
	"go.uber.org/zap"
)

// APIClient содержит общие зависимости для выполнения WB-запросов.
type APIClient struct {
	sellerID     domain.SellerID
	cabinetName  string
	token        string
	httpClient   *http.Client
	rateLimiters *flowcontrol.Registry
	logger       *zap.Logger
}

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

	return &APIClient{
		sellerID:     sellerID,
		cabinetName:  cabinetName,
		token:        token,
		httpClient:   httpClient,
		rateLimiters: rateLimiters,
		logger:       logger,
	}, nil
}

// validateAPIClientDependencies проверяет данные кабинета и общие зависимости клиента.
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
