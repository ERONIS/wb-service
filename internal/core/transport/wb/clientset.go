package wb

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	flowcontrol "github.com/ERONIS/wb-service/internal/core/transport/wb/flowcontrol"
	transport "github.com/ERONIS/wb-service/internal/core/transport/wb/transport"
	contentv1 "github.com/ERONIS/wb-service/internal/core/transport/wb/typed/content/v1"
	"go.uber.org/zap"
)

const (
	contentAPIHost         = "content-api.wildberries.ru"
	defaultRetryBaseDelay  = 500 * time.Millisecond
	defaultMaxReadAttempts = 3
)

// Clientset хранит immutable registry кабинетов и общий connection pool.
type Clientset struct {
	cabinets        map[config.CabinetID]*CabinetClient
	cabinetInfos    []CabinetInfo
	sharedTransport *transport.SharedTransport
}

// NewForConfig создаёт Clientset поверх принадлежащей WB Core копии
// http.DefaultTransport.
func NewForConfig(
	configuration *config.Config,
	logger *zap.Logger,
) (*Clientset, error) {
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

	return buildClientset(
		configCopy,
		baseURL,
		baseHTTPClient,
		sharedTransport,
		logger,
	)
}

// NewForConfigAndHTTPClient создаёт Clientset поверх явно внедрённого base
// HTTP client, не изменяя его.
func NewForConfigAndHTTPClient(
	configuration *config.Config,
	httpClient *http.Client,
	logger *zap.Logger,
) (*Clientset, error) {
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

	return buildClientset(
		configCopy,
		baseURL,
		&baseHTTPClient,
		sharedTransport,
		logger,
	)
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

// ForCabinet возвращает typed client указанного кабинета.
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
	baseURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse WB API base URL: %w", err)
	}

	if baseURL.Scheme != "https" {
		return nil, fmt.Errorf("WB API base URL must use HTTPS")
	}
	if !strings.EqualFold(baseURL.Hostname(), contentAPIHost) {
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

	rateLimiters, err := flowcontrol.NewRegistry(
		contentapi.BucketSpecs(),
	)
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

		contentClient, err := contentv1.NewContentV1Client(apiClient)
		if err != nil {
			return nil, fmt.Errorf(
				"create WB Content v1 client for cabinet %q: %w",
				cabinetConfig.ID,
				err,
			)
		}

		cabinets[cabinetConfig.ID] = &CabinetClient{
			id:        cabinetConfig.ID,
			name:      cabinetConfig.Name,
			contentV1: contentClient,
		}
		cabinetInfos = append(cabinetInfos, CabinetInfo{
			ID:   cabinetConfig.ID,
			Name: cabinetConfig.Name,
		})
	}

	return &Clientset{
		cabinets:        cabinets,
		cabinetInfos:    cabinetInfos,
		sharedTransport: sharedTransport,
	}, nil
}
