package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Registry owns the one-time verification and immutable credential snapshot
// shared by every WB feature.
type Registry struct {
	verifier Verifier
	store    Store

	mutex       sync.RWMutex
	initialized bool
	bindings    []Binding
}

func NewRegistry(verifier Verifier, store Store) (*Registry, error) {
	if verifier == nil {
		return nil, errors.New("WB identity verifier is required")
	}
	if store == nil {
		return nil, errors.New("WB identity store is required")
	}
	return &Registry{verifier: verifier, store: store}, nil
}

func (registry *Registry) VerifyAtStartup(
	ctx context.Context,
	now time.Time,
	credentials []Credential,
) error {
	if registry == nil {
		return errors.New("WB identity registry is required")
	}
	if ctx == nil {
		return errors.New("verify WB identities: context is nil")
	}
	if now.IsZero() {
		return errors.New("verify WB identities: current time is empty")
	}
	if len(credentials) == 0 {
		return errors.New("verify WB identities: credential list is empty")
	}

	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.initialized {
		return errors.New("verify WB identities: registry is already initialized")
	}

	verified := make([]VerifiedCredential, 0, len(credentials))
	seenCabinets := make(map[string]struct{}, len(credentials))
	seenSellers := make(map[SellerKey]struct{}, len(credentials))
	for index, credential := range credentials {
		cabinetID := string(credential.CabinetID)
		if cabinetID == "" || strings.TrimSpace(cabinetID) != cabinetID || len(cabinetID) > 128 {
			return fmt.Errorf("credential at position %d has invalid cabinet ID", index+1)
		}
		if _, exists := seenCabinets[cabinetID]; exists {
			return fmt.Errorf("cabinet %q is duplicated", credential.CabinetID)
		}
		seenCabinets[cabinetID] = struct{}{}

		claims, err := decodeCredentialToken(credential.Token, now.UTC())
		if err != nil {
			return fmt.Errorf("decode cabinet %q token: %w", credential.CabinetID, err)
		}
		contentRead := claims.Properties&contentCapabilityBit != 0
		if !contentRead {
			return fmt.Errorf("cabinet %q token has no Content capability", credential.CabinetID)
		}
		generation := Generation(credential.Token)
		remote, err := registry.verifier.Verify(ctx, credential.CabinetID, generation)
		if err != nil {
			return fmt.Errorf("verify cabinet %q with WB: %w", credential.CabinetID, err)
		}
		remoteSellerID, err := canonicalSellerID(remote.SellerID)
		if err != nil {
			return fmt.Errorf("verify cabinet %q seller identity: %w", credential.CabinetID, err)
		}
		if claims.SellerID != remoteSellerID {
			return fmt.Errorf("cabinet %q token seller differs from WB seller: %w", credential.CabinetID, ErrIdentityMismatch)
		}

		key := sellerKey(remoteSellerID)
		if _, exists := seenSellers[key]; exists {
			return fmt.Errorf("cabinet %q: %w", credential.CabinetID, ErrDuplicateSeller)
		}
		seenSellers[key] = struct{}{}
		verified = append(verified, VerifiedCredential{
			CabinetID:           credential.CabinetID,
			SellerKey:           key,
			ContentRead:         true,
			ContentWrite:        claims.Properties&readOnlyBit == 0,
			CredentialExpiresAt: claims.ExpiresAt,
			ClientGeneration:    generation,
		})
	}

	bindings, err := registry.store.SyncBindings(ctx, now.UTC(), verified)
	if err != nil {
		return fmt.Errorf("sync WB identity bindings: %w", err)
	}
	if err := validateBindings(bindings, verified, now.UTC()); err != nil {
		return err
	}

	registry.bindings = append([]Binding(nil), bindings...)
	registry.initialized = true
	return nil
}

func (registry *Registry) Snapshot() ([]Binding, error) {
	if registry == nil {
		return nil, errors.New("get WB identity snapshot: registry is nil")
	}
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	if !registry.initialized {
		return nil, errors.New("get WB identity snapshot: registry is not initialized")
	}
	return append([]Binding(nil), registry.bindings...), nil
}

func validateBindings(
	bindings []Binding,
	verified []VerifiedCredential,
	now time.Time,
) error {
	if len(bindings) != len(verified) {
		return fmt.Errorf("WB identity binding count %d differs from verified count %d", len(bindings), len(verified))
	}
	for index, binding := range bindings {
		credential := verified[index]
		switch {
		case binding.CabinetID != credential.CabinetID,
			binding.SellerKey != credential.SellerKey,
			binding.ClientGeneration != credential.ClientGeneration:
			return fmt.Errorf("WB identity binding at position %d differs from verified credential", index+1)
		case binding.BindingRevision <= 0:
			return fmt.Errorf("WB identity binding at position %d has invalid binding revision", index+1)
		case binding.CapabilityRevision <= 0:
			return fmt.Errorf("WB identity binding at position %d has invalid capability revision", index+1)
		case binding.ContentRead != credential.ContentRead,
			binding.ContentWrite != credential.ContentWrite:
			return fmt.Errorf("WB identity binding at position %d has stale capabilities", index+1)
		case binding.CredentialExpiresAt.IsZero(),
			!binding.CredentialExpiresAt.After(now),
			!binding.CredentialExpiresAt.Equal(credential.CredentialExpiresAt):
			return fmt.Errorf("WB identity binding at position %d has invalid credential expiry", index+1)
		}
	}
	return nil
}
