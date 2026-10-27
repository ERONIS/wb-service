package wbcabinet_postgres_row

import (
	"errors"

	core_credentials "github.com/ERONIS/wb-service/internal/core/transport/wb/credentials"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"

	"github.com/jackc/pgx/v5"
)

const Columns = `
	seller_id,
	owner_tg_id,
	display_name,
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

func ScanStoredCabinet(row pgx.Row) (wbcabinet_service.Cabinet, string, error) {
	return scanCabinetRow(row, true)
}

func ScanCabinet(row pgx.Row) (wbcabinet_service.Cabinet, error) {
	cabinet, _, err := scanCabinetRow(row, false)
	return cabinet, err
}

func scanCabinetRow(row pgx.Row, withToken bool) (wbcabinet_service.Cabinet, string, error) {
	var (
		cabinet         wbcabinet_service.Cabinet
		token           string
		tokenProperties int64
	)
	fields := []any{
		&cabinet.ID,
		&cabinet.OwnerTelegramID,
		&cabinet.Name,
		&cabinet.BindingRevision,
		&cabinet.CapabilityRevision,
		&cabinet.ContentRead,
		&cabinet.ContentWrite,
		&tokenProperties,
		&cabinet.CredentialExpiresAt,
		&cabinet.ClientGeneration,
		&cabinet.VerifiedAt,
		&cabinet.Status,
		&cabinet.CreatedAt,
		&cabinet.UpdatedAt,
	}
	if withToken {
		fields = append(fields, &token)
	}
	if err := row.Scan(fields...); err != nil {
		return wbcabinet_service.Cabinet{}, "", err
	}
	if tokenProperties < 0 {
		return wbcabinet_service.Cabinet{}, "", errors.New("stored WB token properties are negative")
	}
	cabinet.TokenProperties = core_credentials.TokenProperties(tokenProperties)
	if cabinet.ID.Validate() != nil || cabinet.ClientGeneration <= 0 {
		return wbcabinet_service.Cabinet{}, "", errors.New("stored WB cabinet generation is invalid")
	}
	return cabinet, token, nil
}
