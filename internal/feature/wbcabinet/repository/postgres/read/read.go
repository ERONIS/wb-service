package wbcabinet_postgres_read

import (
	"context"
	"errors"
	"fmt"
	wbcabinet_postgres_row "github.com/ERONIS/wb-service/internal/feature/wbcabinet/repository/postgres/internal/row"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"

	"github.com/jackc/pgx/v5"
)

func (repository *Read) FindByOwnerAndName(
	ctx context.Context,
	ownerTelegramID int64,
	name string,
) (wbcabinet_service.Cabinet, bool, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		SELECT ` + wbcabinet_postgres_row.Columns + `
		FROM wb.api_cabinets
		WHERE owner_tg_id = $1 AND normalized_name = lower(btrim($2));
	`
	cabinet, err := wbcabinet_postgres_row.ScanCabinet(repository.pool.QueryRow(ctx, query, ownerTelegramID, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return wbcabinet_service.Cabinet{}, false, nil
	}
	if err != nil {
		return wbcabinet_service.Cabinet{}, false, fmt.Errorf("find WB cabinet by owner and name: %w", err)
	}
	return cabinet, true, nil
}

func (repository *Read) GetByOwner(
	ctx context.Context,
	ownerTelegramID int64,
	sellerID domain.SellerID,
) (wbcabinet_service.Cabinet, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		SELECT ` + wbcabinet_postgres_row.Columns + `
		FROM wb.api_cabinets
		WHERE owner_tg_id = $1 AND seller_id = $2;
	`
	cabinet, err := wbcabinet_postgres_row.ScanCabinet(repository.pool.QueryRow(
		ctx,
		query,
		ownerTelegramID,
		sellerID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return wbcabinet_service.Cabinet{}, core_errors.ErrNotFound
	}
	if err != nil {
		return wbcabinet_service.Cabinet{}, fmt.Errorf("get owned WB cabinet: %w", err)
	}
	return cabinet, nil
}

func (repository *Read) ListByOwner(
	ctx context.Context,
	ownerTelegramID int64,
) ([]wbcabinet_service.Cabinet, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		SELECT ` + wbcabinet_postgres_row.Columns + `
		FROM wb.api_cabinets
		WHERE owner_tg_id = $1
		ORDER BY lower(display_name), seller_id;
	`
	rows, err := repository.pool.Query(ctx, query, ownerTelegramID)
	if err != nil {
		return nil, fmt.Errorf("list WB cabinets by owner: %w", err)
	}
	defer rows.Close()
	result := make([]wbcabinet_service.Cabinet, 0, 8)
	for rows.Next() {
		cabinet, err := wbcabinet_postgres_row.ScanCabinet(rows)
		if err != nil {
			return nil, fmt.Errorf("scan WB cabinet list: %w", err)
		}
		result = append(result, cabinet)
	}
	return result, rows.Err()
}

func (repository *Read) ListStored(
	ctx context.Context,
) ([]wbcabinet_service.StoredCabinet, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		SELECT ` + wbcabinet_postgres_row.Columns + `, token
		FROM wb.api_cabinets
		ORDER BY owner_tg_id, lower(display_name), seller_id;
	`
	rows, err := repository.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list stored WB cabinets: %w", err)
	}
	result, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (wbcabinet_service.StoredCabinet, error) {
		cabinet, token, err := wbcabinet_postgres_row.ScanStoredCabinet(row)
		if err != nil {
			return wbcabinet_service.StoredCabinet{}, err
		}
		return wbcabinet_service.StoredCabinet{
			Cabinet: cabinet,
			Token:   token,
		}, nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect stored WB cabinets: %w", err)
	}
	return result, nil
}

func (repository *Read) ListTargetsByOwnerRole(
	ctx context.Context,
	role domain.UserRole,
) ([]domain.CabinetAccess, error) {
	return repository.listTargets(ctx, &role)
}

func (repository *Read) ListAllTargets(
	ctx context.Context,
) ([]domain.CabinetAccess, error) {
	return repository.listTargets(ctx, nil)
}

func (repository *Read) listTargets(
	ctx context.Context,
	ownerRole *domain.UserRole,
) ([]domain.CabinetAccess, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	query := `
		SELECT ` + wbcabinet_postgres_row.Columns + `
		FROM wb.api_cabinets
		WHERE status = 'active'
			AND content_read
			AND content_write
			AND credential_expires_at > CURRENT_TIMESTAMP
	`
	arguments := []any(nil)
	if ownerRole != nil {
		query += ` AND EXISTS (
			SELECT 1
			FROM wb.users AS owner
			WHERE owner.tg_id = wb.api_cabinets.owner_tg_id
				AND owner.role = $1
		)`
		arguments = append(arguments, *ownerRole)
	}
	query += " ORDER BY owner_tg_id, lower(display_name), seller_id;"
	rows, err := repository.pool.Query(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list active WB cabinet targets: %w", err)
	}
	defer rows.Close()
	result := make([]domain.CabinetAccess, 0, 16)
	for rows.Next() {
		cabinet, err := wbcabinet_postgres_row.ScanCabinet(rows)
		if err != nil {
			return nil, fmt.Errorf("scan active WB cabinet target: %w", err)
		}
		result = append(result, domain.CabinetAccess{
			SellerID:            cabinet.ID,
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
