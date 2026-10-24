package transfer_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"

	"github.com/jackc/pgx/v5"
)

func (repository *Repository) ApplyPreparationResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_preparation.ApplyPreparationResultCommand,
) error {
	if tx == nil {
		return errors.New("apply preparation result: DBTX is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	transferPhase, transferOutcome, err := lockTransferForPipelineResult(ctx, tx, command.TransferID)
	if err != nil {
		return err
	}

	// 1. Блокируем строку целевой группы для атомарного чтения текущего состояния
	const selectGroupTarget = `
		SELECT
			preparation_status,
			preparation_result_revision,
			preparation_result_code,
			preparation_item_position,
			preparation_field
		FROM wb.transfer_group_targets
		WHERE transfer_id = $1 AND id = $2
		FOR UPDATE;
	`
	var (
		currentStatus       string
		currentRevision     int64
		currentResultCode   *string
		currentItemPosition *int
		currentField        *string
	)
	err = tx.QueryRow(ctx, selectGroupTarget, command.TransferID, command.GroupTargetID).Scan(
		&currentStatus,
		&currentRevision,
		&currentResultCode,
		&currentItemPosition,
		&currentField,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return transfer_service.ErrPreparationResultConflict
		}
		return fmt.Errorf("lock transfer group target: %w", err)
	}

	// 2. Проверяем терминальные состояния для идемпотентности
	if currentStatus == "succeeded" || currentStatus == "rejected" || currentStatus == "unresolved" {
		if isSamePreparationResult(
			command,
			currentStatus,
			currentRevision,
			currentResultCode,
			currentItemPosition,
			currentField,
		) {
			// Идентичный повторный вызов — идемпотентный no-op
			return nil
		}
		// Конфликтующий повторный вызов с другими данными
		return transfer_service.ErrPreparationResultConflict
	}

	// 3. Для нетерминального состояния проверяем, что группа находится в процессе подготовки
	if !preparationPipelinePhase(transferPhase) || transferOutcome != "running" || currentStatus != "running" || currentRevision != 0 {
		return transfer_service.ErrPreparationResultConflict
	}

	// 4. Если подготовка завершилась успехом — сохраняем артефакт предложения WB
	if command.Status == transfer_service.PreparationResultSucceeded && command.Artifact != nil {
		const insertArtifact = `
			INSERT INTO wb.card_preparation_artifacts (
				group_target_id,
				transfer_id,
				subject_id,
				request_payload,
				metadata_snapshot,
				limits_free,
				limits_paid,
				limits_observed_at
			)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8);
		`
		artifact := command.Artifact
		if _, err := tx.Exec(
			ctx,
			insertArtifact,
			command.GroupTargetID,
			command.TransferID,
			artifact.SubjectID,
			artifact.RequestPayload,
			string(artifact.MetadataSnapshot),
			artifact.LimitsFree,
			artifact.LimitsPaid,
			artifact.LimitsObservedAt,
		); err != nil {
			return fmt.Errorf("insert card preparation artifact: %w", err)
		}
	}

	// 5. Подготавливаем аргументы диагностики для обновления проекции
	var (
		itemPositionArg *int
		fieldArg        *string
	)
	if command.ItemPosition > 0 {
		pos := command.ItemPosition
		itemPositionArg = &pos
	}
	if command.Field != "" {
		f := command.Field
		fieldArg = &f
	}

	const updateProjection = `
		UPDATE wb.transfer_group_targets AS group_target
		SET
			preparation_status = $3,
			preparation_result_revision = $4,
			preparation_result_code = $5,
			preparation_item_position = $6,
			preparation_field = $7,
			preparation_started_at = $8::TIMESTAMPTZ,
			preparation_finished_at = $9::TIMESTAMPTZ,
			publication_status = CASE
				WHEN $3 = 'succeeded' THEN group_target.publication_status
				ELSE 'skipped'
			END,
			media_status = CASE
				WHEN $3 = 'succeeded' THEN group_target.media_status
				ELSE 'skipped'
			END,
			overall_outcome = CASE $3
				WHEN 'succeeded' THEN 'running'
				WHEN 'rejected' THEN 'rejected'
				WHEN 'unresolved' THEN 'unresolved'
			END,
			attention_code = CASE
				WHEN $3 = 'unresolved' THEN $5::VARCHAR(128)
				ELSE NULL
			END,
			finished_at = CASE
				WHEN $3 IN ('rejected', 'unresolved') THEN $9::TIMESTAMPTZ
				ELSE NULL
			END,
			revision = group_target.revision + 1,
			updated_at = CURRENT_TIMESTAMP
		WHERE group_target.transfer_id = $1
			AND group_target.id = $2
			AND group_target.preparation_status = 'running'
			AND group_target.preparation_result_revision = 0;
	`
	result, err := tx.Exec(
		ctx,
		updateProjection,
		command.TransferID,
		command.GroupTargetID,
		command.Status,
		command.Revision,
		command.OutcomeCode,
		itemPositionArg,
		fieldArg,
		command.StartedAt,
		command.FinishedAt,
	)
	if err != nil {
		return fmt.Errorf("update transfer preparation result: %w", err)
	}
	if result.RowsAffected() == 0 {
		return transfer_service.ErrPreparationResultConflict
	}

	// 6. При отклонении группы завершаем связанные дочерние товарные позиции
	if command.Status != transfer_service.PreparationResultSucceeded {
		const finishItems = `
			UPDATE wb.transfer_item_targets
			SET state = 'terminal',
			    outcome_class = CASE $3
					WHEN 'rejected' THEN 'rejected'
					WHEN 'unresolved' THEN 'unresolved'
				END,
			    outcome_code = $4,
			    finished_at = $5::TIMESTAMPTZ,
			    revision = revision + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE transfer_id = $1
				AND group_target_id = $2
				AND state = 'running';
		`
		if _, err := tx.Exec(
			ctx,
			finishItems,
			command.TransferID,
			command.GroupTargetID,
			command.Status,
			command.OutcomeCode,
			command.FinishedAt,
		); err != nil {
			return fmt.Errorf("finish transfer preparation items: %w", err)
		}
	}

	return advanceTransferAfterPreparation(ctx, tx, command.TransferID)
}

// isSamePreparationResult проверяет совпадение существующего терминального результата подготовки с командой.
func isSamePreparationResult(
	command transfer_preparation.ApplyPreparationResultCommand,
	currentStatus string,
	currentRevision int64,
	currentResultCode *string,
	currentItemPosition *int,
	currentField *string,
) bool {
	if currentStatus != string(command.Status) || currentRevision != command.Revision {
		return false
	}
	if currentResultCode == nil || *currentResultCode != command.OutcomeCode {
		return false
	}
	if command.Status == transfer_service.PreparationResultSucceeded {
		if currentItemPosition != nil || currentField != nil {
			return false
		}
	} else {
		if command.ItemPosition == 0 {
			if currentItemPosition != nil {
				return false
			}
		} else {
			if currentItemPosition == nil || *currentItemPosition != command.ItemPosition {
				return false
			}
		}
		if command.Field == "" {
			if currentField != nil {
				return false
			}
		} else {
			if currentField == nil || *currentField != command.Field {
				return false
			}
		}
	}
	return true
}

func preparationPipelinePhase(phase string) bool {
	switch phase {
	case "preparing", "awaiting_authorization", "publishing", "reconciling", "media":
		return true
	default:
		return false
	}
}

func lockTransferForPipelineResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_model.TransferID,
) (string, string, error) {
	const query = `
		SELECT phase, outcome
		FROM wb.transfers
		WHERE id = $1
		FOR UPDATE;
	`
	var phase, outcome string
	if err := tx.QueryRow(ctx, query, transferID).Scan(&phase, &outcome); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", transfer_service.ErrPreparationResultConflict
		}
		return "", "", fmt.Errorf("lock transfer pipeline projection: %w", err)
	}
	return phase, outcome, nil
}

var _ transfer_preparation.PreparationResultRepository = (*Repository)(nil)

// verifyStoredPreparationArtifact сравнивает все факты артефакта без изменения сохранённого результата.
