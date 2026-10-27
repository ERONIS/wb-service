package wbcabinet_postgres_write

import (
	"context"
	"errors"
	"fmt"
	wbcabinet_postgres_row "github.com/ERONIS/wb-service/internal/feature/wbcabinet/repository/postgres/internal/row"
	"strings"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_pgerr "github.com/ERONIS/wb-service/internal/core/repository/postgres/pgerr"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"

	"github.com/jackc/pgx/v5"
)

func (repository *Write) UpsertVerified(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	credential wbcabinet_service.VerifiedCredential,
) (wbcabinet_service.Cabinet, error) {
	existing, found, err := lockByOwnerAndName(
		ctx,
		tx,
		credential.OwnerTelegramID,
		credential.Name,
	)
	if err != nil {
		return wbcabinet_service.Cabinet{}, err
	}
	if found {
		if existing.ID != credential.SellerID {
			return wbcabinet_service.Cabinet{}, wbcabinet_service.ErrIdentityMismatch
		}
		return updateVerified(ctx, tx, existing, credential)
	}
	return insertVerified(ctx, tx, credential)
}

func lockByOwnerAndName(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	ownerTelegramID int64,
	name string,
) (wbcabinet_service.Cabinet, bool, error) {
	const query = `
		SELECT ` + wbcabinet_postgres_row.Columns + `
		FROM wb.api_cabinets
		WHERE owner_tg_id = $1 AND normalized_name = lower(btrim($2))
		FOR UPDATE;
	`
	cabinet, err := wbcabinet_postgres_row.ScanCabinet(tx.QueryRow(ctx, query, ownerTelegramID, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return wbcabinet_service.Cabinet{}, false, nil
	}
	if err != nil {
		return wbcabinet_service.Cabinet{}, false, fmt.Errorf("lock WB cabinet: %w", err)
	}
	return cabinet, true, nil
}

func insertVerified(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	credential wbcabinet_service.VerifiedCredential,
) (wbcabinet_service.Cabinet, error) {
	const query = `
		INSERT INTO wb.api_cabinets (
			seller_id,
			owner_tg_id,
			display_name,
			normalized_name,
			token,
			content_read,
			content_write,
			token_properties,
			credential_expires_at,
			verified_at,
			status
		)
		VALUES ($1, $2, btrim($3), lower(btrim($3)), $4, $5, $6, $7, $8, $9, 'active')
		RETURNING ` + wbcabinet_postgres_row.Columns + `;
	`
	cabinet, err := wbcabinet_postgres_row.ScanCabinet(tx.QueryRow(
		ctx,
		query,
		credential.SellerID,
		credential.OwnerTelegramID,
		credential.Name,
		credential.Token,
		credential.ContentRead,
		credential.ContentWrite,
		int64(credential.TokenProperties),
		credential.CredentialExpiresAt.UTC(),
		credential.VerifiedAt.UTC(),
	))
	if err != nil {
		return wbcabinet_service.Cabinet{}, classifyConstraint(err)
	}
	return cabinet, nil
}

func updateVerified(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	existing wbcabinet_service.Cabinet,
	credential wbcabinet_service.VerifiedCredential,
) (wbcabinet_service.Cabinet, error) {
	capabilityRevision := existing.CapabilityRevision
	if existing.ContentRead != credential.ContentRead ||
		existing.ContentWrite != credential.ContentWrite ||
		existing.TokenProperties != credential.TokenProperties {
		capabilityRevision++
	}
	const query = `
		UPDATE wb.api_cabinets
		SET
			display_name = btrim($2),
			normalized_name = lower(btrim($2)),
			token = $3,
			capability_revision = $4,
			content_read = $5,
			content_write = $6,
			token_properties = $7,
			client_generation = CASE
				WHEN token IS DISTINCT FROM $3 THEN nextval('wb.client_generation_seq')
				ELSE client_generation
			END,
			credential_expires_at = $8,
			verified_at = $9,
			status = 'active',
			updated_at = CURRENT_TIMESTAMP
		WHERE seller_id = $1
		RETURNING ` + wbcabinet_postgres_row.Columns + `;
	`
	cabinet, err := wbcabinet_postgres_row.ScanCabinet(tx.QueryRow(
		ctx,
		query,
		existing.ID,
		credential.Name,
		credential.Token,
		capabilityRevision,
		credential.ContentRead,
		credential.ContentWrite,
		int64(credential.TokenProperties),
		credential.CredentialExpiresAt.UTC(),
		credential.VerifiedAt.UTC(),
	))
	if err != nil {
		return wbcabinet_service.Cabinet{}, classifyConstraint(err)
	}
	return cabinet, nil
}

func (repository *Write) MarkStatus(
	ctx context.Context,
	sellerID domain.SellerID,
	status wbcabinet_service.Status,
) error {
	if !status.IsValid() {
		return core_errors.ErrInvalidArgument
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		UPDATE wb.api_cabinets
		SET status = $2, updated_at = CURRENT_TIMESTAMP
		WHERE seller_id = $1;
	`
	tag, err := repository.pool.Exec(ctx, query, sellerID, status)
	if err != nil {
		return fmt.Errorf("mark WB cabinet status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return core_errors.ErrNotFound
	}
	return nil
}

func (repository *Write) DeleteByOwner(
	ctx context.Context,
	ownerTelegramID int64,
	sellerID domain.SellerID,
) error {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		DELETE FROM wb.api_cabinets
		WHERE owner_tg_id = $1 AND seller_id = $2;
	`
	tag, err := repository.pool.Exec(ctx, query, ownerTelegramID, sellerID)
	if err != nil {
		return fmt.Errorf("delete owned WB cabinet: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return core_errors.ErrNotFound
	}
	return nil
}

func classifyConstraint(err error) error {
	if !core_postgres_pgerr.IsUniqueViolation(err) {
		return err
	}
	switch strings.TrimSpace(core_postgres_pgerr.ConstraintName(err)) {
	case "api_cabinets_pkey":
		return wbcabinet_service.ErrDuplicateSeller
	case "api_cabinets_owner_name_key":
		return wbcabinet_service.ErrCabinetNameTaken
	default:
		return fmt.Errorf("WB cabinet uniqueness conflict: %w", core_errors.ErrConflict)
	}
}
