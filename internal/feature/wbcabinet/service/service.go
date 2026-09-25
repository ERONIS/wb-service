package wbcabinet_service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_wb_config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	"go.uber.org/zap"
)

type Service struct {
	repository Repository
	verifier   Verifier
	now        func() time.Time
	mutationMu sync.Mutex
}

func New(
	repository Repository,
	verifier Verifier,
) *Service {
	if repository == nil || verifier == nil {
		panic("wbcabinet service dependency is nil")
	}
	return &Service{
		repository: repository,
		verifier:   verifier,
		now:        time.Now,
	}
}

// Add verifies and activates a new cabinet. Reusing the same normalized name
// performs a safe token rotation and requires the same WB seller identity.
func (service *Service) Add(
	ctx context.Context,
	command AddCommand,
) (Cabinet, error) {
	if ctx == nil {
		return Cabinet{}, fmt.Errorf("add WB cabinet: context is nil: %w", core_errors.ErrInvalidArgument)
	}
	command.Name = strings.TrimSpace(command.Name)
	command.Token = strings.TrimSpace(command.Token)
	if err := validateAddCommand(command); err != nil {
		return Cabinet{}, err
	}
	// Verification includes remote requests and persistence. Serializing this
	// section keeps the durable generation and the activated runtime candidate
	// in the same order when two token rotations arrive concurrently.
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()

	existing, found, err := service.repository.FindByOwnerAndName(
		ctx,
		command.OwnerTelegramID,
		command.Name,
	)
	if err != nil {
		return Cabinet{}, fmt.Errorf("find WB cabinet before verification: %w", err)
	}
	cabinetID := command.CabinetID
	if found {
		cabinetID = existing.ID
	}
	if cabinetID == "" {
		cabinetID, err = newCabinetID()
		if err != nil {
			return Cabinet{}, err
		}
	}

	now := service.now().UTC()
	claims, err := decodeCredentialToken(command.Token, now)
	if err != nil {
		return Cabinet{}, err
	}
	contentRead := claims.Properties.Has(PermissionContent)
	contentWrite := !claims.Properties.Has(PermissionReadOnly)
	if !contentRead || !contentWrite {
		return Cabinet{}, ErrContentAccess
	}

	prepared, err := service.verifier.Prepare(ctx, cabinetID, command.Name, command.Token)
	if err != nil {
		return Cabinet{}, fmt.Errorf(
			"prepare and verify WB credential: %w",
			errors.Join(ErrVerification, err),
		)
	}
	generation := credentialGeneration(command.Token)
	if generation != prepared.Generation() {
		return Cabinet{}, fmt.Errorf("WB credential generation differs from prepared client: %w", ErrIdentityMismatch)
	}
	if found && existing.SellerKey != sellerKey(claims.SellerID) {
		return Cabinet{}, ErrIdentityMismatch
	}

	verified := VerifiedCredential{
		CabinetID:           cabinetID,
		OwnerTelegramID:     command.OwnerTelegramID,
		Name:                command.Name,
		SellerKey:           sellerKey(claims.SellerID),
		ContentRead:         contentRead,
		ContentWrite:        contentWrite,
		TokenProperties:     claims.Properties,
		CredentialExpiresAt: claims.ExpiresAt,
		ClientGeneration:    generation,
		VerifiedAt:          now,
		Token:               command.Token,
	}
	cabinet, err := service.repository.UpsertVerified(ctx, verified)
	if err != nil {
		return Cabinet{}, err
	}
	if cabinet.ID != cabinetID {
		return Cabinet{}, fmt.Errorf("persisted WB cabinet ID differs from verified candidate: %w", core_errors.ErrConflict)
	}
	if err := prepared.Activate(); err != nil {
		return Cabinet{}, fmt.Errorf("activate verified WB cabinet: %w", err)
	}
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
	cabinetID CabinetID,
) (Cabinet, error) {
	if ctx == nil || ownerTelegramID <= 0 || strings.TrimSpace(string(cabinetID)) == "" {
		return Cabinet{}, core_errors.ErrInvalidArgument
	}
	return service.repository.GetByOwner(ctx, ownerTelegramID, cabinetID)
}

// Delete removes a cabinet owned by the Telegram user from durable storage
// and from the live WB client registry.
func (service *Service) Delete(
	ctx context.Context,
	ownerTelegramID int64,
	cabinetID CabinetID,
) error {
	if ctx == nil || ownerTelegramID <= 0 || strings.TrimSpace(string(cabinetID)) == "" {
		return core_errors.ErrInvalidArgument
	}
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()

	if err := service.repository.DeleteByOwner(ctx, ownerTelegramID, cabinetID); err != nil {
		return fmt.Errorf("delete owned WB cabinet: %w", err)
	}
	service.verifier.Remove(cabinetID)
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

// TargetsByOwnerRole returns active cabinets whose current owner has the
// requested application role. Role membership is resolved on every call.
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

// Restore verifies every durable credential before publishing it into the
// runtime registry. Broken credentials are fail-closed and do not prevent the
// Telegram service from starting.
func (service *Service) Restore(ctx context.Context) error {
	stored, err := service.repository.ListStored(ctx)
	if err != nil {
		return fmt.Errorf("list stored WB cabinets: %w", err)
	}
	var restoreErrors []error
	for _, cabinet := range stored {
		_, addErr := service.Add(ctx, AddCommand{
			OwnerTelegramID: cabinet.OwnerTelegramID,
			Name:            cabinet.Name,
			Token:           cabinet.Token,
			CabinetID:       cabinet.ID,
		})
		if addErr == nil {
			continue
		}
		service.verifier.Remove(cabinet.ID)
		if IsTransientVerificationError(addErr) {
			restoreErrors = append(restoreErrors, fmt.Errorf(
				"restore WB cabinet %q: transient verification failure: %w",
				cabinet.ID,
				addErr,
			))
			continue
		}
		status := StatusVerificationFailed
		if errors.Is(addErr, ErrCredentialExpired) {
			status = StatusExpired
		} else if errors.Is(addErr, ErrIdentityMismatch) {
			status = StatusIdentityMismatch
		}
		statusErr := service.repository.MarkStatus(ctx, cabinet.ID, status)
		restoreErrors = append(restoreErrors, fmt.Errorf(
			"restore WB cabinet %q: %w",
			cabinet.ID,
			errors.Join(addErr, statusErr),
		))
	}
	return errors.Join(restoreErrors...)
}

func (service *Service) Bootstrap(
	ctx context.Context,
	ownerTelegramID int64,
	cabinets []core_wb_config.CabinetConfig,
) error {
	if len(cabinets) == 0 {
		return nil
	}
	if ownerTelegramID <= 0 {
		return fmt.Errorf("bootstrap WB cabinet owner is required: %w", core_errors.ErrInvalidArgument)
	}
	var bootstrapErrors []error
	for _, cabinet := range cabinets {
		_, err := service.Add(ctx, AddCommand{
			OwnerTelegramID: ownerTelegramID,
			Name:            cabinet.Name,
			Token:           cabinet.Token,
			CabinetID:       CabinetID(cabinet.ID),
		})
		if err != nil {
			bootstrapErrors = append(bootstrapErrors, fmt.Errorf("bootstrap WB cabinet %q: %w", cabinet.ID, err))
		}
	}
	return errors.Join(bootstrapErrors...)
}

func validateAddCommand(command AddCommand) error {
	nameLength := utf8.RuneCountInString(command.Name)
	if command.OwnerTelegramID <= 0 || command.Name == "" ||
		!utf8.ValidString(command.Name) || nameLength > MaxCabinetNameLength ||
		command.Token == "" {
		return fmt.Errorf("invalid add WB cabinet command: %w", core_errors.ErrInvalidArgument)
	}
	if command.CabinetID != "" && (strings.TrimSpace(string(command.CabinetID)) != string(command.CabinetID) ||
		len(command.CabinetID) > 128) {
		return fmt.Errorf("invalid bootstrap WB cabinet ID: %w", core_errors.ErrInvalidArgument)
	}
	return nil
}

func newCabinetID() (CabinetID, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate WB cabinet ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return CabinetID(fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	)), nil
}

// RunRestoreRetry runs a background loop that retries loading cabinets that
// failed with a transient verification error during startup. It stops when ctx
// is cancelled. Interval controls how often the retry is attempted; 30 seconds
// is a reasonable default.
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

// retryTransientCabinets attempts to load any stored cabinet not yet active in
// the runtime verifier. Cabinets that succeed are logged; those that still
// fail transiently are silently deferred to the next tick.
func (service *Service) retryTransientCabinets(ctx context.Context, logger *zap.Logger) error {
	stored, err := service.repository.ListStored(ctx)
	if err != nil {
		return fmt.Errorf("list stored WB cabinets for retry: %w", err)
	}
	for _, cabinet := range stored {
		_, addErr := service.Add(ctx, AddCommand{
			OwnerTelegramID: cabinet.OwnerTelegramID,
			Name:            cabinet.Name,
			Token:           cabinet.Token,
			CabinetID:       cabinet.ID,
		})
		if addErr == nil {
			logger.Info(
				"WB cabinet restored after transient failure",
				zap.String("cabinet_id", string(cabinet.ID)),
				zap.String("cabinet_name", cabinet.Name),
			)
			continue
		}
		if IsTransientVerificationError(addErr) {
			// Still unavailable — will retry on next tick.
			service.verifier.Remove(cabinet.ID)
			continue
		}
		// Permanent failure: update status in DB (same logic as Restore).
		service.verifier.Remove(cabinet.ID)
		status := StatusVerificationFailed
		if errors.Is(addErr, ErrCredentialExpired) {
			status = StatusExpired
		} else if errors.Is(addErr, ErrIdentityMismatch) {
			status = StatusIdentityMismatch
		}
		statusErr := service.repository.MarkStatus(ctx, cabinet.ID, status)
		logger.Warn(
			"WB cabinet permanently failed during restore retry",
			zap.String("cabinet_id", string(cabinet.ID)),
			zap.String("cabinet_name", cabinet.Name),
			zap.String("status", string(status)),
			zap.NamedError("add_error", addErr),
			zap.NamedError("status_error", statusErr),
		)
	}
	return nil
}
