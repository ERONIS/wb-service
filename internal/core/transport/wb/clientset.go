package wb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/transport/wb/credentials"
	"go.uber.org/zap"

	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	flowcontrol "github.com/ERONIS/wb-service/internal/core/transport/wb/flowcontrol"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	transport "github.com/ERONIS/wb-service/internal/core/transport/wb/transport"
)

const defaultClientTimeout = 45 * time.Second

type CredentialCandidate struct {
	ID         domain.SellerID
	Name       string
	generation domain.ClientGeneration
	executor   *client.APIClient
}

type Clientset struct {
	mu              sync.RWMutex
	cabinets        map[domain.SellerID]CredentialCandidate
	httpClient      *http.Client
	rateLimiters    *flowcontrol.Registry
	logger          *zap.Logger
	sharedTransport *transport.SharedTransport
}

// NewClientset создаёт реестр клиентов WB с политиками ограничений запросов.
func NewClientset(
	ctx context.Context,
	logger *zap.Logger,
	bucketCatalog policy.BucketCatalog,
) (*Clientset, error) {
	if ctx == nil {
		return nil, fmt.Errorf("WB startup context is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("WB logger is required")
	}
	rateLimiters := flowcontrol.NewRegistry(bucketCatalog)
	sharedTransport, err := transport.NewSharedTransport()
	if err != nil {
		return nil, fmt.Errorf("create WB shared HTTP transport: %w", err)
	}
	httpClient, err := sharedTransport.NewHTTPClient(defaultClientTimeout)
	if err != nil {
		return nil, fmt.Errorf("create WB HTTP client: %w", err)
	}
	return &Clientset{
		cabinets:        make(map[domain.SellerID]CredentialCandidate),
		httpClient:      httpClient,
		rateLimiters:    rateLimiters,
		logger:          logger,
		sharedTransport: sharedTransport,
	}, nil
}

// NewCandidate готовит клиента кабинета без добавления в реестр.
func (clientset *Clientset) NewCandidate(
	id domain.SellerID,
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
	candidate := &CredentialCandidate{
		ID:   id,
		Name: name,
	}
	executor, err := client.NewAPIClient(
		id,
		name,
		token,
		clientset.httpClient,
		clientset.rateLimiters,
		clientset.logger,
	)
	if err != nil {
		return nil, fmt.Errorf("create WB API client for cabinet %q: %w", id, err)
	}
	candidate.executor = executor
	return candidate, nil
}

// ActivateCandidate добавляет подготовленный кабинет в реестр.
func (clientset *Clientset) ActivateCandidate(
	candidate *CredentialCandidate,
	generation domain.ClientGeneration,
) error {
	if clientset == nil {
		return errors.New("WB clientset is required")
	}
	if candidate == nil || candidate.ID == "" || candidate.Name == "" ||
		generation.IsZero() || candidate.executor == nil {
		return errors.New("WB credential candidate is invalid")
	}
	active := *candidate
	active.generation = generation
	clientset.mu.Lock()
	defer clientset.mu.Unlock()
	if current, exists := clientset.cabinets[candidate.ID]; exists && current.generation > generation {
		return ErrClientGenerationChanged
	}
	clientset.cabinets[candidate.ID] = active
	return nil
}

// RemoveCabinet удаляет кабинет из реестра.
func (clientset *Clientset) RemoveCabinet(id domain.SellerID) {
	if clientset == nil {
		return
	}
	clientset.mu.Lock()
	defer clientset.mu.Unlock()
	delete(clientset.cabinets, id)
}

// Cabinets возвращает активные кабинеты, отсортированные по ID.
func (clientset *Clientset) Cabinets() []CredentialCandidate {
	if clientset == nil {
		return nil
	}
	clientset.mu.RLock()
	result := make([]CredentialCandidate, 0, len(clientset.cabinets))
	for _, cabinet := range clientset.cabinets {
		result = append(result, cabinet)
	}
	clientset.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// ExecutorForCabinet находит клиент кабинета по его ID.
func (clientset *Clientset) ExecutorForCabinet(id domain.SellerID) (*client.APIClient, error) {
	entry, err := clientset.cabinet(id, nil)
	return entry.executor, err
}

// PinnedExecutor возвращает клиент только при совпадении поколения.
func (clientset *Clientset) PinnedExecutor(id domain.SellerID, generation domain.ClientGeneration) (*client.APIClient, error) {
	entry, err := clientset.cabinet(id, &generation)
	return entry.executor, err
}

// CloseIdleConnections закрывает простаивающие соединения общего транспорта.
func (clientset *Clientset) CloseIdleConnections() {
	if clientset == nil || clientset.sharedTransport == nil {
		return
	}
	clientset.sharedTransport.CloseIdleConnections()
}

// Executor возвращает клиент подготовленного кандидата.
func (candidate *CredentialCandidate) Executor() *client.APIClient {
	if candidate == nil {
		return nil
	}
	return candidate.executor
}

// cabinet ищет кабинет и при необходимости проверяет поколение.
func (clientset *Clientset) cabinet(
	id domain.SellerID,
	generation *domain.ClientGeneration) (
	CredentialCandidate, error) {
	if clientset == nil {
		return CredentialCandidate{}, errors.New("WB clientset is required")
	}
	clientset.mu.RLock()
	defer clientset.mu.RUnlock()
	entry, exists := clientset.cabinets[id]
	if !exists {
		return CredentialCandidate{}, fmt.Errorf("%w: %q", ErrCabinetNotFound, id)
	}
	if generation != nil && (generation.IsZero() || entry.generation != *generation) {
		return CredentialCandidate{}, ErrClientGenerationChanged
	}
	return entry, nil
}

// validateCandidate проверяет ID, имя и форму токена кандидата.
func validateCandidate(id domain.SellerID, name string, token string) error {
	if err := id.Validate(); err != nil {
		return err
	}
	normalizedName := strings.TrimSpace(name)
	if normalizedName == "" {
		return fmt.Errorf("validate cabinet %q name: cabinet name is empty", id)
	}
	if err := credentials.ValidateTokenShape(token); err != nil {
		return fmt.Errorf("validate cabinet %q token: %w", id, err)
	}
	return nil
}
