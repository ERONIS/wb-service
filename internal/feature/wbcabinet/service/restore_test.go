package wbcabinet_service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"go.uber.org/zap"
)

const testSeller = "a723f78d-4d8f-45eb-a40b-3d46841d1688"

func testToken(expiry time.Time, suffix string) string {
	payload := fmt.Sprintf(`{"id":%q,"sid":%q,"s":2,"exp":%d}`, testSeller, testSeller, expiry.Unix())
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + suffix
}

type restoreRepository struct {
	Repository
	stored *StoredCabinet
	writes int
}

type restoreUnitOfWork struct {
	commitErr error
}

func (u restoreUnitOfWork) WithinTransaction(ctx context.Context, fn func(context.Context, core_postgres_transaction.DBTX) error) error {
	if err := fn(ctx, nil); err != nil {
		return err
	}
	return u.commitErr
}

func (r *restoreRepository) OperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}

func (r *restoreRepository) ListStored(context.Context) ([]StoredCabinet, error) {
	if r.stored == nil {
		return nil, nil
	}
	return []StoredCabinet{*r.stored}, nil
}
func (r *restoreRepository) GetByOwner(context.Context, int64, SellerID) (Cabinet, error) {
	if r.stored == nil {
		return Cabinet{}, core_errors.ErrNotFound
	}
	return r.stored.Cabinet, nil
}
func (r *restoreRepository) FindByOwnerAndName(context.Context, int64, string) (Cabinet, bool, error) {
	if r.stored == nil {
		return Cabinet{}, false, nil
	}
	return r.stored.Cabinet, true, nil
}
func (r *restoreRepository) UpsertVerified(_ context.Context, _ core_postgres_transaction.DBTX, credential VerifiedCredential) (Cabinet, error) {
	r.writes++
	r.stored.Token = credential.Token
	r.stored.ClientGeneration = credential.ClientGeneration
	r.stored.CredentialExpiresAt = credential.CredentialExpiresAt
	r.stored.Status = StatusActive
	return r.stored.Cabinet, nil
}
func (r *restoreRepository) MarkStatus(_ context.Context, _ SellerID, status Status) error {
	r.stored.Status = status
	return nil
}
func (r *restoreRepository) DeleteByOwner(context.Context, int64, SellerID) error {
	r.stored = nil
	return nil
}

type restoreVerifier struct{ failures, calls, removed, activated int }

func (v *restoreVerifier) Prepare(_ context.Context, _ SellerID, _ string, token string) (PreparedCredential, error) {
	v.calls++
	if v.failures > 0 {
		v.failures--
		return nil, ErrVerificationTransient
	}
	return &restorePrepared{generation: domain.NewClientGeneration(token), verifier: v}, nil
}
func (v *restoreVerifier) Remove(SellerID) { v.removed++ }

type restorePrepared struct {
	generation ClientGeneration
	verifier   *restoreVerifier
}

func (p *restorePrepared) Generation() ClientGeneration { return p.generation }
func (p *restorePrepared) Activate() error              { p.verifier.activated++; return nil }

func newRestoreTestService() (*Service, *restoreRepository, *restoreVerifier, time.Time) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	expiry := now.Add(time.Hour)
	token := testToken(expiry, "first")
	repository := &restoreRepository{stored: &StoredCabinet{Cabinet: Cabinet{ID: domain.SellerID(testSeller), Name: "Кабинет", OwnerTelegramID: 1, Status: StatusActive, ClientGeneration: domain.NewClientGeneration(token), CredentialExpiresAt: expiry}, Token: token}}
	verifier := &restoreVerifier{}
	service := New(repository, verifier, restoreUnitOfWork{})
	service.now = func() time.Time { return now }
	return service, repository, verifier, now
}

func TestAddActivatesOnlyAfterCommit(t *testing.T) {
	service, repository, verifier, now := newRestoreTestService()
	commitErr := errors.New("commit failed")
	service.uow = restoreUnitOfWork{commitErr: commitErr}

	_, err := service.Add(context.Background(), AddCommand{
		OwnerTelegramID: 1,
		Name:            repository.stored.Name,
		Token:           testToken(now.Add(time.Hour), "second"),
	})
	if !errors.Is(err, commitErr) {
		t.Fatalf("ошибка commit потеряна: %v", err)
	}
	if verifier.activated != 0 {
		t.Fatalf("клиент активирован до commit: %d", verifier.activated)
	}
}

func TestRestoreRetriesOnlyFailedCabinetsAndExpiresActive(t *testing.T) {
	service, repository, verifier, now := newRestoreTestService()
	verifier.failures = 1
	if err := service.Restore(context.Background()); err == nil {
		t.Fatal("ожидался временный сбой")
	}
	if len(service.restorePending) != 1 || repository.writes != 0 {
		t.Fatal("сбой не поставлен на повтор")
	}
	if err := service.retryTransientCabinets(context.Background(), zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if len(service.restorePending) != 0 || verifier.calls != 2 || verifier.activated != 1 {
		t.Fatal("кабинет не восстановлен")
	}
	verifier.failures = 1
	if err := service.retryTransientCabinets(context.Background(), zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if verifier.calls != 2 || repository.writes != 1 || verifier.removed != 1 {
		t.Fatal("работающий клиент повторно проверен или удалён")
	}
	service.now = func() time.Time { return now.Add(2 * time.Hour) }
	if err := service.retryTransientCabinets(context.Background(), zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if repository.stored.Status != StatusExpired || verifier.removed != 2 {
		t.Fatal("просроченный доступ не удалён")
	}
}

func TestRestoreCannotResurrectStaleOrDeletedCredentials(t *testing.T) {
	service, repository, verifier, now := newRestoreTestService()
	stale := *repository.stored
	_, err := service.Add(context.Background(), AddCommand{OwnerTelegramID: 1, Name: stale.Name, Token: testToken(now.Add(time.Hour), "second")})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.restoreCabinet(context.Background(), stale, false); err != nil {
		t.Fatal(err)
	}
	if verifier.calls != 1 || repository.stored.Token == stale.Token {
		t.Fatal("восстановлен старый токен")
	}
	if err := service.Delete(context.Background(), 1, stale.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.restoreCabinet(context.Background(), stale, false); err != nil {
		t.Fatal(err)
	}
	if verifier.calls != 1 || repository.stored != nil {
		t.Fatal("восстановлен удалённый кабинет")
	}
}
