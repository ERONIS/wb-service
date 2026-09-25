package wb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	generalapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/general/v1"
	pricesapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/prices/v2"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	flowcontrol "github.com/ERONIS/wb-service/internal/core/transport/wb/flowcontrol"
	transport "github.com/ERONIS/wb-service/internal/core/transport/wb/transport"
	"go.uber.org/zap"
)

const (
	contentAPIHost         = "content-api.wildberries.ru"
	commonAPIHost          = "common-api.wildberries.ru"
	commonAPIBaseURL       = "https://common-api.wildberries.ru"
	pricesAPIHost          = "discounts-prices-api.wildberries.ru"
	pricesAPIBaseURL       = "https://discounts-prices-api.wildberries.ru"
	defaultRetryBaseDelay  = 500 * time.Millisecond
	defaultMaxReadAttempts = 3
)

type CabinetInfo struct {
	ID   config.CabinetID
	Name string
}

// CredentialCandidate is isolated from the runtime registry until the
// wbcabinet feature verifies and explicitly activates it.
type CredentialCandidate struct {
	id         config.CabinetID
	name       string
	generation ClientGeneration
	content    *CabinetClient
	general    client.Executor
	prices     client.Executor
}

func (candidate *CredentialCandidate) ID() config.CabinetID {
	if candidate == nil {
		return ""
	}
	return candidate.id
}

func (candidate *CredentialCandidate) Name() string {
	if candidate == nil {
		return ""
	}
	return candidate.name
}

func (candidate *CredentialCandidate) Generation() ClientGeneration {
	if candidate == nil {
		return ClientGeneration{}
	}
	return candidate.generation
}

func (candidate *CredentialCandidate) ContentExecutor() client.Executor {
	if candidate == nil {
		return nil
	}
	return candidate.content
}

func (candidate *CredentialCandidate) GeneralExecutor() client.Executor {
	if candidate == nil {
		return nil
	}
	return candidate.general
}

func (candidate *CredentialCandidate) PricesExecutor() client.Executor {
	if candidate == nil {
		return nil
	}
	return candidate.prices
}

// Clientset owns shared HTTP/rate-limit infrastructure and a mutable runtime
// registry containing only identity-verified cabinets.
type Clientset struct {
	mutex           sync.RWMutex
	cabinets        map[config.CabinetID]*CabinetClient
	cabinetInfos    map[config.CabinetID]CabinetInfo
	generations     map[config.CabinetID]ClientGeneration
	commonExecutors map[config.CabinetID]client.Executor
	pricesExecutors map[config.CabinetID]client.Executor

	contentBaseURL  *url.URL
	commonBaseURL   *url.URL
	pricesBaseURL   *url.URL
	baseHTTPClient  *http.Client
	rateLimiters    *flowcontrol.Registry
	logger          *zap.Logger
	sharedTransport *transport.SharedTransport
}

func NewForConfig(
	ctx context.Context,
	configuration *config.Config,
	logger *zap.Logger,
) (*Clientset, error) {
	if ctx == nil {
		return nil, fmt.Errorf("WB startup context is required")
	}
	configCopy, contentBaseURL, err := prepareClientsetConfig(configuration, logger)
	if err != nil {
		return nil, err
	}
	sharedTransport, err := transport.NewSharedTransport()
	if err != nil {
		return nil, fmt.Errorf("create WB shared HTTP transport: %w", err)
	}
	baseHTTPClient := &http.Client{Timeout: configCopy.Timeout}
	return buildClientset(configCopy, contentBaseURL, baseHTTPClient, sharedTransport, logger)
}

func NewForConfigAndHTTPClient(
	ctx context.Context,
	configuration *config.Config,
	httpClient *http.Client,
	logger *zap.Logger,
) (*Clientset, error) {
	if ctx == nil {
		return nil, fmt.Errorf("WB startup context is required")
	}
	configCopy, contentBaseURL, err := prepareClientsetConfig(configuration, logger)
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
	sharedTransport, err := transport.NewSharedTransportFrom(baseRoundTripper)
	if err != nil {
		return nil, fmt.Errorf("create WB shared HTTP transport: %w", err)
	}
	baseHTTPClient := *httpClient
	baseHTTPClient.Timeout = configCopy.Timeout
	return buildClientset(configCopy, contentBaseURL, &baseHTTPClient, sharedTransport, logger)
}

// NewCandidate builds credential-bound clients without exposing them through
// ForCabinet. It is the verification seam used by feature/wbcabinet.
func (clientset *Clientset) NewCandidate(
	cabinetConfig config.CabinetConfig,
) (*CredentialCandidate, error) {
	if clientset == nil {
		return nil, errors.New("WB clientset is required")
	}
	if err := cabinetConfig.Validate(); err != nil {
		return nil, err
	}
	cabinetConfig.Name = strings.TrimSpace(cabinetConfig.Name)
	roundTripper := clientset.sharedTransport.RoundTripper(
		transport.Authorization(cabinetConfig.Token),
		transport.AttemptTrace(),
	)
	httpClient, err := transport.NewHTTPClient(clientset.baseHTTPClient, roundTripper)
	if err != nil {
		return nil, fmt.Errorf("create WB HTTP client for cabinet %q: %w", cabinetConfig.ID, err)
	}
	contentClient, err := client.NewAPIClient(
		cabinetConfig.ID,
		cabinetConfig.Name,
		clientset.contentBaseURL,
		httpClient,
		clientset.rateLimiters,
		defaultRetryBaseDelay,
		defaultMaxReadAttempts,
		clientset.logger,
	)
	if err != nil {
		return nil, fmt.Errorf("create WB Content API client for cabinet %q: %w", cabinetConfig.ID, err)
	}
	generalClient, err := client.NewAPIClient(
		cabinetConfig.ID,
		cabinetConfig.Name,
		clientset.commonBaseURL,
		httpClient,
		clientset.rateLimiters,
		defaultRetryBaseDelay,
		defaultMaxReadAttempts,
		clientset.logger,
	)
	if err != nil {
		return nil, fmt.Errorf("create WB General API client for cabinet %q: %w", cabinetConfig.ID, err)
	}
	pricesClient, err := client.NewAPIClient(
		cabinetConfig.ID,
		cabinetConfig.Name,
		clientset.pricesBaseURL,
		httpClient,
		clientset.rateLimiters,
		defaultRetryBaseDelay,
		defaultMaxReadAttempts,
		clientset.logger,
	)
	if err != nil {
		return nil, fmt.Errorf("create WB Prices API client for cabinet %q: %w", cabinetConfig.ID, err)
	}
	return &CredentialCandidate{
		id:         cabinetConfig.ID,
		name:       cabinetConfig.Name,
		generation: Generation(cabinetConfig.Token),
		content: &CabinetClient{
			id:       cabinetConfig.ID,
			name:     cabinetConfig.Name,
			executor: contentClient,
		},
		general: generalClient,
		prices:  pricesClient,
	}, nil
}

// ActivateCandidate atomically publishes an already verified candidate.
func (clientset *Clientset) ActivateCandidate(candidate *CredentialCandidate) error {
	if clientset == nil {
		return errors.New("WB clientset is required")
	}
	if candidate == nil || candidate.id == "" || candidate.name == "" ||
		candidate.generation == (ClientGeneration{}) || candidate.content == nil ||
		candidate.general == nil || candidate.prices == nil {
		return errors.New("WB credential candidate is invalid")
	}
	clientset.mutex.Lock()
	defer clientset.mutex.Unlock()
	clientset.cabinets[candidate.id] = candidate.content
	clientset.cabinetInfos[candidate.id] = CabinetInfo{ID: candidate.id, Name: candidate.name}
	clientset.generations[candidate.id] = candidate.generation
	clientset.commonExecutors[candidate.id] = candidate.general
	clientset.pricesExecutors[candidate.id] = candidate.prices
	return nil
}

func (clientset *Clientset) RemoveCabinet(id config.CabinetID) {
	if clientset == nil {
		return
	}
	clientset.mutex.Lock()
	defer clientset.mutex.Unlock()
	delete(clientset.cabinets, id)
	delete(clientset.cabinetInfos, id)
	delete(clientset.generations, id)
	delete(clientset.commonExecutors, id)
	delete(clientset.pricesExecutors, id)
}

func (clientset *Clientset) Cabinets() []CabinetInfo {
	if clientset == nil {
		return nil
	}
	clientset.mutex.RLock()
	result := make([]CabinetInfo, 0, len(clientset.cabinetInfos))
	for _, cabinet := range clientset.cabinetInfos {
		result = append(result, cabinet)
	}
	clientset.mutex.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (clientset *Clientset) ForCabinet(id config.CabinetID) (*CabinetClient, error) {
	if clientset == nil {
		return nil, fmt.Errorf("WB clientset is required")
	}
	clientset.mutex.RLock()
	cabinet, exists := clientset.cabinets[id]
	clientset.mutex.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: %q", ErrCabinetNotFound, id)
	}
	return cabinet, nil
}

func (clientset *Clientset) ExecutorForCabinet(id config.CabinetID) (client.Executor, error) {
	return clientset.ForCabinet(id)
}

func (clientset *Clientset) PricesExecutorForCabinet(
	id config.CabinetID,
) (client.Executor, error) {
	if clientset == nil {
		return nil, fmt.Errorf("WB clientset is required")
	}
	clientset.mutex.RLock()
	executor, exists := clientset.pricesExecutors[id]
	clientset.mutex.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: %q", ErrCabinetNotFound, id)
	}
	return executor, nil
}

func (clientset *Clientset) PinnedExecutor(
	id config.CabinetID,
	generation ClientGeneration,
) (client.Executor, error) {
	if err := clientset.validateGeneration(id, generation); err != nil {
		return nil, err
	}
	return clientset.ForCabinet(id)
}

func (clientset *Clientset) PinnedGeneralExecutor(
	id config.CabinetID,
	generation ClientGeneration,
) (client.Executor, error) {
	if err := clientset.validateGeneration(id, generation); err != nil {
		return nil, err
	}
	clientset.mutex.RLock()
	executor, exists := clientset.commonExecutors[id]
	clientset.mutex.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: %q", ErrCabinetNotFound, id)
	}
	return executor, nil
}

func (clientset *Clientset) validateGeneration(
	id config.CabinetID,
	generation ClientGeneration,
) error {
	if clientset == nil {
		return errors.New("pin WB executor: clientset is nil")
	}
	clientset.mutex.RLock()
	current, exists := clientset.generations[id]
	clientset.mutex.RUnlock()
	if !exists {
		return fmt.Errorf("%w: %q", ErrCabinetNotFound, id)
	}
	if current != generation {
		return errors.New("pin WB executor: client generation changed")
	}
	return nil
}

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
		return config.Config{}, nil, fmt.Errorf("WB API config is required")
	}
	if logger == nil {
		return config.Config{}, nil, fmt.Errorf("WB logger is required")
	}
	configCopy := *configuration
	configCopy.Cabinets = append([]config.CabinetConfig(nil), configuration.Cabinets...)
	if err := configCopy.Validate(); err != nil {
		return config.Config{}, nil, fmt.Errorf("validate WB API config: %w", err)
	}
	for index := range configCopy.Cabinets {
		configCopy.Cabinets[index].Name = strings.TrimSpace(configCopy.Cabinets[index].Name)
	}
	sort.Slice(configCopy.Cabinets, func(i, j int) bool {
		return configCopy.Cabinets[i].ID < configCopy.Cabinets[j].ID
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

func parseProductionServiceURL(rawURL string, allowedHost string) (*url.URL, error) {
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
		return nil, fmt.Errorf("WB API base URL must not be opaque or contain user info")
	}
	if baseURL.Path != "" && baseURL.Path != "/" {
		return nil, fmt.Errorf("WB API base URL must not contain a path")
	}
	if baseURL.RawPath != "" || baseURL.RawQuery != "" || baseURL.ForceQuery ||
		baseURL.Fragment != "" || baseURL.RawFragment != "" {
		return nil, fmt.Errorf("WB API base URL must not contain path encoding, query, or fragment")
	}
	return baseURL, nil
}

func buildClientset(
	configuration config.Config,
	contentBaseURL *url.URL,
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
	bucketSpecs = append(bucketSpecs, pricesapi.BucketSpecs()...)
	rateLimiters, err := flowcontrol.NewRegistry(bucketSpecs)
	if err != nil {
		return nil, fmt.Errorf("create WB rate limiter registry: %w", err)
	}
	commonBaseURL, err := parseProductionServiceURL(commonAPIBaseURL, commonAPIHost)
	if err != nil {
		return nil, err
	}
	pricesBaseURL, err := parseProductionServiceURL(pricesAPIBaseURL, pricesAPIHost)
	if err != nil {
		return nil, err
	}
	return &Clientset{
		cabinets:        make(map[config.CabinetID]*CabinetClient),
		cabinetInfos:    make(map[config.CabinetID]CabinetInfo),
		generations:     make(map[config.CabinetID]ClientGeneration),
		commonExecutors: make(map[config.CabinetID]client.Executor),
		pricesExecutors: make(map[config.CabinetID]client.Executor),
		contentBaseURL:  contentBaseURL,
		commonBaseURL:   commonBaseURL,
		pricesBaseURL:   pricesBaseURL,
		baseHTTPClient:  baseHTTPClient,
		rateLimiters:    rateLimiters,
		logger:          logger,
		sharedTransport: sharedTransport,
	}, nil
}
