package wb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	generalapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/general/v1"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	identity "github.com/ERONIS/wb-service/internal/core/transport/wb/identity"
)

type SellerKey = identity.SellerKey
type ClientGeneration = identity.ClientGeneration
type CredentialIdentity = identity.Binding

type credentialVerifier struct {
	clientset *Clientset
}

func (verifier *credentialVerifier) Verify(
	ctx context.Context,
	cabinetID config.CabinetID,
	generation identity.ClientGeneration,
) (identity.RemoteIdentity, error) {
	if verifier == nil || verifier.clientset == nil {
		return identity.RemoteIdentity{}, errors.New("WB credential verifier is required")
	}
	if ctx == nil {
		return identity.RemoteIdentity{}, errors.New("verify WB credential: context is nil")
	}

	contentExecutor, err := verifier.clientset.PinnedExecutor(cabinetID, generation)
	if err != nil {
		return identity.RemoteIdentity{}, fmt.Errorf("pin Content API executor: %w", err)
	}
	ping, err := client.ExecuteResponse[contentapi.PingResponse](
		ctx,
		contentExecutor,
		contentapi.PingOperation(),
		nil,
		nil,
	)
	if err != nil {
		return identity.RemoteIdentity{}, fmt.Errorf("check Content API token: %w", err)
	}
	if strings.TrimSpace(ping.Status) != "OK" {
		return identity.RemoteIdentity{}, errors.New("Content API ping returned invalid status")
	}
	if _, err := time.Parse(time.RFC3339, ping.Timestamp); err != nil {
		return identity.RemoteIdentity{}, errors.New("Content API ping returned invalid timestamp")
	}

	commonExecutor, err := verifier.clientset.pinnedCommonExecutor(cabinetID, generation)
	if err != nil {
		return identity.RemoteIdentity{}, fmt.Errorf("pin General API executor: %w", err)
	}
	seller, err := client.ExecuteResponse[generalapi.SellerInfoResponse](
		ctx,
		commonExecutor,
		generalapi.SellerInfoOperation(),
		nil,
		nil,
	)
	if err != nil {
		return identity.RemoteIdentity{}, fmt.Errorf("get WB seller info: %w", err)
	}
	return identity.RemoteIdentity{SellerID: seller.SellerID}, nil
}

// CredentialSnapshot returns the immutable, remotely verified identity
// snapshot created during Clientset initialization.
func (clientset *Clientset) CredentialSnapshot() ([]CredentialIdentity, error) {
	if clientset == nil {
		return nil, errors.New("get WB credential snapshot: clientset is nil")
	}
	if clientset.identityRegistry == nil {
		return nil, errors.New("get WB credential snapshot: identity registry is unavailable")
	}
	return clientset.identityRegistry.Snapshot()
}

func (clientset *Clientset) PinnedExecutor(
	id config.CabinetID,
	generation ClientGeneration,
) (client.Executor, error) {
	if clientset == nil {
		return nil, errors.New("pin WB executor: clientset is nil")
	}
	if err := clientset.validateGeneration(id, generation); err != nil {
		return nil, err
	}
	return clientset.ForCabinet(id)
}

func (clientset *Clientset) pinnedCommonExecutor(
	id config.CabinetID,
	generation ClientGeneration,
) (client.Executor, error) {
	if clientset == nil {
		return nil, errors.New("pin WB General API executor: clientset is nil")
	}
	if err := clientset.validateGeneration(id, generation); err != nil {
		return nil, err
	}
	executor, exists := clientset.commonExecutors[id]
	if !exists {
		return nil, fmt.Errorf("%w: %q", ErrCabinetNotFound, id)
	}
	return executor, nil
}

func (clientset *Clientset) validateGeneration(
	id config.CabinetID,
	generation ClientGeneration,
) error {
	token, exists := clientset.credentialTokens[id]
	if !exists {
		return fmt.Errorf("%w: %q", ErrCabinetNotFound, id)
	}
	if identity.Generation(token) != generation {
		return errors.New("pin WB executor: client generation changed")
	}
	return nil
}

var _ identity.Verifier = (*credentialVerifier)(nil)
