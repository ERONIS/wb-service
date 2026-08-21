package transfer_postgres_repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (repository *Repository) SyncTargetBindings(
	ctx context.Context,
	verifiedAt time.Time,
	targets []transfer_service.VerifiedTarget,
) ([]transfer_service.TargetBinding, error) {
	if ctx == nil {
		return nil, errors.New("sync WB mutation target bindings: context is nil")
	}
	if verifiedAt.IsZero() {
		return nil, errors.New(
			"sync WB mutation target bindings: verified time is empty",
		)
	}
	if len(targets) == 0 {
		return nil, errors.New(
			"sync WB mutation target bindings: target list is empty",
		)
	}

	bindings := make([]transfer_service.TargetBinding, 0, len(targets))
	identityMismatch := false
	err := repository.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			for _, target := range targets {
				binding, mismatch, err := syncTargetBinding(
					ctx,
					tx,
					verifiedAt.UTC(),
					target,
				)
				if err != nil {
					return err
				}
				identityMismatch = identityMismatch || mismatch
				bindings = append(bindings, binding)
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	if identityMismatch {
		return nil, transfer_service.ErrTargetIdentityMismatch
	}
	return bindings, nil
}

func syncTargetBinding(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	verifiedAt time.Time,
	target transfer_service.VerifiedTarget,
) (transfer_service.TargetBinding, bool, error) {
	existing, found, err := lockTargetBinding(ctx, tx, target.CabinetID)
	if err != nil {
		return transfer_service.TargetBinding{}, false, err
	}
	if !found {
		binding, err := insertTargetBinding(ctx, tx, verifiedAt, target)
		return binding, false, err
	}
	if existing.SellerKey != target.SellerKey {
		const mismatch = `
			UPDATE wb.mutation_target_bindings
			SET
				status = 'identity_mismatch',
				mismatch_seller_key = $2,
				verified_at = $3,
				updated_at = CURRENT_TIMESTAMP
			WHERE cabinet_id = $1;
		`
		if _, err := tx.Exec(
			ctx,
			mismatch,
			target.CabinetID,
			target.SellerKey[:],
			verifiedAt,
		); err != nil {
			return transfer_service.TargetBinding{}, false, fmt.Errorf(
				"mark WB mutation target identity mismatch: %w",
				err,
			)
		}
		return transfer_service.TargetBinding{}, true, nil
	}

	capabilityRevision := existing.CapabilityRevision
	if existing.ContentRead != target.ContentRead ||
		existing.ContentWrite != target.ContentWrite {
		capabilityRevision++
	}
	const update = `
		UPDATE wb.mutation_target_bindings
		SET
			capability_revision = $2,
			content_read = $3,
			content_write = $4,
			client_generation = $5,
			credential_expires_at = $6,
			verified_at = $7,
			status = 'active',
			mismatch_seller_key = NULL,
			updated_at = CURRENT_TIMESTAMP
		WHERE cabinet_id = $1;
	`
	if _, err := tx.Exec(
		ctx,
		update,
		target.CabinetID,
		capabilityRevision,
		target.ContentRead,
		target.ContentWrite,
		target.ClientGeneration[:],
		target.CredentialExpiresAt.UTC(),
		verifiedAt,
	); err != nil {
		return transfer_service.TargetBinding{}, false, fmt.Errorf(
			"update WB mutation target binding: %w",
			err,
		)
	}
	existing.CapabilityRevision = capabilityRevision
	existing.ContentRead = target.ContentRead
	existing.ContentWrite = target.ContentWrite
	existing.ClientGeneration = target.ClientGeneration
	existing.CredentialExpiresAt = target.CredentialExpiresAt.UTC()
	return existing, false, nil
}

func lockTargetBinding(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	cabinetID transfer_service.CabinetID,
) (transfer_service.TargetBinding, bool, error) {
	const query = `
		SELECT
			cabinet_id,
			seller_key,
			binding_revision,
			capability_revision,
			content_read,
			content_write,
			credential_expires_at,
			client_generation
		FROM wb.mutation_target_bindings
		WHERE cabinet_id = $1
		FOR UPDATE;
	`
	binding, err := scanTargetBinding(tx.QueryRow(ctx, query, cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_service.TargetBinding{}, false, nil
	}
	if err != nil {
		return transfer_service.TargetBinding{}, false, fmt.Errorf(
			"lock WB mutation target binding: %w",
			err,
		)
	}
	return binding, true, nil
}

func insertTargetBinding(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	verifiedAt time.Time,
	target transfer_service.VerifiedTarget,
) (transfer_service.TargetBinding, error) {
	const query = `
		INSERT INTO wb.mutation_target_bindings (
			cabinet_id,
			seller_key,
			content_read,
			content_write,
			client_generation,
			credential_expires_at,
			verified_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING
			cabinet_id,
			seller_key,
			binding_revision,
			capability_revision,
			content_read,
			content_write,
			credential_expires_at,
			client_generation;
	`
	binding, err := scanTargetBinding(tx.QueryRow(
		ctx,
		query,
		target.CabinetID,
		target.SellerKey[:],
		target.ContentRead,
		target.ContentWrite,
		target.ClientGeneration[:],
		target.CredentialExpiresAt.UTC(),
		verifiedAt,
	))
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return transfer_service.TargetBinding{}, fmt.Errorf(
				"%w: seller is already bound",
				transfer_service.ErrDuplicateTargetSeller,
			)
		}
		return transfer_service.TargetBinding{}, fmt.Errorf(
			"insert WB mutation target binding: %w",
			err,
		)
	}
	return binding, nil
}

type targetBindingRow interface {
	Scan(dest ...any) error
}

func scanTargetBinding(
	row targetBindingRow,
) (transfer_service.TargetBinding, error) {
	var (
		binding          transfer_service.TargetBinding
		sellerKey        []byte
		clientGeneration []byte
	)
	err := row.Scan(
		&binding.CabinetID,
		&sellerKey,
		&binding.BindingRevision,
		&binding.CapabilityRevision,
		&binding.ContentRead,
		&binding.ContentWrite,
		&binding.CredentialExpiresAt,
		&clientGeneration,
	)
	if err != nil {
		return transfer_service.TargetBinding{}, err
	}
	if len(sellerKey) != len(binding.SellerKey) ||
		len(clientGeneration) != len(binding.ClientGeneration) {
		return transfer_service.TargetBinding{}, errors.New(
			"stored WB mutation target digest has invalid length",
		)
	}
	copy(binding.SellerKey[:], sellerKey)
	copy(binding.ClientGeneration[:], clientGeneration)
	return binding, nil
}

var _ transfer_service.TargetBindingStore = (*Repository)(nil)
