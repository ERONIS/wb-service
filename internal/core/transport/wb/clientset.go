package wb

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	generalapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/general/v1"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	flowcontrol "github.com/ERONIS/wb-service/internal/core/transport/wb/flowcontrol"
	identity "github.com/ERONIS/wb-service/internal/core/transport/wb/identity"
	transport "github.com/ERONIS/wb-service/internal/core/transport/wb/transport"
	"go.uber.org/zap"
)

const (
	contentAPIHost         = "content-api.wildberries.ru"
	commonAPIHost          = "common-api.wildberries.ru"
	commonAPIBaseURL       = "https://common-api.wildberries.ru"
	defaultRetryBaseDelay  = 500 * time.Millisecond
	defaultMaxReadAttempts = 3
)

type CabinetInfo struct {
	ID   config.CabinetID
	Name string
}

// Clientset хранит immutable registry кабинетов и общий connection pool.
type Clientset struct {
	cabinets         map[config.CabinetID]*CabinetClient
	cabinetInfos     []CabinetInfo
	credentialTokens map[config.CabinetID]string
	commonExecutors  map[config.CabinetID]client.Executor
	identityRegistry *identity.Registry
	sharedTransport  *transport.SharedTransport
}

// NewForConfig создаёт Clientset поверх принадлежащей WB Core копии
// http.DefaultTransport и до возврата проверяет credentials через WB Content
// and General APIs, then persists the verified identity bindings.
func NewForConfig(
	ctx context.Context,
	configuration *config.Config,
	identityStore identity.Store,
	logger *zap.Logger,
) (*Clientset, error) {
	if ctx == nil {
		return nil, fmt.Errorf("WB startup context is required")
	}

	configCopy, baseURL, err := prepareClientsetConfig(
		configuration,
		logger,
	)
	if err != nil {
		return nil, err
	}

	sharedTransport, err := transport.NewSharedTransport()
	if err != nil {
		return nil, fmt.Errorf(
			"create WB shared HTTP transport: %w",
			err,
		)
	}

	baseHTTPClient := &http.Client{Timeout: configCopy.Timeout}

	clientset, err := buildClientset(
		configCopy,
		baseURL,
		baseHTTPClient,
		sharedTransport,
		logger,
	)
	if err != nil {
		return nil, err
	}

	return verifyClientsetCredentials(ctx, clientset, identityStore)
}

// NewForConfigAndHTTPClient создаёт Clientset поверх явно внедрённого base
// HTTP client, не изменяя его, и до возврата проверяет credentials и identity.
func NewForConfigAndHTTPClient(
	ctx context.Context,
	configuration *config.Config,
	httpClient *http.Client,
	identityStore identity.Store,
	logger *zap.Logger,
) (*Clientset, error) {
	if ctx == nil {
		return nil, fmt.Errorf("WB startup context is required")
	}

	configCopy, baseURL, err := prepareClientsetConfig(
		configuration,
		logger,
	)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		return nil, fmt.Errorf("WB base HTTP client is required")
	}

	baseRoundTripper := httpClient.Transport
	if baseRoundTripper == nil {
		baseRoundTripper = http.DefaultTransport
	}

	sharedTransport, err := transport.NewSharedTransportFrom(
		baseRoundTripper,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"create WB shared HTTP transport: %w",
			err,
		)
	}

	baseHTTPClient := *httpClient
	baseHTTPClient.Timeout = configCopy.Timeout

	clientset, err := buildClientset(
		configCopy,
		baseURL,
		&baseHTTPClient,
		sharedTransport,
		logger,
	)
	if err != nil {
		return nil, err
	}

	return verifyClientsetCredentials(ctx, clientset, identityStore)
}

func verifyClientsetCredentials(
	ctx context.Context,
	clientset *Clientset,
	store identity.Store,
) (*Clientset, error) {
	registry, err := identity.NewRegistry(
		&credentialVerifier{clientset: clientset},
		store,
	)
	if err != nil {
		clientset.CloseIdleConnections()
		return nil, fmt.Errorf("create WB identity registry: %w", err)
	}
	credentials := make([]identity.Credential, 0, len(clientset.cabinetInfos))
	for _, cabinet := range clientset.cabinetInfos {
		token, exists := clientset.credentialTokens[cabinet.ID]
		if !exists {
			clientset.CloseIdleConnections()
			return nil, fmt.Errorf("WB credential %q is unavailable", cabinet.ID)
		}
		credentials = append(credentials, identity.Credential{
			CabinetID: cabinet.ID,
			Token:     token,
		})
	}
	if err := registry.VerifyAtStartup(ctx, time.Now().UTC(), credentials); err != nil {
		clientset.CloseIdleConnections()
		return nil, fmt.Errorf("verify WB credentials at startup: %w", err)
	}
	clientset.identityRegistry = registry

	return clientset, nil
}

// Cabinets возвращает deterministic snapshot настроенных кабинетов.
func (clientset *Clientset) Cabinets() []CabinetInfo {
	if clientset == nil {
		return nil
	}

	result := make([]CabinetInfo, len(clientset.cabinetInfos))
	copy(result, clientset.cabinetInfos)

	return result
}

// ForCabinet возвращает generic executor указанного кабинета.
func (clientset *Clientset) ForCabinet(
	id config.CabinetID,
) (*CabinetClient, error) {
	if clientset == nil {
		return nil, fmt.Errorf("WB clientset is required")
	}

	cabinet, exists := clientset.cabinets[id]
	if !exists {
		return nil, fmt.Errorf("%w: %q", ErrCabinetNotFound, id)
	}

	return cabinet, nil
}

// ExecutorForCabinet exposes only the generic execution contract expected by
// feature transports.
func (clientset *Clientset) ExecutorForCabinet(
	id config.CabinetID,
) (client.Executor, error) {
	return clientset.ForCabinet(id)
}

// CloseIdleConnections закрывает idle connections общего transport.
func (clientset *Clientset) CloseIdleConnections() {
	if clientset == nil || clientset.sharedTransport == nil {
		return
	}

	clientset.sharedTransport.CloseIdleConnections()
}

func prepareClientsetConfig(
	configuration *config.Config,
	logger *zap.Logger,
) (config.Config, *url.URL, error) {
	if configuration == nil {
		return config.Config{}, nil,
			fmt.Errorf("WB API config is required")
	}
	if logger == nil {
		return config.Config{}, nil,
			fmt.Errorf("WB logger is required")
	}

	configCopy := *configuration
	configCopy.Cabinets = append(
		[]config.CabinetConfig(nil),
		configuration.Cabinets...,
	)
	if err := configCopy.Validate(); err != nil {
		return config.Config{}, nil, fmt.Errorf(
			"validate WB API config: %w",
			err,
		)
	}

	for index := range configCopy.Cabinets {
		configCopy.Cabinets[index].Name = strings.TrimSpace(
			configCopy.Cabinets[index].Name,
		)
	}
	sort.Slice(configCopy.Cabinets, func(first, second int) bool {
		return configCopy.Cabinets[first].ID <
			configCopy.Cabinets[second].ID
	})

	baseURL, err := parseProductionBaseURL(configCopy.BaseURL)
	if err != nil {
		return config.Config{}, nil, err
	}

	return configCopy, baseURL, nil
}

func parseProductionBaseURL(rawURL string) (*url.URL, error) {
	return parseProductionServiceURL(rawURL, contentAPIHost)
}

func parseProductionServiceURL(
	rawURL string,
	allowedHost string,
) (*url.URL, error) {
	baseURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse WB API base URL: %w", err)
	}

	if baseURL.Scheme != "https" {
		return nil, fmt.Errorf("WB API base URL must use HTTPS")
	}
	if !strings.EqualFold(baseURL.Hostname(), allowedHost) {
		return nil, fmt.Errorf("WB API base URL host is not allowed")
	}
	if baseURL.Port() != "" {
		return nil, fmt.Errorf("WB API base URL must not contain a port")
	}
	if baseURL.Opaque != "" || baseURL.User != nil {
		return nil, fmt.Errorf(
			"WB API base URL must not be opaque or contain user info",
		)
	}
	if baseURL.Path != "" && baseURL.Path != "/" {
		return nil, fmt.Errorf("WB API base URL must not contain a path")
	}
	if baseURL.RawPath != "" {
		return nil, fmt.Errorf("WB API base URL must not contain a raw path")
	}
	if baseURL.RawQuery != "" || baseURL.ForceQuery {
		return nil, fmt.Errorf("WB API base URL must not contain a query")
	}
	if baseURL.Fragment != "" || baseURL.RawFragment != "" {
		return nil, fmt.Errorf("WB API base URL must not contain a fragment")
	}

	return baseURL, nil
}

func buildClientset(
	configuration config.Config,
	baseURL *url.URL,
	baseHTTPClient *http.Client,
	sharedTransport *transport.SharedTransport,
	logger *zap.Logger,
) (_ *Clientset, err error) {
	defer func() {
		if err != nil {
			sharedTransport.CloseIdleConnections()
		}
	}()

	bucketSpecs := append(contentapi.BucketSpecs(), generalapi.BucketSpecs()...)
	rateLimiters, err := flowcontrol.NewRegistry(bucketSpecs)
	if err != nil {
		return nil, fmt.Errorf(
			"create WB rate limiter registry: %w",
			err,
		)
	}

	cabinets := make(
		map[config.CabinetID]*CabinetClient,
		len(configuration.Cabinets),
	)
	cabinetInfos := make(
		[]CabinetInfo,
		0,
		len(configuration.Cabinets),
	)
	credentialTokens := make(
		map[config.CabinetID]string,
		len(configuration.Cabinets),
	)
	commonExecutors := make(
		map[config.CabinetID]client.Executor,
		len(configuration.Cabinets),
	)
	commonBaseURL, err := parseProductionServiceURL(
		commonAPIBaseURL,
		commonAPIHost,
	)
	if err != nil {
		return nil, err
	}

	for _, cabinetConfig := range configuration.Cabinets {
		roundTripper := sharedTransport.RoundTripper(
			transport.Authorization(cabinetConfig.Token),
			transport.AttemptTrace(),
		)

		cabinetHTTPClient, err := transport.NewHTTPClient(
			baseHTTPClient,
			roundTripper,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"create WB HTTP client for cabinet %q: %w",
				cabinetConfig.ID,
				err,
			)
		}

		apiClient, err := client.NewAPIClient(
			cabinetConfig.ID,
			cabinetConfig.Name,
			baseURL,
			cabinetHTTPClient,
			rateLimiters,
			defaultRetryBaseDelay,
			defaultMaxReadAttempts,
			logger,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"create WB API client for cabinet %q: %w",
				cabinetConfig.ID,
				err,
			)
		}
		commonAPIClient, err := client.NewAPIClient(
			cabinetConfig.ID,
			cabinetConfig.Name,
			commonBaseURL,
			cabinetHTTPClient,
			rateLimiters,
			defaultRetryBaseDelay,
			defaultMaxReadAttempts,
			logger,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"create WB General API client for cabinet %q: %w",
				cabinetConfig.ID,
				err,
			)
		}

		cabinets[cabinetConfig.ID] = &CabinetClient{
			id:       cabinetConfig.ID,
			name:     cabinetConfig.Name,
			executor: apiClient,
		}
		credentialTokens[cabinetConfig.ID] = cabinetConfig.Token
		commonExecutors[cabinetConfig.ID] = commonAPIClient
		cabinetInfos = append(cabinetInfos, CabinetInfo{
			ID:   cabinetConfig.ID,
			Name: cabinetConfig.Name,
		})
	}

	return &Clientset{
		cabinets:         cabinets,
		cabinetInfos:     cabinetInfos,
		credentialTokens: credentialTokens,
		commonExecutors:  commonExecutors,
		sharedTransport:  sharedTransport,
	}, nil
}
