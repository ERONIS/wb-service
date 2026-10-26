package transfer_postgres_workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_workflow "github.com/ERONIS/wb-service/internal/feature/transfer/service/workflow"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const transferColumns = `
	transfer.id,
	transfer.batch_id,
	transfer.phase,
	transfer.outcome,
	COALESCE(transfer.attention_code, ''),
	transfer.items_count,
	transfer.groups_count,
	transfer.targets_count,
	transfer.item_targets_count,
	transfer.group_targets_count,
	transfer.created_at,
	transfer.started_at,
	transfer.finished_at,
	transfer.updated_at
`

// ListTransfers выбирает страницу переносов по внутреннему статическому условию.
func (repository *WorkflowRepository) ListTransfers(
	ctx context.Context,
	afterID transfer_model.TransferID,
	limit int,
	condition string,
	operation string,
) ([]transfer_model.Transfer, error) {
	if err := core_errors.RequireContext(ctx); err != nil {
		return nil, err
	}
	if afterID < 0 || limit <= 0 || limit > MaxTransferPageSize {
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

	transfers := make([]transfer_model.Transfer, 0)
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

func (repository *WorkflowRepository) FindByBatch(
	ctx context.Context,
	batchID cardpipeline.BatchID,
) (transfer_model.Transfer, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	return loadByBatch(ctx, repository.pool, batchID)
}

func (repository *WorkflowRepository) FindByID(
	ctx context.Context,
	transferID transfer_model.TransferID,
) (transfer_model.Transfer, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	query := `
		SELECT ` + transferColumns + `
		FROM wb.transfers AS transfer
		WHERE transfer.id = $1;
	`
	transfer, err := scanTransfer(repository.pool.QueryRow(ctx, query, transferID))
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_model.Transfer{}, fmt.Errorf(
			"transfer ID='%d': %w",
			transferID,
			core_errors.ErrNotFound,
		)
	}
	if err != nil {
		return transfer_model.Transfer{}, fmt.Errorf(
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
	batchID cardpipeline.BatchID,
) (transfer_model.Transfer, error) {
	query := `
		SELECT ` + transferColumns + `
		FROM wb.transfers AS transfer
		WHERE transfer.batch_id = $1;
	`
	transfer, err := scanTransfer(db.QueryRow(ctx, query, batchID))
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_model.Transfer{}, fmt.Errorf(
			"transfer for batch ID='%d': %w",
			batchID,
			core_errors.ErrNotFound,
		)
	}
	if err != nil {
		return transfer_model.Transfer{}, fmt.Errorf(
			"scan transfer for batch ID='%d': %w",
			batchID,
			err,
		)
	}
	return transfer, nil
}

func (repository *WorkflowRepository) Create(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_workflow.CreateTransfer,
) (transfer_model.Transfer, error) {
	if tx == nil {
		return transfer_model.Transfer{}, errors.New(
			"create transfer: DBTX is nil",
		)
	}

	const query = `
		INSERT INTO wb.transfers (
			batch_id,
			items_count,
			groups_count,
			targets_count,
			item_targets_count,
			group_targets_count
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (batch_id) DO NOTHING
		RETURNING
			id,
			batch_id,
			phase,
			outcome,
			COALESCE(attention_code, ''),
			items_count,
			groups_count,
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
		command.Batch.ItemsCount,
		command.Batch.GroupsCount,
		command.Capacity.Targets,
		command.Capacity.ItemTargets,
		command.Capacity.GroupTargets,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := loadByBatch(ctx, tx, command.Batch.ID)
		return existing, loadErr
	}
	if err != nil {
		return transfer_model.Transfer{}, fmt.Errorf(
			"insert transfer: %w",
			err,
		)
	}

	if err := copyTargets(ctx, tx, transfer.ID, command.Targets); err != nil {
		return transfer_model.Transfer{}, err
	}
	return transfer, nil
}

func copyTargets(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_model.TransferID,
	targets []transfer_model.MutationTarget,
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
				int64(target.ClientGeneration),
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

func scanTransfer(row pgx.Row) (transfer_model.Transfer, error) {
	var (
		transfer   transfer_model.Transfer
		id         int64
		batchID    int64
		finishedAt pgtype.Timestamptz
	)
	err := row.Scan(
		&id,
		&batchID,
		&transfer.Phase,
		&transfer.Outcome,
		&transfer.AttentionCode,
		&transfer.ItemsCount,
		&transfer.GroupsCount,
		&transfer.TargetsCount,
		&transfer.ItemTargetsCount,
		&transfer.GroupTargetsCount,
		&transfer.CreatedAt,
		&transfer.StartedAt,
		&finishedAt,
		&transfer.UpdatedAt,
	)
	if err != nil {
		return transfer_model.Transfer{}, err
	}
	transfer.ID = transfer_model.TransferID(id)
	transfer.BatchID = cardpipeline.BatchID(batchID)
	if finishedAt.Valid {
		value := finishedAt.Time
		transfer.FinishedAt = &value
	}
	if err := transfer.Validate(); err != nil {
		return transfer_model.Transfer{}, err
	}
	return transfer, nil
}
