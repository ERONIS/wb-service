package wbcabinet_postgres_repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const cabinetColumns = `
	cabinet_id,
	owner_tg_id,
	display_name,
	seller_key,
	binding_revision,
	capability_revision,
	content_read,
	content_write,
	token_properties,
	credential_expires_at,
	client_generation,
	verified_at,
	status,
	created_at,
	updated_at
`

type rowScanner interface {
	Scan(dest ...any) error
}

func (repository *Repository) FindByOwnerAndName(
	ctx context.Context,
	ownerTelegramID int64,
	name string,
) (wbcabinet_service.Cabinet, bool, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		SELECT ` + cabinetColumns + `
		FROM wb.api_cabinets
		WHERE owner_tg_id = $1 AND normalized_name = lower(btrim($2));
	`
	cabinet, err := scanCabinet(repository.pool.QueryRow(ctx, query, ownerTelegramID, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return wbcabinet_service.Cabinet{}, false, nil
	}
	if err != nil {
		return wbcabinet_service.Cabinet{}, false, fmt.Errorf("find WB cabinet by owner and name: %w", err)
	}
	return cabinet, true, nil
}

func (repository *Repository) GetByOwner(
	ctx context.Context,
	ownerTelegramID int64,
	cabinetID wbcabinet_service.CabinetID,
) (wbcabinet_service.Cabinet, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		SELECT ` + cabinetColumns + `
		FROM wb.api_cabinets
		WHERE owner_tg_id = $1 AND cabinet_id = $2;
	`
	cabinet, err := scanCabinet(repository.pool.QueryRow(
		ctx,
		query,
		ownerTelegramID,
		cabinetID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return wbcabinet_service.Cabinet{}, core_errors.ErrNotFound
	}
	if err != nil {
		return wbcabinet_service.Cabinet{}, fmt.Errorf("get owned WB cabinet: %w", err)
	}
	return cabinet, nil
}

func (repository *Repository) UpsertVerified(
	ctx context.Context,
	credential wbcabinet_service.VerifiedCredential,
) (wbcabinet_service.Cabinet, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	var result wbcabinet_service.Cabinet
	err := repository.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			existing, found, err := lockByOwnerAndName(
				ctx,
				tx,
				credential.OwnerTelegramID,
				credential.Name,
			)
			if err != nil {
				return err
			}
			if found {
				if existing.ID != credential.CabinetID {
					return fmt.Errorf("WB cabinet ID changed during verification: %w", core_errors.ErrConflict)
				}
				if existing.SellerKey != credential.SellerKey {
					return wbcabinet_service.ErrIdentityMismatch
				}
				result, err = updateVerified(ctx, tx, existing, credential)
				return err
			}
			result, err = insertVerified(ctx, tx, credential)
			return err
		},
	)
	if err != nil {
		return wbcabinet_service.Cabinet{}, fmt.Errorf("persist verified WB cabinet: %w", err)
	}
	return result, nil
}

func lockByOwnerAndName(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	ownerTelegramID int64,
	name string,
) (wbcabinet_service.Cabinet, bool, error) {
	const query = `
		SELECT ` + cabinetColumns + `
		FROM wb.api_cabinets
		WHERE owner_tg_id = $1 AND normalized_name = lower(btrim($2))
		FOR UPDATE;
	`
	cabinet, err := scanCabinet(tx.QueryRow(ctx, query, ownerTelegramID, name))
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
			cabinet_id,
			owner_tg_id,
			display_name,
			normalized_name,
			token,
			seller_key,
			content_read,
			content_write,
			token_properties,
			client_generation,
			credential_expires_at,
			verified_at,
			status
		)
		VALUES ($1, $2, btrim($3), lower(btrim($3)), $4, $5, $6, $7, $8, $9, $10, $11, 'active')
		RETURNING ` + cabinetColumns + `;
	`
	cabinet, err := scanCabinet(tx.QueryRow(
		ctx,
		query,
		credential.CabinetID,
		credential.OwnerTelegramID,
		credential.Name,
		credential.Token,
		credential.SellerKey[:],
		credential.ContentRead,
		credential.ContentWrite,
		int64(credential.TokenProperties),
		credential.ClientGeneration[:],
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
			client_generation = $8,
			credential_expires_at = $9,
			verified_at = $10,
			status = 'active',
			updated_at = CURRENT_TIMESTAMP
		WHERE cabinet_id = $1
		RETURNING ` + cabinetColumns + `;
	`
	cabinet, err := scanCabinet(tx.QueryRow(
		ctx,
		query,
		existing.ID,
		credential.Name,
		credential.Token,
		capabilityRevision,
		credential.ContentRead,
		credential.ContentWrite,
		int64(credential.TokenProperties),
		credential.ClientGeneration[:],
		credential.CredentialExpiresAt.UTC(),
		credential.VerifiedAt.UTC(),
	))
	if err != nil {
		return wbcabinet_service.Cabinet{}, classifyConstraint(err)
	}
	return cabinet, nil
}

func (repository *Repository) ListByOwner(
	ctx context.Context,
	ownerTelegramID int64,
) ([]wbcabinet_service.Cabinet, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		SELECT ` + cabinetColumns + `
		FROM wb.api_cabinets
		WHERE owner_tg_id = $1
		ORDER BY lower(display_name), cabinet_id;
	`
	rows, err := repository.pool.Query(ctx, query, ownerTelegramID)
	if err != nil {
		return nil, fmt.Errorf("list WB cabinets by owner: %w", err)
	}
	defer rows.Close()
	result := make([]wbcabinet_service.Cabinet, 0, 8)
	for rows.Next() {
		cabinet, err := scanCabinet(rows)
		if err != nil {
			return nil, fmt.Errorf("scan WB cabinet list: %w", err)
		}
		result = append(result, cabinet)
	}
	return result, rows.Err()
}

func (repository *Repository) ListStored(
	ctx context.Context,
) ([]wbcabinet_service.StoredCabinet, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		SELECT ` + cabinetColumns + `, token
		FROM wb.api_cabinets
		ORDER BY owner_tg_id, lower(display_name), cabinet_id;
	`
	rows, err := repository.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list stored WB cabinets: %w", err)
	}
	defer rows.Close()
	result := make([]wbcabinet_service.StoredCabinet, 0, 16)
	for rows.Next() {
		cabinet, token, err := scanStoredCabinet(rows)
		if err != nil {
			return nil, fmt.Errorf("scan stored WB cabinet: %w", err)
		}
		result = append(result, wbcabinet_service.StoredCabinet{
			Cabinet: cabinet,
			Token:   token,
		})
	}
	return result, rows.Err()
}

func (repository *Repository) ListTargets(
	ctx context.Context,
	ownerTelegramID int64,
) ([]wbcabinet_service.TargetCredential, error) {
	return repository.listTargets(ctx, &ownerTelegramID, nil)
}

func (repository *Repository) ListTargetsByOwnerRole(
	ctx context.Context,
	role domain.UserRole,
) ([]wbcabinet_service.TargetCredential, error) {
	return repository.listTargets(ctx, nil, &role)
}

func (repository *Repository) ListAllTargets(
	ctx context.Context,
) ([]wbcabinet_service.TargetCredential, error) {
	return repository.listTargets(ctx, nil, nil)
}

func (repository *Repository) listTargets(
	ctx context.Context,
	ownerTelegramID *int64,
	ownerRole *domain.UserRole,
) ([]wbcabinet_service.TargetCredential, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	query := `
		SELECT ` + cabinetColumns + `
		FROM wb.api_cabinets
		WHERE status = 'active'
			AND content_read
			AND content_write
			AND credential_expires_at > CURRENT_TIMESTAMP
	`
	arguments := []any(nil)
	if ownerTelegramID != nil {
		query += fmt.Sprintf(" AND owner_tg_id = $%d", len(arguments)+1)
		arguments = append(arguments, *ownerTelegramID)
	}
	if ownerRole != nil {
		query += fmt.Sprintf(` AND EXISTS (
			SELECT 1
			FROM wb.users AS owner
			WHERE owner.tg_id = wb.api_cabinets.owner_tg_id
				AND owner.role = $%d
		)`, len(arguments)+1)
		arguments = append(arguments, *ownerRole)
	}
	query += " ORDER BY owner_tg_id, lower(display_name), cabinet_id;"
	rows, err := repository.pool.Query(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list active WB cabinet targets: %w", err)
	}
	defer rows.Close()
	result := make([]wbcabinet_service.TargetCredential, 0, 16)
	for rows.Next() {
		cabinet, err := scanCabinet(rows)
		if err != nil {
			return nil, fmt.Errorf("scan active WB cabinet target: %w", err)
		}
		result = append(result, wbcabinet_service.TargetCredential{
			CabinetID:           cabinet.ID,
			Name:                cabinet.Name,
			SellerKey:           cabinet.SellerKey,
			BindingRevision:     cabinet.BindingRevision,
			CapabilityRevision:  cabinet.CapabilityRevision,
			ContentRead:         cabinet.ContentRead,
			ContentWrite:        cabinet.ContentWrite,
			CredentialExpiresAt: cabinet.CredentialExpiresAt,
			ClientGeneration:    cabinet.ClientGeneration,
		})
	}
	return result, rows.Err()
}

func (repository *Repository) MarkStatus(
	ctx context.Context,
	cabinetID wbcabinet_service.CabinetID,
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
		WHERE cabinet_id = $1;
	`
	tag, err := repository.pool.Exec(ctx, query, cabinetID, status)
	if err != nil {
		return fmt.Errorf("mark WB cabinet status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return core_errors.ErrNotFound
	}
	return nil
}

func (repository *Repository) DeleteByOwner(
	ctx context.Context,
	ownerTelegramID int64,
	cabinetID wbcabinet_service.CabinetID,
) error {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		DELETE FROM wb.api_cabinets
		WHERE owner_tg_id = $1 AND cabinet_id = $2;
	`
	tag, err := repository.pool.Exec(ctx, query, ownerTelegramID, cabinetID)
	if err != nil {
		return fmt.Errorf("delete owned WB cabinet: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return core_errors.ErrNotFound
	}
	return nil
}

func scanStoredCabinet(row rowScanner) (wbcabinet_service.Cabinet, string, error) {
	var (
		cabinet          wbcabinet_service.Cabinet
		sellerKey        []byte
		clientGeneration []byte
		token            string
		tokenProperties  int64
	)
	err := row.Scan(
		&cabinet.ID,
		&cabinet.OwnerTelegramID,
		&cabinet.Name,
		&sellerKey,
		&cabinet.BindingRevision,
		&cabinet.CapabilityRevision,
		&cabinet.ContentRead,
		&cabinet.ContentWrite,
		&tokenProperties,
		&cabinet.CredentialExpiresAt,
		&clientGeneration,
		&cabinet.VerifiedAt,
		&cabinet.Status,
		&cabinet.CreatedAt,
		&cabinet.UpdatedAt,
		&token,
	)
	if err != nil {
		return wbcabinet_service.Cabinet{}, "", err
	}
	if tokenProperties < 0 {
		return wbcabinet_service.Cabinet{}, "", errors.New("stored WB token properties are negative")
	}
	cabinet.TokenProperties = wbcabinet_service.TokenProperties(tokenProperties)
	if err := setDigests(&cabinet, sellerKey, clientGeneration); err != nil {
		return wbcabinet_service.Cabinet{}, "", err
	}
	return cabinet, token, nil
}

func scanCabinet(row rowScanner) (wbcabinet_service.Cabinet, error) {
	var (
		cabinet          wbcabinet_service.Cabinet
		sellerKey        []byte
		clientGeneration []byte
		tokenProperties  int64
	)
	err := row.Scan(
		&cabinet.ID,
		&cabinet.OwnerTelegramID,
		&cabinet.Name,
		&sellerKey,
		&cabinet.BindingRevision,
		&cabinet.CapabilityRevision,
		&cabinet.ContentRead,
		&cabinet.ContentWrite,
		&tokenProperties,
		&cabinet.CredentialExpiresAt,
		&clientGeneration,
		&cabinet.VerifiedAt,
		&cabinet.Status,
		&cabinet.CreatedAt,
		&cabinet.UpdatedAt,
	)
	if err != nil {
		return wbcabinet_service.Cabinet{}, err
	}
	if tokenProperties < 0 {
		return wbcabinet_service.Cabinet{}, errors.New("stored WB token properties are negative")
	}
	cabinet.TokenProperties = wbcabinet_service.TokenProperties(tokenProperties)
	if err := setDigests(&cabinet, sellerKey, clientGeneration); err != nil {
		return wbcabinet_service.Cabinet{}, err
	}
	return cabinet, nil
}

func setDigests(
	cabinet *wbcabinet_service.Cabinet,
	sellerKey []byte,
	clientGeneration []byte,
) error {
	if len(sellerKey) != len(cabinet.SellerKey) ||
		len(clientGeneration) != len(cabinet.ClientGeneration) {
		return errors.New("stored WB cabinet digest has invalid length")
	}
	copy(cabinet.SellerKey[:], sellerKey)
	copy(cabinet.ClientGeneration[:], clientGeneration)
	return nil
}

func classifyConstraint(err error) error {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23505" {
		return err
	}
	switch strings.TrimSpace(postgresError.ConstraintName) {
	case "api_cabinets_seller_key_key":
		return wbcabinet_service.ErrDuplicateSeller
	case "api_cabinets_owner_name_key":
		return wbcabinet_service.ErrCabinetNameTaken
	default:
		return fmt.Errorf("WB cabinet uniqueness conflict: %w", core_errors.ErrConflict)
	}
}

var _ wbcabinet_service.Repository = (*Repository)(nil)
