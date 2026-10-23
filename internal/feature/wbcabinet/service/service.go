package wbcabinet_service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	credentials "github.com/ERONIS/wb-service/internal/core/transport/wb/credentials"
	"go.uber.org/zap"
)

type Service struct {
	repository     Repository
	verifier       Verifier
	uow            core_postgres_transaction.UnitOfWork
	now            func() time.Time
	mutationMu     sync.Mutex
	restorePending map[SellerID]ClientGeneration
}

func New(
	repository Repository,
	verifier Verifier,
	uow core_postgres_transaction.UnitOfWork,
) *Service {
	if repository == nil || verifier == nil || uow == nil {
		panic("wbcabinet service dependency is nil")
	}
	return &Service{
		repository:     repository,
		verifier:       verifier,
		uow:            uow,
		now:            time.Now,
		restorePending: make(map[SellerID]ClientGeneration),
	}
}

func (service *Service) Add(ctx context.Context, command AddCommand) (Cabinet, error) {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	return service.add(ctx, command)
}

// add выполняется под mutationMu, чтобы восстановление не перезаписало новый токен.
func (service *Service) add(ctx context.Context, command AddCommand) (Cabinet, error) {
	if err := core_errors.RequireContext(ctx); err != nil {
		return Cabinet{}, err
	}
	command.Name = strings.TrimSpace(command.Name)
	command.Token = strings.TrimSpace(command.Token)
	if err := validateAddCommand(command); err != nil {
		return Cabinet{}, err
	}

	existing, found, err := service.repository.FindByOwnerAndName(
		ctx,
		command.OwnerTelegramID,
		command.Name,
	)
	if err != nil {
		return Cabinet{}, fmt.Errorf("find WB cabinet before verification: %w", err)
	}
	now := service.now().UTC()
	claims, err := credentials.ParseToken(command.Token, now)
	if err != nil {
		return Cabinet{}, err
	}
	sellerID := domain.SellerID(claims.SellerID)
	if command.SellerID != "" && command.SellerID != sellerID {
		return Cabinet{}, ErrIdentityMismatch
	}
	if found && existing.ID != sellerID {
		return Cabinet{}, ErrIdentityMismatch
	}
	contentRead := claims.Properties.Has(credentials.PermissionContent)
	contentWrite := !claims.Properties.Has(credentials.PermissionReadOnly)
	if !contentRead || !contentWrite {
		return Cabinet{}, ErrContentAccess
	}

	prepared, err := service.verifier.Prepare(ctx, sellerID, command.Name, command.Token)
	if err != nil {
		return Cabinet{}, fmt.Errorf("prepare and verify WB credential: %w", err)
	}
	generation := domain.NewClientGeneration(command.Token)
	if generation != prepared.Generation() {
		return Cabinet{}, fmt.Errorf("WB credential generation differs from prepared client: %w", ErrIdentityMismatch)
	}
	verified := VerifiedCredential{
		SellerID:            sellerID,
		OwnerTelegramID:     command.OwnerTelegramID,
		Name:                command.Name,
		ContentRead:         contentRead,
		ContentWrite:        contentWrite,
		TokenProperties:     claims.Properties,
		CredentialExpiresAt: claims.ExpiresAt,
		ClientGeneration:    generation,
		VerifiedAt:          now,
		Token:               command.Token,
	}
	txCtx, cancel := service.repository.OperationContext(ctx)
	defer cancel()
	var cabinet Cabinet
	err = service.uow.WithinTransaction(txCtx, func(txCtx context.Context, tx core_postgres_transaction.DBTX) error {
		var persistErr error
		cabinet, persistErr = service.repository.UpsertVerified(txCtx, tx, verified)
		return persistErr
	})
	if err != nil {
		return Cabinet{}, fmt.Errorf("persist verified WB cabinet: %w", err)
	}
	if cabinet.ID != sellerID {
		return Cabinet{}, core_errors.Conflict("persisted WB seller ID differs from verified candidate")
	}
	if err := prepared.Activate(); err != nil {
		return Cabinet{}, fmt.Errorf("activate verified WB cabinet: %w", err)
	}
	delete(service.restorePending, cabinet.ID)
	return cabinet, nil
}

func (service *Service) ListByOwner(
	ctx context.Context,
	ownerTelegramID int64,
) ([]Cabinet, error) {
	if ownerTelegramID <= 0 {
		return nil, core_errors.ErrInvalidArgument
	}
	return service.repository.ListByOwner(ctx, ownerTelegramID)
}

func (service *Service) GetByOwner(
	ctx context.Context,
	ownerTelegramID int64,
	sellerID SellerID,
) (Cabinet, error) {
	if ctx == nil || ownerTelegramID <= 0 || sellerID.Validate() != nil {
		return Cabinet{}, core_errors.ErrInvalidArgument
	}
	return service.repository.GetByOwner(ctx, ownerTelegramID, sellerID)
}

func (service *Service) Delete(
	ctx context.Context,
	ownerTelegramID int64,
	sellerID SellerID,
) error {
	if ctx == nil || ownerTelegramID <= 0 || sellerID.Validate() != nil {
		return core_errors.ErrInvalidArgument
	}
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()

	if err := service.repository.DeleteByOwner(ctx, ownerTelegramID, sellerID); err != nil {
		return fmt.Errorf("delete owned WB cabinet: %w", err)
	}
	service.verifier.Remove(sellerID)
	delete(service.restorePending, sellerID)
	return nil
}

func (service *Service) Targets(
	ctx context.Context,
	ownerTelegramID int64,
) ([]TargetCredential, error) {
	if ownerTelegramID <= 0 {
		return nil, core_errors.ErrInvalidArgument
	}
	return service.repository.ListTargets(ctx, ownerTelegramID)
}

func (service *Service) TargetsByOwnerRole(
	ctx context.Context,
	role domain.UserRole,
) ([]TargetCredential, error) {
	if ctx == nil || !role.IsValid() {
		return nil, core_errors.ErrInvalidArgument
	}
	return service.repository.ListTargetsByOwnerRole(ctx, role)
}

func (service *Service) AllTargets(
	ctx context.Context,
) ([]TargetCredential, error) {
	return service.repository.ListAllTargets(ctx)
}

func (service *Service) Restore(ctx context.Context) error {
	stored, err := service.repository.ListStored(ctx)
	if err != nil {
		return fmt.Errorf("list stored WB cabinets: %w", err)
	}
	var failures []error
	for _, cabinet := range stored {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := service.restoreCabinet(ctx, cabinet, false); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// restoreCabinet сверяет снимок с актуальной записью под блокировкой изменений.
func (service *Service) restoreCabinet(ctx context.Context, stored StoredCabinet, pendingOnly bool) error {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	current, err := service.repository.GetByOwner(ctx, stored.OwnerTelegramID, stored.ID)
	if errors.Is(err, core_errors.ErrNotFound) {
		delete(service.restorePending, stored.ID)
		return nil
	}
	if err != nil {
		return err
	}
	if current.ClientGeneration != stored.ClientGeneration || current.Name != stored.Name {
		return nil
	}
	if current.Status != StatusActive {
		delete(service.restorePending, current.ID)
		return nil
	}
	if !current.CredentialExpiresAt.After(service.now().UTC()) {
		service.verifier.Remove(current.ID)
		delete(service.restorePending, current.ID)
		return service.repository.MarkStatus(ctx, current.ID, StatusExpired)
	}
	if pendingOnly {
		generation, pending := service.restorePending[current.ID]
		if !pending || generation != current.ClientGeneration {
			return nil
		}
	}
	_, err = service.add(ctx, AddCommand{OwnerTelegramID: current.OwnerTelegramID, Name: current.Name, Token: stored.Token, SellerID: current.ID})
	if err == nil {
		return nil
	}
	service.verifier.Remove(current.ID)
	if IsTransientVerificationError(err) {
		service.restorePending[current.ID] = current.ClientGeneration
		return fmt.Errorf("restore WB cabinet %q: %w", current.ID, err)
	}
	delete(service.restorePending, current.ID)
	status := StatusVerificationFailed
	if errors.Is(err, ErrCredentialExpired) {
		status = StatusExpired
	} else if errors.Is(err, ErrIdentityMismatch) {
		status = StatusIdentityMismatch
	}
	return errors.Join(err, service.repository.MarkStatus(ctx, current.ID, status))
}

func validateAddCommand(command AddCommand) error {
	nameLength := utf8.RuneCountInString(command.Name)
	if command.OwnerTelegramID <= 0 || command.Name == "" ||
		!utf8.ValidString(command.Name) || nameLength > MaxCabinetNameLength ||
		command.Token == "" {
		return core_errors.InvalidArgument("invalid add WB cabinet command")
	}
	if command.SellerID != "" && command.SellerID.Validate() != nil {
		return core_errors.InvalidArgument("invalid WB seller ID")
	}
	return nil
}

func (service *Service) RunRestoreRetry(
	ctx context.Context,
	interval time.Duration,
	logger *zap.Logger,
) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := service.retryTransientCabinets(ctx, logger); err != nil && ctx.Err() == nil {
				logger.Warn(
					"cabinet restore retry failed",
					zap.Error(err),
				)
			}
		}
	}
}

func (service *Service) retryTransientCabinets(ctx context.Context, logger *zap.Logger) error {
	stored, err := service.repository.ListStored(ctx)
	if err != nil {
		return fmt.Errorf("list stored WB cabinets for retry: %w", err)
	}
	var failures []error
	for _, cabinet := range stored {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := service.restoreCabinet(ctx, cabinet, true); err != nil {
			failures = append(failures, err)
			logger.Warn("WB cabinet restore retry failed", zap.String("seller_id", string(cabinet.ID)), zap.Error(err))
		}
	}
	return errors.Join(failures...)
}
