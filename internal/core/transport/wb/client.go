package core_transport_wb

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	core_transport_wb_middleware "github.com/ERONIS/wb-service/internal/core/transport/wb/middleware"
	core_wb_policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	core_transport_wb_requestmeta "github.com/ERONIS/wb-service/internal/core/transport/wb/requestmeta"
	"go.uber.org/zap"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}
type Credentials struct {
	Scope string
	Token string
}

type ScopedClient struct {
	client      *Client
	credentials Credentials
}

func NewClient(config Config, logger *zap.Logger) *Client {
	baseURL := strings.TrimSpace(config.BaseURL)
	baseURL = strings.TrimRight(baseURL, "/")

	baseTransport := http.DefaultTransport.(*http.Transport).Clone()

	transport := core_transport_wb_middleware.Chain(
		baseTransport,
		core_transport_wb_middleware.Logger(logger),
	)

	return &Client{

		baseURL: baseURL,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   config.Timeout,
		},
	}
}

func (client *Client) ForCredentials(
	credentials Credentials,
) *ScopedClient {
	credentials.Scope = strings.TrimSpace(credentials.Scope)
	credentials.Token = strings.TrimSpace(credentials.Token)

	return &ScopedClient{
		client:      client,
		credentials: credentials,
	}
}

func (client *ScopedClient) DoJSON(
	ctx context.Context,
	operation core_wb_policy.Operation,
	query url.Values,
	requestBody any,
	responseBody any,
) error {
	if err := operation.Validate(); err != nil {
		return fmt.Errorf("validate WB operation: %w", err)
	}

	requestCtx := core_transport_wb_requestmeta.WithMetadata(
		ctx , 
		core_transport_wb_requestmeta.Metadata{
			SellerScope: client.credentials.Scope,
			OperationName: operation.Name,
			BucketIDs: operation.Buckets,
			RetryMode: operation.RetryMode,
			Attempt: 1,
		},
	)

	request, err := client.newRequest(
		requestCtx,
		operation.Method,
		operation.Path,
		query,
		requestBody,
	)
	if err != nil {
		return fmt.Errorf("build WB request %w", err)
	}

	response, err := client.sendRequest(request)
	if err != nil {
		return err
	}
	body, err := readResponseBody(response)
	if err != nil {
		return err
	}
	if err := checkResponseStatus(response, body); err != nil {
		return err

	}
	if err := decodeResponseBody(body, responseBody); err != nil {
		return err
	}
	return nil
}

func (client *ScopedClient) sendRequest(
	request *http.Request,
) (*http.Response, error) {

	response, err := client.client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send WB request %w", err)
	}

	return response, nil

}
