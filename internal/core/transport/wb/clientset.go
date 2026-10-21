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

type SellerID = domain.SellerID
type ClientGeneration = domain.ClientGeneration

type CredentialCandidate struct {
	ID         SellerID
	Name       string
	generation ClientGeneration
	executor   client.Executor
}

type Clientset struct {
	mu              sync.RWMutex
	cabinets        map[SellerID]CredentialCandidate
	baseHTTPClient  *http.Client
	rateLimiters    *flowcontrol.Registry
	logger          *zap.Logger
	sharedTransport *transport.SharedTransport
}

func (candidate *CredentialCandidate) Generation() ClientGeneration {
	if candidate == nil {
		return ClientGeneration{}
	}
	return candidate.generation
}

func (candidate *CredentialCandidate) Executor() client.Executor {
	if candidate == nil {
		return nil
	}
	return candidate.executor
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
	return &Clientset{
		cabinets:        make(map[SellerID]CredentialCandidate),
		baseHTTPClient:  &http.Client{Timeout: defaultClientTimeout},
		rateLimiters:    rateLimiters,
		logger:          logger,
		sharedTransport: sharedTransport,
	}, nil
}

func (clientset *Clientset) NewCandidate(
	id SellerID,
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
	httpClient, err := clientset.sharedTransport.NewHTTPClient(clientset.baseHTTPClient)
	if err != nil {
		return nil, fmt.Errorf("create WB HTTP client for cabinet %q: %w", id, err)
	}
	candidate := &CredentialCandidate{
		ID:         id,
		Name:       name,
		generation: domain.NewClientGeneration(token),
	}
	candidate.executor, err = client.NewAPIClient(
		id,
		name,
		token,
		httpClient,
		clientset.rateLimiters,
		clientset.logger,
	)
	if err != nil {
		return nil, fmt.Errorf("create WB API client for cabinet %q: %w", id, err)
	}
	return candidate, nil
}

func validateCandidate(id SellerID, name string, token string) error {
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

func (clientset *Clientset) ActivateCandidate(candidate *CredentialCandidate) error {
	if clientset == nil {
		return errors.New("WB clientset is required")
	}
	if candidate == nil || candidate.ID == "" || candidate.Name == "" ||
		candidate.generation.IsZero() || candidate.executor == nil {
		return errors.New("WB credential candidate is invalid")
	}
	clientset.mu.Lock()
	defer clientset.mu.Unlock()
	clientset.cabinets[candidate.ID] = *candidate
	return nil
}

func (clientset *Clientset) RemoveCabinet(id SellerID) {
	if clientset == nil {
		return
	}
	clientset.mu.Lock()
	defer clientset.mu.Unlock()
	delete(clientset.cabinets, id)
}

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

func (clientset *Clientset) ExecutorForCabinet(id SellerID) (client.Executor, error) {
	entry, err := clientset.cabinet(id, nil)
	return entry.executor, err
}

func (clientset *Clientset) PinnedExecutor(id SellerID, generation ClientGeneration) (client.Executor, error) {
	entry, err := clientset.cabinet(id, &generation)
	return entry.executor, err
}

func (clientset *Clientset) cabinet(id SellerID, generation *ClientGeneration) (CredentialCandidate, error) {
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

func (clientset *Clientset) CloseIdleConnections() {
	if clientset == nil || clientset.sharedTransport == nil {
		return
	}
	clientset.sharedTransport.CloseIdleConnections()
}
