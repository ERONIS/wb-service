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
	"unicode/utf8"

	"github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	pricesapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/prices/v2"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	flowcontrol "github.com/ERONIS/wb-service/internal/core/transport/wb/flowcontrol"
	transport "github.com/ERONIS/wb-service/internal/core/transport/wb/transport"
	"go.uber.org/zap"
)

const (
	contentAPIHost         = "content-api.wildberries.ru"
	pricesAPIHost          = "discounts-prices-api.wildberries.ru"
	defaultClientTimeout   = 45 * time.Second
	defaultRetryBaseDelay  = 500 * time.Millisecond
	defaultMaxReadAttempts = 3
	maxCabinetNameLength   = 128
	maxCabinetTokenLength  = 16 * 1024
)

type CabinetID = domain.CabinetID
type ClientGeneration = domain.ClientGeneration

type CabinetInfo struct {
	ID   CabinetID
	Name string
}

type CredentialCandidate struct {
	id         CabinetID
	name       string
	generation ClientGeneration
	content    client.Executor
	prices     client.Executor
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

type Clientset struct {
	mutex           sync.RWMutex
	cabinets        map[CabinetID]CredentialCandidate
	baseHTTPClient  *http.Client
	rateLimiters    *flowcontrol.Registry
	logger          *zap.Logger
	sharedTransport *transport.SharedTransport
}

// NewClientset создает реестр клиентов WB с константными сетевыми параметрами.
func NewClientset(
	ctx context.Context,
	logger *zap.Logger,
) (*Clientset, error) {
	return NewClientsetWithHTTPClient(ctx, &http.Client{Timeout: defaultClientTimeout}, logger)
}

// NewClientsetWithHTTPClient создает реестр клиентов с переданным базовым HTTP-клиентом.
func NewClientsetWithHTTPClient(
	ctx context.Context,
	httpClient *http.Client,
	logger *zap.Logger,
) (*Clientset, error) {
	if ctx == nil {
		return nil, fmt.Errorf("WB startup context is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("WB logger is required")
	}
	if httpClient == nil {
		return nil, fmt.Errorf("WB base HTTP client is required")
	}
	bucketSpecs := append(contentapi.BucketSpecs(), pricesapi.BucketSpecs()...)
	rateLimiters, err := flowcontrol.NewRegistry(bucketSpecs)
	if err != nil {
		return nil, fmt.Errorf("create WB rate limiter registry: %w", err)
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
	if baseHTTPClient.Timeout <= 0 {
		baseHTTPClient.Timeout = defaultClientTimeout
	}
	return &Clientset{
		cabinets:        make(map[CabinetID]CredentialCandidate),
		baseHTTPClient:  &baseHTTPClient,
		rateLimiters:    rateLimiters,
		logger:          logger,
		sharedTransport: sharedTransport,
	}, nil
}

func (clientset *Clientset) NewCandidate(
	id CabinetID,
	name string,
	token string,
) (*CredentialCandidate, error) {
	if clientset == nil {
		return nil, errors.New("WB clientset is required")
	}
	if err := validateCandidate(id, name, token); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	roundTripper := clientset.sharedTransport.RoundTripper(
		transport.Authorization(token),
		transport.AttemptTrace(),
	)
	httpClient, err := transport.NewHTTPClient(clientset.baseHTTPClient, roundTripper)
	if err != nil {
		return nil, fmt.Errorf("create WB HTTP client for cabinet %q: %w", id, err)
	}
	candidate := &CredentialCandidate{
		id:         id,
		name:       name,
		generation: domain.NewClientGeneration(token),
	}
	for _, api := range []struct {
		name     string
		baseURL  *url.URL
		executor *client.Executor
	}{
		{name: "Content", baseURL: &url.URL{Scheme: "https", Host: contentAPIHost}, executor: &candidate.content},
		{name: "Prices", baseURL: &url.URL{Scheme: "https", Host: pricesAPIHost}, executor: &candidate.prices},
	} {
		executor, err := client.NewAPIClient(
			id,
			name,
			api.baseURL,
			httpClient,
			clientset.rateLimiters,
			defaultRetryBaseDelay,
			defaultMaxReadAttempts,
			clientset.logger,
		)
		if err != nil {
			return nil, fmt.Errorf("create WB %s API client for cabinet %q: %w", api.name, id, err)
		}
		*api.executor = executor
	}
	return candidate, nil
}

func validateCandidate(id CabinetID, name string, token string) error {
	if err := id.Validate(); err != nil {
		return err
	}
	normalizedName := strings.TrimSpace(name)
	if normalizedName == "" {
		return fmt.Errorf("validate cabinet %q name: cabinet name is empty", id)
	}
	if !utf8.ValidString(normalizedName) {
		return fmt.Errorf("validate cabinet %q name: cabinet name is not valid UTF-8", id)
	}
	if utf8.RuneCountInString(normalizedName) > maxCabinetNameLength {
		return fmt.Errorf("validate cabinet %q name: cabinet name exceeds %d characters", id, maxCabinetNameLength)
	}
	if token == "" {
		return fmt.Errorf("validate cabinet %q token: cabinet token is empty", id)
	}
	if len(token) > maxCabinetTokenLength {
		return fmt.Errorf("validate cabinet %q token: cabinet token exceeds %d bytes", id, maxCabinetTokenLength)
	}
	if strings.TrimSpace(token) != token {
		return fmt.Errorf("validate cabinet %q token: cabinet token contains surrounding whitespace", id)
	}
	return nil
}

func (clientset *Clientset) ActivateCandidate(candidate *CredentialCandidate) error {
	if clientset == nil {
		return errors.New("WB clientset is required")
	}
	if candidate == nil || candidate.id == "" || candidate.name == "" ||
		candidate.generation.IsZero() || candidate.content == nil ||
		candidate.prices == nil {
		return errors.New("WB credential candidate is invalid")
	}
	clientset.mutex.Lock()
	defer clientset.mutex.Unlock()
	clientset.cabinets[candidate.id] = *candidate
	return nil
}

func (clientset *Clientset) RemoveCabinet(id CabinetID) {
	if clientset == nil {
		return
	}
	clientset.mutex.Lock()
	defer clientset.mutex.Unlock()
	delete(clientset.cabinets, id)
}

func (clientset *Clientset) Cabinets() []CabinetInfo {
	if clientset == nil {
		return nil
	}
	clientset.mutex.RLock()
	result := make([]CabinetInfo, 0, len(clientset.cabinets))
	for _, cabinet := range clientset.cabinets {
		result = append(result, CabinetInfo{ID: cabinet.id, Name: cabinet.name})
	}
	clientset.mutex.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (clientset *Clientset) ExecutorForCabinet(id CabinetID) (client.Executor, error) {
	entry, err := clientset.cabinet(id, nil)
	return entry.content, err
}

func (clientset *Clientset) PricesExecutorForCabinet(id CabinetID) (client.Executor, error) {
	entry, err := clientset.cabinet(id, nil)
	return entry.prices, err
}

func (clientset *Clientset) PinnedExecutor(id CabinetID, generation ClientGeneration) (client.Executor, error) {
	entry, err := clientset.cabinet(id, &generation)
	return entry.content, err
}

func (clientset *Clientset) cabinet(id CabinetID, generation *ClientGeneration) (CredentialCandidate, error) {
	if clientset == nil {
		return CredentialCandidate{}, errors.New("WB clientset is required")
	}
	clientset.mutex.RLock()
	defer clientset.mutex.RUnlock()
	entry, exists := clientset.cabinets[id]
	if !exists {
		return CredentialCandidate{}, fmt.Errorf("%w: %q", ErrCabinetNotFound, id)
	}
	if generation != nil && (generation.IsZero() || entry.generation != *generation) {
		return CredentialCandidate{}, ErrClientGenerationChanged
	}
	return entry, nil
}

func (clientset *Clientset) CloseIdleConnections() {
	if clientset == nil || clientset.sharedTransport == nil {
		return
	}
	clientset.sharedTransport.CloseIdleConnections()
}
