package wbidentity_postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	identity "github.com/ERONIS/wb-service/internal/core/transport/wb/identity"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Store struct {
	uow core_postgres_transaction.UnitOfWork
}

func New(uow core_postgres_transaction.UnitOfWork) *Store {
	if uow == nil {
		panic("WB identity unit of work is nil")
	}
	return &Store{uow: uow}
}

func (store *Store) SyncBindings(
	ctx context.Context,
	verifiedAt time.Time,
	credentials []identity.VerifiedCredential,
) ([]identity.Binding, error) {
	if ctx == nil {
		return nil, errors.New("sync WB identity bindings: context is nil")
	}
	if verifiedAt.IsZero() {
		return nil, errors.New("sync WB identity bindings: verified time is empty")
	}
	if len(credentials) == 0 {
		return nil, errors.New("sync WB identity bindings: credential list is empty")
	}

	bindings := make([]identity.Binding, 0, len(credentials))
	identityMismatch := false
	err := store.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			for _, credential := range credentials {
				binding, mismatch, err := syncBinding(ctx, tx, verifiedAt.UTC(), credential)
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
		return nil, identity.ErrIdentityMismatch
	}
	return bindings, nil
}

func syncBinding(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	verifiedAt time.Time,
	credential identity.VerifiedCredential,
) (identity.Binding, bool, error) {
	existing, found, err := lockBinding(ctx, tx, credential.CabinetID)
	if err != nil {
		return identity.Binding{}, false, err
	}
	if !found {
		binding, err := insertBinding(ctx, tx, verifiedAt, credential)
		return binding, false, err
	}
	if existing.SellerKey != credential.SellerKey {
		const mismatch = `
			UPDATE wb.cabinet_identity_bindings
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
			credential.CabinetID,
			credential.SellerKey[:],
			verifiedAt,
		); err != nil {
			return identity.Binding{}, false, fmt.Errorf("mark WB cabinet identity mismatch: %w", err)
		}
		return identity.Binding{}, true, nil
	}

	capabilityRevision := existing.CapabilityRevision
	if existing.ContentRead != credential.ContentRead ||
		existing.ContentWrite != credential.ContentWrite {
		capabilityRevision++
	}
	const update = `
		UPDATE wb.cabinet_identity_bindings
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
		credential.CabinetID,
		capabilityRevision,
		credential.ContentRead,
		credential.ContentWrite,
		credential.ClientGeneration[:],
		credential.CredentialExpiresAt.UTC(),
		verifiedAt,
	); err != nil {
		return identity.Binding{}, false, fmt.Errorf("update WB identity binding: %w", err)
	}
	existing.CapabilityRevision = capabilityRevision
	existing.ContentRead = credential.ContentRead
	existing.ContentWrite = credential.ContentWrite
	existing.ClientGeneration = credential.ClientGeneration
	existing.CredentialExpiresAt = credential.CredentialExpiresAt.UTC()
	return existing, false, nil
}

func lockBinding(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	cabinetID config.CabinetID,
) (identity.Binding, bool, error) {
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
		FROM wb.cabinet_identity_bindings
		WHERE cabinet_id = $1
		FOR UPDATE;
	`
	binding, err := scanBinding(tx.QueryRow(ctx, query, cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Binding{}, false, nil
	}
	if err != nil {
		return identity.Binding{}, false, fmt.Errorf("lock WB identity binding: %w", err)
	}
	return binding, true, nil
}

func insertBinding(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	verifiedAt time.Time,
	credential identity.VerifiedCredential,
) (identity.Binding, error) {
	const query = `
		INSERT INTO wb.cabinet_identity_bindings (
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
	binding, err := scanBinding(tx.QueryRow(
		ctx,
		query,
		credential.CabinetID,
		credential.SellerKey[:],
		credential.ContentRead,
		credential.ContentWrite,
		credential.ClientGeneration[:],
		credential.CredentialExpiresAt.UTC(),
		verifiedAt,
	))
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return identity.Binding{}, fmt.Errorf("%w: seller is already bound", identity.ErrDuplicateSeller)
		}
		return identity.Binding{}, fmt.Errorf("insert WB identity binding: %w", err)
	}
	return binding, nil
}

type bindingRow interface {
	Scan(dest ...any) error
}

func scanBinding(row bindingRow) (identity.Binding, error) {
	var (
		binding          identity.Binding
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
		return identity.Binding{}, err
	}
	if len(sellerKey) != len(binding.SellerKey) ||
		len(clientGeneration) != len(binding.ClientGeneration) {
		return identity.Binding{}, errors.New("stored WB identity digest has invalid length")
	}
	copy(binding.SellerKey[:], sellerKey)
	copy(binding.ClientGeneration[:], clientGeneration)
	return binding, nil
}

var _ identity.Store = (*Store)(nil)
