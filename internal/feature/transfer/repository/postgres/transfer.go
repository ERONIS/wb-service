package transfer_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
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
	transfer.batch_schema_version,
	transfer.batch_normalization_version,
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

func (repository *Repository) FindByBatch(
	ctx context.Context,
	batchID cardimport_service.BatchID,
) (transfer_service.Transfer, error) {
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
	defer cancel()
	return loadByBatch(ctx, repository.pool, batchID)
}

func (repository *Repository) FindByID(
	ctx context.Context,
	transferID transfer_service.TransferID,
) (transfer_service.Transfer, error) {
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
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

func loadByBatch(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	batchID cardimport_service.BatchID,
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
			batch_schema_version,
			batch_normalization_version,
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
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (batch_id) DO NOTHING
		RETURNING
			id,
			batch_id,
			phase,
			outcome,
			COALESCE(attention_code, ''),
			revision,
			batch_schema_version,
			batch_normalization_version,
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
		command.Batch.SchemaVersion,
		command.Batch.NormalizationVersion,
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
			"cabinet_id",
			"seller_key",
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
				string(target.CabinetID),
				target.SellerKey[:],
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
		&transfer.BatchSchemaVersion,
		&transfer.BatchNormalizationVersion,
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
	transfer.BatchID = cardimport_service.BatchID(batchID)
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
