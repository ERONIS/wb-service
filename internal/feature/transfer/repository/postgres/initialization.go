package transfer_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/jackc/pgx/v5"
)

const initializationListPageLimit = 1000

func (repository *Repository) ListInitializing(
	ctx context.Context,
	afterID transfer_service.TransferID,
	limit int,
) ([]transfer_service.Transfer, error) {
	if afterID < 0 || limit <= 0 || limit > initializationListPageLimit {
		return nil, core_errors.ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
	defer cancel()

	query := `
		SELECT ` + transferColumns + `
		FROM wb.transfers AS transfer
		WHERE transfer.phase = 'initializing'
			AND transfer.outcome = 'running'
			AND transfer.id > $1
		ORDER BY transfer.id
		LIMIT $2;
	`
	rows, err := repository.pool.Query(ctx, query, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list initializing transfers: %w", err)
	}
	defer rows.Close()

	transfers := make([]transfer_service.Transfer, 0)
	for rows.Next() {
		transfer, err := scanTransfer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan initializing transfer: %w", err)
		}
		transfers = append(transfers, transfer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate initializing transfers: %w", err)
	}
	return transfers, nil
}

func (repository *Repository) Initialize(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_service.InitializeTransferCommand,
) error {
	if tx == nil {
		return errors.New("initialize transfer: DBTX is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}

	transfer, err := lockTransferByID(ctx, tx, command.TransferID)
	if err != nil {
		return err
	}
	if transfer.Phase != transfer_service.PhaseInitializing {
		return nil
	}
	if transfer.BatchID != command.BatchID ||
		transfer.BatchChecksum != command.BatchChecksum ||
		transfer.ItemsCount != command.ItemsCount ||
		transfer.GroupsCount != command.GroupsCount {
		return transfer_service.ErrInitializationConflict
	}

	targetIDs, err := loadInitializationTargetIDs(ctx, tx, transfer)
	if err != nil {
		return err
	}
	if err := ensureInitializationEmpty(ctx, tx, transfer.ID); err != nil {
		return err
	}

	groupTargetCount := int64(len(command.Groups)) * int64(len(targetIDs))
	itemTargetCount := int64(len(command.Items)) * int64(len(targetIDs))
	if groupTargetCount != transfer.GroupTargetsCount ||
		itemTargetCount != transfer.ItemTargetsCount ||
		groupTargetCount > maxIntValue() || itemTargetCount > maxIntValue() {
		return transfer_service.ErrInitializationConflict
	}

	groupIDs, err := reserveIDs(
		ctx,
		tx,
		"wb.transfer_groups_id_seq",
		len(command.Groups),
	)
	if err != nil {
		return err
	}
	itemIDs, err := reserveIDs(
		ctx,
		tx,
		"wb.transfer_items_id_seq",
		len(command.Items),
	)
	if err != nil {
		return err
	}
	groupTargetIDs, err := reserveIDs(
		ctx,
		tx,
		"wb.transfer_group_targets_id_seq",
		int(groupTargetCount),
	)
	if err != nil {
		return err
	}

	if err := copyInitializationGroups(ctx, tx, transfer.ID, groupIDs, command.Groups); err != nil {
		return err
	}
	if err := copyInitializationItems(
		ctx,
		tx,
		transfer.ID,
		groupIDs,
		itemIDs,
		command.Items,
	); err != nil {
		return err
	}
	if err := copyInitializationGroupTargets(
		ctx,
		tx,
		transfer.ID,
		groupIDs,
		targetIDs,
		groupTargetIDs,
	); err != nil {
		return err
	}
	if err := copyInitializationItemTargets(
		ctx,
		tx,
		transfer.ID,
		groupIDs,
		itemIDs,
		targetIDs,
		groupTargetIDs,
		command.Items,
	); err != nil {
		return err
	}
	if err := validateInitializationCounts(ctx, tx, transfer); err != nil {
		return err
	}

	result, err := tx.Exec(
		ctx,
		`UPDATE wb.transfers
		 SET phase = 'preparing', revision = revision + 1,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1
		   AND phase = 'initializing'
		   AND outcome = 'running';`,
		transfer.ID,
	)
	if err != nil {
		return fmt.Errorf("activate initialized transfer: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_service.ErrInitializationConflict
	}
	return nil
}

func (repository *Repository) MarkInitializationFailed(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
	failureCode string,
) error {
	if tx == nil {
		return errors.New("mark transfer initialization failed: DBTX is nil")
	}
	if transferID <= 0 || failureCode == "" || len(failureCode) > 128 {
		return core_errors.ErrInvalidArgument
	}
	result, err := tx.Exec(
		ctx,
		`UPDATE wb.transfers
		 SET phase = 'finished', outcome = 'failed', attention_code = $2,
		     finished_at = CURRENT_TIMESTAMP,
		     revision = revision + 1, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1
		   AND phase = 'initializing'
		   AND outcome = 'running';`,
		transferID,
		failureCode,
	)
	if err != nil {
		return fmt.Errorf("mark transfer initialization failed: %w", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}

	const load = `
		SELECT phase, outcome, COALESCE(attention_code, '')
		FROM wb.transfers
		WHERE id = $1
		FOR UPDATE;
	`
	var phase, outcome, storedCode string
	if err := tx.QueryRow(ctx, load, transferID).Scan(
		&phase,
		&outcome,
		&storedCode,
	); err != nil {
		return fmt.Errorf("load failed transfer state: %w", err)
	}
	if phase == string(transfer_service.PhaseFinished) &&
		outcome == string(transfer_service.OutcomeFailed) &&
		storedCode == failureCode {
		return nil
	}
	return transfer_service.ErrInitializationConflict
}

func lockTransferByID(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
) (transfer_service.Transfer, error) {
	query := `
		SELECT ` + transferColumns + `
		FROM wb.transfers AS transfer
		WHERE transfer.id = $1
		FOR UPDATE;
	`
	transfer, err := scanTransfer(tx.QueryRow(ctx, query, transferID))
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_service.Transfer{}, core_errors.ErrNotFound
	}
	if err != nil {
		return transfer_service.Transfer{}, fmt.Errorf("lock transfer for initialization: %w", err)
	}
	return transfer, nil
}

func loadInitializationTargetIDs(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transfer transfer_service.Transfer,
) ([]int64, error) {
	const query = `
		SELECT id
		FROM wb.transfer_targets
		WHERE transfer_id = $1
		ORDER BY position;
	`
	rows, err := tx.Query(ctx, query, transfer.ID)
	if err != nil {
		return nil, fmt.Errorf("load transfer targets for initialization: %w", err)
	}
	defer rows.Close()
	targetIDs := make([]int64, 0, transfer.TargetsCount)
	for rows.Next() {
		var targetID int64
		if err := rows.Scan(&targetID); err != nil {
			return nil, fmt.Errorf("scan transfer target for initialization: %w", err)
		}
		targetIDs = append(targetIDs, targetID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transfer targets for initialization: %w", err)
	}
	if len(targetIDs) != transfer.TargetsCount {
		return nil, transfer_service.ErrInitializationConflict
	}
	return targetIDs, nil
}

func ensureInitializationEmpty(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
) error {
	const query = `
		SELECT
			(SELECT COUNT(*) FROM wb.transfer_groups WHERE transfer_id = $1),
			(SELECT COUNT(*) FROM wb.transfer_items WHERE transfer_id = $1),
			(SELECT COUNT(*) FROM wb.transfer_group_targets WHERE transfer_id = $1),
			(SELECT COUNT(*) FROM wb.transfer_item_targets WHERE transfer_id = $1);
	`
	var groups, items, groupTargets, itemTargets int64
	if err := tx.QueryRow(ctx, query, transferID).Scan(
		&groups,
		&items,
		&groupTargets,
		&itemTargets,
	); err != nil {
		return fmt.Errorf("inspect transfer initialization state: %w", err)
	}
	if groups != 0 || items != 0 || groupTargets != 0 || itemTargets != 0 {
		return transfer_service.ErrInitializationConflict
	}
	return nil
}

func reserveIDs(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	sequence string,
	count int,
) ([]int64, error) {
	if count <= 0 {
		return nil, transfer_service.ErrInitializationConflict
	}
	const query = `
		SELECT nextval($1::regclass)
		FROM generate_series(1, $2);
	`
	rows, err := tx.Query(ctx, query, sequence, count)
	if err != nil {
		return nil, fmt.Errorf("reserve transfer initialization IDs: %w", err)
	}
	defer rows.Close()
	ids := make([]int64, 0, count)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan reserved transfer initialization ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reserved transfer initialization IDs: %w", err)
	}
	if len(ids) != count {
		return nil, transfer_service.ErrInitializationConflict
	}
	return ids, nil
}

func copyInitializationGroups(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
	groupIDs []int64,
	groups []transfer_service.InitializationGroup,
) error {
	count, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"wb", "transfer_groups"},
		[]string{"id", "transfer_id", "source_group_key", "source_file_id", "source_group_name"},
		pgx.CopyFromSlice(len(groups), func(index int) ([]any, error) {
			group := groups[index]
			return []any{
				groupIDs[index],
				transferID,
				group.SourceGroupKey[:],
				group.SourceFileID,
				group.SourceGroupName,
			}, nil
		}),
	)
	return exactCopied("transfer groups", count, len(groups), err)
}

func copyInitializationItems(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
	groupIDs []int64,
	itemIDs []int64,
	items []transfer_service.InitializationItem,
) error {
	count, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"wb", "transfer_items"},
		[]string{
			"id", "transfer_id", "batch_item_id", "position", "source_file_id",
			"source_rows", "source_group_id", "vendor_code", "payload",
		},
		pgx.CopyFromSlice(len(items), func(index int) ([]any, error) {
			item := items[index]
			sourceRows := make([]int32, len(item.SourceRows))
			for rowIndex, row := range item.SourceRows {
				sourceRows[rowIndex] = int32(row)
			}
			return []any{
				itemIDs[index], transferID, item.BatchItemID, item.Position,
				item.SourceFileID, sourceRows, groupIDs[item.GroupPosition],
				item.VendorCode, item.Payload,
			}, nil
		}),
	)
	return exactCopied("transfer items", count, len(items), err)
}

func copyInitializationGroupTargets(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
	groupIDs []int64,
	targetIDs []int64,
	groupTargetIDs []int64,
) error {
	targetCount := len(targetIDs)
	count, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"wb", "transfer_group_targets"},
		[]string{"id", "transfer_id", "source_group_id", "target_id"},
		pgx.CopyFromSlice(len(groupTargetIDs), func(index int) ([]any, error) {
			groupPosition := index / targetCount
			targetPosition := index % targetCount
			return []any{
				groupTargetIDs[index],
				transferID,
				groupIDs[groupPosition],
				targetIDs[targetPosition],
			}, nil
		}),
	)
	return exactCopied("transfer group targets", count, len(groupTargetIDs), err)
}

func copyInitializationItemTargets(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
	groupIDs []int64,
	itemIDs []int64,
	targetIDs []int64,
	groupTargetIDs []int64,
	items []transfer_service.InitializationItem,
) error {
	targetCount := len(targetIDs)
	rowCount := len(items) * targetCount
	count, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"wb", "transfer_item_targets"},
		[]string{"transfer_id", "source_group_id", "transfer_item_id", "group_target_id"},
		pgx.CopyFromSlice(rowCount, func(index int) ([]any, error) {
			itemPosition := index / targetCount
			targetPosition := index % targetCount
			groupPosition := items[itemPosition].GroupPosition
			groupTargetPosition := groupPosition*targetCount + targetPosition
			return []any{
				transferID,
				groupIDs[groupPosition],
				itemIDs[itemPosition],
				groupTargetIDs[groupTargetPosition],
			}, nil
		}),
	)
	return exactCopied("transfer item targets", count, rowCount, err)
}

func exactCopied(name string, count int64, expected int, err error) error {
	if err != nil {
		return fmt.Errorf("copy %s: %w", name, err)
	}
	if count != int64(expected) {
		return fmt.Errorf(
			"copied %d of %d %s: %w",
			count,
			expected,
			name,
			transfer_service.ErrInitializationConflict,
		)
	}
	return nil
}

func validateInitializationCounts(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transfer transfer_service.Transfer,
) error {
	const query = `
		SELECT
			(SELECT COUNT(*) FROM wb.transfer_groups WHERE transfer_id = $1),
			(SELECT COUNT(*) FROM wb.transfer_items WHERE transfer_id = $1),
			(SELECT COUNT(*) FROM wb.transfer_group_targets WHERE transfer_id = $1),
			(SELECT COUNT(*) FROM wb.transfer_item_targets WHERE transfer_id = $1);
	`
	var groups, items, groupTargets, itemTargets int64
	if err := tx.QueryRow(ctx, query, transfer.ID).Scan(
		&groups,
		&items,
		&groupTargets,
		&itemTargets,
	); err != nil {
		return fmt.Errorf("validate transfer initialization counts: %w", err)
	}
	if groups != int64(transfer.GroupsCount) ||
		items != int64(transfer.ItemsCount) ||
		groupTargets != transfer.GroupTargetsCount ||
		itemTargets != transfer.ItemTargetsCount {
		return transfer_service.ErrInitializationConflict
	}
	return nil
}

func maxIntValue() int64 {
	return int64(^uint(0) >> 1)
}

var _ transfer_service.Repository = (*Repository)(nil)
