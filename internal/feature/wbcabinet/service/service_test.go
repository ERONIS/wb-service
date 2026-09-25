package wbcabinet_service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	"go.uber.org/zap"
)

func nopLogger() *zap.Logger { return zap.NewNop() }

const testSellerID = "11111111-2222-4333-8444-555555555555"

func TestAddAndRotateCredential(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	verifier := &verifierStub{}
	service := New(repository, verifier)
	service.now = func() time.Time { return now }

	properties := PermissionContent | PermissionAnalytics | PermissionPrices
	firstToken := testToken(t, testSellerID, properties, now.Add(time.Hour))
	first, err := service.Add(context.Background(), AddCommand{
		OwnerTelegramID: 42,
		Name:            "  Основной  ",
		Token:           firstToken,
	})
	if err != nil {
		t.Fatalf("Add(first) error = %v", err)
	}
	if first.ID == "" || first.Name != "Основной" || first.Status != StatusActive {
		t.Fatalf("Add(first) = %+v", first)
	}
	if first.TokenProperties != properties {
		t.Fatalf("TokenProperties = %d, want %d", first.TokenProperties, properties)
	}
	if repository.stored.Token != firstToken {
		t.Fatal("stored credential differs from supplied token")
	}

	secondToken := testToken(t, testSellerID, properties, now.Add(2*time.Hour))
	second, err := service.Add(context.Background(), AddCommand{
		OwnerTelegramID: 42,
		Name:            "основной",
		Token:           secondToken,
	})
	if err != nil {
		t.Fatalf("Add(rotation) error = %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("rotation changed cabinet ID: %q != %q", second.ID, first.ID)
	}
	if second.ClientGeneration == first.ClientGeneration {
		t.Fatal("rotation did not change client generation")
	}
	if repository.stored.Token != secondToken {
		t.Fatal("rotation did not replace the stored token")
	}
	if verifier.activations != 2 {
		t.Fatalf("activation count = %d, want 2", verifier.activations)
	}
}

func TestRestoreStoredToken(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service := New(repository, &verifierStub{})
	service.now = func() time.Time { return now }
	token := testToken(t, testSellerID, PermissionContent, now.Add(time.Hour))
	cabinet, err := service.Add(context.Background(), AddCommand{
		OwnerTelegramID: 42,
		Name:            "Основной",
		Token:           token,
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	verifier := &verifierStub{}
	restarted := New(repository, verifier)
	restarted.now = service.now
	if err := restarted.Restore(context.Background()); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if verifier.activations != 1 || repository.stored.ID != cabinet.ID || repository.stored.Token != token {
		t.Fatal("Restore() did not reactivate the stored credential")
	}

	// An invalid stored credential must not be activated.
	repository.stored.Token = ""
	repository.stored.Status = StatusVerificationFailed
	if err := restarted.Restore(context.Background()); err == nil {
		t.Fatal("Restore() accepted an empty token")
	}
	if verifier.activations != 1 || len(verifier.removals) != 1 || repository.stored.Status != StatusVerificationFailed {
		t.Fatal("Restore() did not leave the cabinet inactive")
	}
	updated, err := restarted.Add(context.Background(), AddCommand{
		OwnerTelegramID: 42,
		Name:            "Основной",
		Token:           token,
	})
	if err != nil {
		t.Fatalf("Add(re-entered token) error = %v", err)
	}
	if updated.ID != cabinet.ID || updated.Status != StatusActive || verifier.activations != 2 {
		t.Fatal("re-entering the token did not reactivate the same cabinet")
	}
}

func TestRestorePreservesActiveStatusOnTransientVerificationError(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service := New(repository, &verifierStub{})
	service.now = func() time.Time { return now }
	token := testToken(t, testSellerID, PermissionContent, now.Add(time.Hour))
	cabinet, err := service.Add(context.Background(), AddCommand{
		OwnerTelegramID: 42,
		Name:            "Основной",
		Token:           token,
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	verifier := &verifierStub{
		prepareErr: ErrVerificationTransient,
	}
	restarted := New(repository, verifier)
	restarted.now = service.now
	err = restarted.Restore(context.Background())
	if err == nil {
		t.Fatal("Restore() expected transient error, got nil")
	}
	if verifier.activations != 0 {
		t.Fatalf("verifier activations = %d, want 0", verifier.activations)
	}
	if len(verifier.removals) != 1 || verifier.removals[0] != cabinet.ID {
		t.Fatalf("verifier removals = %+v, want [%s]", verifier.removals, cabinet.ID)
	}
	if repository.stored.Status != StatusActive {
		t.Fatalf("cabinet status was changed to %v, want StatusActive", repository.stored.Status)
	}
}

func TestAddRejectsInsufficientContentAccessBeforeRemoteVerification(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	verifier := &verifierStub{}
	service := New(repository, verifier)
	service.now = func() time.Time { return now }

	readOnly := PermissionContent | PermissionReadOnly
	_, err := service.Add(context.Background(), AddCommand{
		OwnerTelegramID: 42,
		Name:            "Основной",
		Token:           testToken(t, testSellerID, readOnly, now.Add(time.Hour)),
	})
	if !errors.Is(err, ErrContentAccess) {
		t.Fatalf("Add() error = %v, want ErrContentAccess", err)
	}
	if verifier.preparations != 0 {
		t.Fatalf("remote verification calls = %d, want 0", verifier.preparations)
	}
}

func TestAddRejectsSellerChangeDuringTokenRotation(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	verifier := &verifierStub{}
	service := New(repository, verifier)
	service.now = func() time.Time { return now }

	first, err := service.Add(context.Background(), AddCommand{
		OwnerTelegramID: 42,
		Name:            "Основной",
		Token:           testToken(t, testSellerID, PermissionContent, now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Add(first) error = %v", err)
	}
	_, err = service.Add(context.Background(), AddCommand{
		OwnerTelegramID: 42,
		Name:            "Основной",
		Token: testToken(
			t,
			"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
			PermissionContent,
			now.Add(time.Hour),
		),
	})
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("Add() error = %v, want ErrIdentityMismatch", err)
	}
	if verifier.activations != 1 || !repository.found || repository.stored.ID != first.ID ||
		repository.stored.SellerKey != sellerKey(testSellerID) {
		t.Fatal("seller-changing credential replaced the existing cabinet")
	}
}

func TestAddPreservesRemoteVerificationFailureWithoutClaimingForbidden(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	verifier := &verifierStub{prepareErr: context.DeadlineExceeded}
	service := New(repository, verifier)
	service.now = func() time.Time { return now }

	_, err := service.Add(context.Background(), AddCommand{
		OwnerTelegramID: 42,
		Name:            "Основной",
		Token:           testToken(t, testSellerID, PermissionContent, now.Add(time.Hour)),
	})
	if !errors.Is(err, ErrVerification) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Add() error = %v, want verification deadline error", err)
	}
	if errors.Is(err, core_errors.ErrForbidden) {
		t.Fatalf("Add() transport error was incorrectly classified as forbidden: %v", err)
	}
	if repository.found {
		t.Fatal("failed verification was persisted")
	}
}

func TestDeleteOnlyRemovesOwnedCabinet(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	verifier := &verifierStub{}
	service := New(repository, verifier)
	service.now = func() time.Time { return now }

	cabinet, err := service.Add(context.Background(), AddCommand{
		OwnerTelegramID: 42,
		Name:            "Основной",
		Token:           testToken(t, testSellerID, PermissionContent, now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := service.Delete(context.Background(), 43, cabinet.ID); !errors.Is(err, core_errors.ErrNotFound) {
		t.Fatalf("Delete(other owner) error = %v, want ErrNotFound", err)
	}
	if !repository.found || len(verifier.removals) != 0 {
		t.Fatal("Delete(other owner) changed stored or runtime cabinet")
	}
	if err := service.Delete(context.Background(), 42, cabinet.ID); err != nil {
		t.Fatalf("Delete(owner) error = %v", err)
	}
	if repository.found {
		t.Fatal("Delete(owner) left stored cabinet")
	}
	if len(verifier.removals) != 1 || verifier.removals[0] != cabinet.ID {
		t.Fatalf("runtime removals = %v, want %q", verifier.removals, cabinet.ID)
	}
}

func TestTargetsByOwnerRoleDelegatesValidatedRole(t *testing.T) {
	repository := newMemoryRepository()
	repository.roleTargets = []TargetCredential{{CabinetID: "partner-cabinet"}}
	service := New(repository, &verifierStub{})

	targets, err := service.TargetsByOwnerRole(context.Background(), domain.RolePartner)
	if err != nil {
		t.Fatalf("TargetsByOwnerRole() error = %v", err)
	}
	if repository.targetRole != domain.RolePartner || len(targets) != 1 ||
		targets[0].CabinetID != "partner-cabinet" {
		t.Fatalf("TargetsByOwnerRole() = %+v, role = %q", targets, repository.targetRole)
	}
	if _, err := service.TargetsByOwnerRole(context.Background(), domain.UserRole("unknown")); !errors.Is(err, core_errors.ErrInvalidArgument) {
		t.Fatalf("TargetsByOwnerRole(invalid) error = %v", err)
	}
}

func TestDecodeCredentialTokenRejectsExpiredAndMalformedClaims(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	if _, err := decodeCredentialToken(
		testToken(t, testSellerID, PermissionContent, now),
		now,
	); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("decodeCredentialToken(expired) error = %v", err)
	}
	if _, err := decodeCredentialToken("not-a-jwt", now); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("decodeCredentialToken(malformed) error = %v", err)
	}
}

func testToken(t *testing.T, sellerID string, properties TokenProperties, expiresAt time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"id":  "99999999-aaaa-4bbb-8ccc-dddddddddddd",
		"sid": sellerID,
		"s":   properties,
		"exp": expiresAt.Unix(),
	})
	if err != nil {
		t.Fatalf("marshal token payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

type verifierStub struct {
	prepareErr   error
	preparations int
	activations  int
	removals     []CabinetID
}

func (verifier *verifierStub) Prepare(
	_ context.Context,
	_ CabinetID,
	_ string,
	token string,
) (PreparedCredential, error) {
	verifier.preparations++
	if verifier.prepareErr != nil {
		return nil, verifier.prepareErr
	}
	return &preparedStub{
		generation: credentialGeneration(token),
		activate:   func() { verifier.activations++ },
	}, nil
}

func (verifier *verifierStub) Remove(cabinetID CabinetID) {
	verifier.removals = append(verifier.removals, cabinetID)
}

type preparedStub struct {
	generation ClientGeneration
	activate   func()
}

func (prepared *preparedStub) Generation() ClientGeneration { return prepared.generation }
func (prepared *preparedStub) Activate() error {
	prepared.activate()
	return nil
}

type memoryRepository struct {
	found       bool
	stored      StoredCabinet
	targetRole  domain.UserRole
	roleTargets []TargetCredential
}

func newMemoryRepository() *memoryRepository { return &memoryRepository{} }

func (repository *memoryRepository) FindByOwnerAndName(
	_ context.Context,
	owner int64,
	name string,
) (Cabinet, bool, error) {
	if !repository.found || repository.stored.OwnerTelegramID != owner ||
		!strings.EqualFold(strings.TrimSpace(repository.stored.Name), strings.TrimSpace(name)) {
		return Cabinet{}, false, nil
	}
	return repository.stored.Cabinet, true, nil
}

func (repository *memoryRepository) UpsertVerified(
	_ context.Context,
	credential VerifiedCredential,
) (Cabinet, error) {
	bindingRevision := int64(1)
	capabilityRevision := int64(1)
	createdAt := credential.VerifiedAt
	if repository.found {
		bindingRevision = repository.stored.BindingRevision
		capabilityRevision = repository.stored.CapabilityRevision
		createdAt = repository.stored.CreatedAt
		if repository.stored.ContentRead != credential.ContentRead ||
			repository.stored.ContentWrite != credential.ContentWrite ||
			repository.stored.TokenProperties != credential.TokenProperties {
			capabilityRevision++
		}
	}
	cabinet := Cabinet{
		ID:                  credential.CabinetID,
		OwnerTelegramID:     credential.OwnerTelegramID,
		Name:                credential.Name,
		SellerKey:           credential.SellerKey,
		BindingRevision:     bindingRevision,
		CapabilityRevision:  capabilityRevision,
		ContentRead:         credential.ContentRead,
		ContentWrite:        credential.ContentWrite,
		TokenProperties:     credential.TokenProperties,
		CredentialExpiresAt: credential.CredentialExpiresAt,
		ClientGeneration:    credential.ClientGeneration,
		VerifiedAt:          credential.VerifiedAt,
		Status:              StatusActive,
		CreatedAt:           createdAt,
		UpdatedAt:           credential.VerifiedAt,
	}
	repository.found = true
	repository.stored = StoredCabinet{
		Cabinet: cabinet,
		Token:   credential.Token,
	}
	return cabinet, nil
}

func (repository *memoryRepository) ListByOwner(_ context.Context, owner int64) ([]Cabinet, error) {
	if !repository.found || repository.stored.OwnerTelegramID != owner {
		return nil, nil
	}
	return []Cabinet{repository.stored.Cabinet}, nil
}

func (repository *memoryRepository) GetByOwner(
	_ context.Context,
	owner int64,
	cabinetID CabinetID,
) (Cabinet, error) {
	if !repository.found || repository.stored.OwnerTelegramID != owner ||
		repository.stored.ID != cabinetID {
		return Cabinet{}, core_errors.ErrNotFound
	}
	return repository.stored.Cabinet, nil
}

func (repository *memoryRepository) ListStored(context.Context) ([]StoredCabinet, error) {
	if !repository.found {
		return nil, nil
	}
	return []StoredCabinet{repository.stored}, nil
}

func (repository *memoryRepository) ListTargets(context.Context, int64) ([]TargetCredential, error) {
	return nil, nil
}

func (repository *memoryRepository) ListTargetsByOwnerRole(
	_ context.Context,
	role domain.UserRole,
) ([]TargetCredential, error) {
	repository.targetRole = role
	return append([]TargetCredential(nil), repository.roleTargets...), nil
}

func (repository *memoryRepository) ListAllTargets(context.Context) ([]TargetCredential, error) {
	return nil, nil
}

func (repository *memoryRepository) MarkStatus(_ context.Context, _ CabinetID, status Status) error {
	repository.stored.Status = status
	return nil
}

func (repository *memoryRepository) DeleteByOwner(
	_ context.Context,
	owner int64,
	cabinetID CabinetID,
) error {
	if !repository.found || repository.stored.OwnerTelegramID != owner ||
		repository.stored.ID != cabinetID {
		return core_errors.ErrNotFound
	}
	repository.found = false
	repository.stored = StoredCabinet{}
	return nil
}

var _ Repository = (*memoryRepository)(nil)
var _ Verifier = (*verifierStub)(nil)

// TestRunRestoreRetryLoadsTransientCabinet verifies that a cabinet that failed
// with a transient error during Restore is successfully loaded by the next
// RunRestoreRetry tick.
func TestRunRestoreRetryLoadsTransientCabinet(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	verifier := &verifierStub{}
	service := New(repository, verifier)
	service.now = func() time.Time { return now }

	// Pre-populate repository with a stored cabinet (simulating a previous
	// successful Add that is already in the DB but not in the runtime verifier).
	token := testToken(t, testSellerID, PermissionContent, now.Add(time.Hour))
	cabinetID := CabinetID("test-cabinet-id")
	repository.found = true
	repository.stored = StoredCabinet{
		Cabinet: Cabinet{
			ID:                  cabinetID,
			OwnerTelegramID:     42,
			Name:                "Основной",
			SellerKey:           sellerKey(testSellerID),
			BindingRevision:     1,
			CapabilityRevision:  1,
			ContentRead:         true,
			ContentWrite:        true,
			TokenProperties:     PermissionContent,
			CredentialExpiresAt: now.Add(time.Hour),
			ClientGeneration:    credentialGeneration(token),
			VerifiedAt:          now,
			Status:              StatusActive,
			CreatedAt:           now,
			UpdatedAt:           now,
		},
		Token: token,
	}

	// First attempt returns a transient error.
	verifier.prepareErr = ErrVerificationTransient
	if err := service.Restore(context.Background()); err == nil {
		t.Fatal("Restore() expected transient error, got nil")
	}
	if len(verifier.removals) != 1 {
		t.Fatalf("Restore() expected 1 removal, got %d", len(verifier.removals))
	}

	// Clear the transient error — cabinet is now reachable.
	verifier.prepareErr = nil
	verifier.removals = nil

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		service.RunRestoreRetry(ctx, 10*time.Millisecond, nopLogger())
	}()

	// Wait for at least one successful activation.
	deadline := time.After(2 * time.Second)
	for {
		if verifier.activations >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for RunRestoreRetry to load the cabinet")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
}
