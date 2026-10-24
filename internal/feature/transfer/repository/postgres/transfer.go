package transfer_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const transferColumns = `
	transfer.id,
	transfer.batch_id,
	transfer.phase,
	transfer.outcome,
	COALESCE(transfer.attention_code, ''),
	transfer.revision,
	transfer.batch_checksum,
	transfer.items_count,
	transfer.groups_count,
	transfer.cohort_name,
	transfer.target_snapshot_revision,
	transfer.target_set_root,
	transfer.targets_count,
	transfer.item_targets_count,
	transfer.group_targets_count,
	transfer.created_at,
	transfer.started_at,
	transfer.finished_at,
	transfer.updated_at
`

type rowScanner interface {
	Scan(dest ...any) error
}

// listTransfers выбирает страницу переносов по внутреннему статическому условию.
func (repository *Repository) listTransfers(
	ctx context.Context,
	afterID transfer_service.TransferID,
	limit int,
	condition string,
	operation string,
) ([]transfer_service.Transfer, error) {
	if afterID < 0 || limit <= 0 || limit > initializationListPageLimit {
		return nil, core_errors.ErrInvalidArgument
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	query := `
		SELECT ` + transferColumns + `
		FROM wb.transfers AS transfer
		WHERE ` + condition + `
			AND transfer.id > $1
		ORDER BY transfer.id
		LIMIT $2;
	`
	rows, err := repository.pool.Query(ctx, query, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list %s transfers: %w", operation, err)
	}
	defer rows.Close()

	transfers := make([]transfer_service.Transfer, 0)
	for rows.Next() {
		transfer, err := scanTransfer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan %s transfer: %w", operation, err)
		}
		transfers = append(transfers, transfer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s transfers: %w", operation, err)
	}
	return transfers, nil
}

func (repository *Repository) FindByBatch(
	ctx context.Context,
	batchID cardpipeline.BatchID,
) (transfer_service.Transfer, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	return loadByBatch(ctx, repository.pool, batchID)
}

func (repository *Repository) FindByID(
	ctx context.Context,
	transferID transfer_service.TransferID,
) (transfer_service.Transfer, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	query := `
		SELECT ` + transferColumns + `
		FROM wb.transfers AS transfer
		WHERE transfer.id = $1;
	`
	transfer, err := scanTransfer(repository.pool.QueryRow(ctx, query, transferID))
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_service.Transfer{}, fmt.Errorf(
			"transfer ID='%d': %w",
			transferID,
			core_errors.ErrNotFound,
		)
	}
	if err != nil {
		return transfer_service.Transfer{}, fmt.Errorf(
			"scan transfer ID='%d': %w",
			transferID,
			err,
		)
	}
	return transfer, nil
}

func (repository *Repository) ListTargetSellerIDs(
	ctx context.Context,
	transferID transfer_service.TransferID,
) ([]transfer_service.SellerID, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	return listTargetSellerIDs(ctx, repository.pool, transferID)
}

func (repository *Repository) ListTargetSellerIDsWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
) ([]transfer_service.SellerID, error) {
	if tx == nil {
		return nil, errors.New("list transfer targets: DBTX is nil")
	}
	return listTargetSellerIDs(ctx, tx, transferID)
}

func listTargetSellerIDs(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
) ([]transfer_service.SellerID, error) {
	const query = `
		SELECT seller_id
		FROM wb.transfer_targets
		WHERE transfer_id = $1
		ORDER BY position;
	`
	rows, err := db.Query(ctx, query, transferID)
	if err != nil {
		return nil, fmt.Errorf("select transfer targets: %w", err)
	}
	defer rows.Close()
	result := make([]transfer_service.SellerID, 0)
	for rows.Next() {
		var sellerID string
		if err := rows.Scan(&sellerID); err != nil {
			return nil, fmt.Errorf("scan transfer target: %w", err)
		}
		result = append(result, transfer_service.SellerID(sellerID))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transfer targets: %w", err)
	}
	return result, nil
}

func loadByBatch(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	batchID cardpipeline.BatchID,
) (transfer_service.Transfer, error) {
	query := `
		SELECT ` + transferColumns + `
		FROM wb.transfers AS transfer
		WHERE transfer.batch_id = $1;
	`
	transfer, err := scanTransfer(db.QueryRow(ctx, query, batchID))
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_service.Transfer{}, fmt.Errorf(
			"transfer for batch ID='%d': %w",
			batchID,
			core_errors.ErrNotFound,
		)
	}
	if err != nil {
		return transfer_service.Transfer{}, fmt.Errorf(
			"scan transfer for batch ID='%d': %w",
			batchID,
			err,
		)
	}
	return transfer, nil
}

func (repository *Repository) Create(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_service.CreateTransfer,
) (transfer_service.Transfer, bool, error) {
	if tx == nil {
		return transfer_service.Transfer{}, false, errors.New(
			"create transfer: DBTX is nil",
		)
	}

	const query = `
		INSERT INTO wb.transfers (
			batch_id,
			batch_checksum,
			items_count,
			groups_count,
			cohort_name,
			target_snapshot_revision,
			target_set_root,
			targets_count,
			item_targets_count,
			group_targets_count
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (batch_id) DO NOTHING
		RETURNING
			id,
			batch_id,
			phase,
			outcome,
			COALESCE(attention_code, ''),
			revision,
			batch_checksum,
			items_count,
			groups_count,
			cohort_name,
			target_snapshot_revision,
			target_set_root,
			targets_count,
			item_targets_count,
			group_targets_count,
			created_at,
			started_at,
			finished_at,
			updated_at;
	`
	transfer, err := scanTransfer(tx.QueryRow(
		ctx,
		query,
		command.Batch.ID,
		command.Batch.Checksum[:],
		command.Batch.ItemsCount,
		command.Batch.GroupsCount,
		command.Snapshot.CohortName,
		command.Snapshot.Revision[:],
		command.TargetSetRoot[:],
		command.Capacity.Targets,
		command.Capacity.ItemTargets,
		command.Capacity.GroupTargets,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := loadByBatch(ctx, tx, command.Batch.ID)
		return existing, false, loadErr
	}
	if err != nil {
		return transfer_service.Transfer{}, false, fmt.Errorf(
			"insert transfer: %w",
			err,
		)
	}

	if err := copyTargets(ctx, tx, transfer.ID, command.Snapshot.Targets); err != nil {
		return transfer_service.Transfer{}, false, err
	}
	return transfer, true, nil
}

func copyTargets(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
	targets []transfer_service.MutationTarget,
) error {
	count, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"wb", "transfer_targets"},
		[]string{
			"transfer_id",
			"position",
			"seller_id",
			"client_generation",
			"credential_expires_at",
			"binding_revision",
			"capability_revision",
			"content_read",
			"content_write",
		},
		pgx.CopyFromSlice(len(targets), func(index int) ([]any, error) {
			target := targets[index]
			return []any{
				transferID,
				target.Position,
				string(target.SellerID),
				target.ClientGeneration[:],
				target.CredentialExpiresAt.UTC(),
				target.BindingRevision,
				target.CapabilityRevision,
				target.ContentRead,
				target.ContentWrite,
			}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("copy transfer target snapshot: %w", err)
	}
	if count != int64(len(targets)) {
		return fmt.Errorf(
			"copied %d of %d transfer targets: %w",
			count,
			len(targets),
			core_errors.ErrConflict,
		)
	}
	return nil
}

func scanTransfer(row rowScanner) (transfer_service.Transfer, error) {
	var (
		transfer      transfer_service.Transfer
		id            int64
		batchID       int64
		batchChecksum []byte
		snapshotRoot  []byte
		targetSetRoot []byte
		finishedAt    pgtype.Timestamptz
	)
	err := row.Scan(
		&id,
		&batchID,
		&transfer.Phase,
		&transfer.Outcome,
		&transfer.AttentionCode,
		&transfer.Revision,
		&batchChecksum,
		&transfer.ItemsCount,
		&transfer.GroupsCount,
		&transfer.CohortName,
		&snapshotRoot,
		&targetSetRoot,
		&transfer.TargetsCount,
		&transfer.ItemTargetsCount,
		&transfer.GroupTargetsCount,
		&transfer.CreatedAt,
		&transfer.StartedAt,
		&finishedAt,
		&transfer.UpdatedAt,
	)
	if err != nil {
		return transfer_service.Transfer{}, err
	}
	if len(batchChecksum) != len(transfer.BatchChecksum) ||
		len(snapshotRoot) != len(transfer.TargetSnapshotRevision) ||
		len(targetSetRoot) != len(transfer.TargetSetRoot) {
		return transfer_service.Transfer{}, errors.New(
			"stored transfer digest has invalid length",
		)
	}
	transfer.ID = transfer_service.TransferID(id)
	transfer.BatchID = cardpipeline.BatchID(batchID)
	if finishedAt.Valid {
		value := finishedAt.Time
		transfer.FinishedAt = &value
	}
	copy(transfer.BatchChecksum[:], batchChecksum)
	copy(transfer.TargetSnapshotRevision[:], snapshotRoot)
	copy(transfer.TargetSetRoot[:], targetSetRoot)
	if err := transfer.Validate(); err != nil {
		return transfer_service.Transfer{}, err
	}
	return transfer, nil
}
