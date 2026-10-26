package transfer_postgres_repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_postgres_repository "github.com/ERONIS/wb-service/internal/feature/cardpublication/repository/postgres"
	cardpublication_execution "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/execution"
	transfer_postgres_repository "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres"
	transfer_authorization "github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
	transfer_publication "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
)

// TestPostgres_ActiveTransferWaitsForEveryCabinetGroupTarget проверяет завершение переноса только после всех целей группы.
func TestPostgres_ActiveTransferWaitsForEveryCabinetGroupTarget(t *testing.T) {
	pool, uow := getTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	transferID, firstGroupTargetID, secondGroupTargetID, _, _ := setupTestTransferData(
		t, ctx, pool, "phase_terminal",
	)
	repository := transfer_postgres_repository.New(pool)

	applyRejection := func(groupTargetID int64) {
		t.Helper()
		finishedAt := time.Now().UTC()
		command := transfer_preparation.ApplyPreparationResultCommand{
			TransferID:    transferID,
			GroupTargetID: groupTargetID,
			Status:        transfer_preparation.PreparationResultRejected,
			OutcomeCode:   "invalid_card",
			StartedAt:     finishedAt.Add(-time.Second),
			FinishedAt:    finishedAt,
		}
		if err := uow.WithinTransaction(
			ctx,
			func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
				return repository.ApplyPreparationResult(ctx, tx, command)
			},
		); err != nil {
			t.Fatalf("применить отказ подготовки цели группы %d: %v", groupTargetID, err)
		}
	}

	assertTransfer := func(wantPhase, wantOutcome string, wantTerminalCount int64) {
		t.Helper()
		var (
			phase          string
			outcome        string
			finishedAt     *time.Time
			terminalGroups int64
			terminalItems  int64
		)
		err := pool.QueryRow(ctx, `
			SELECT transfer.phase, transfer.outcome, transfer.finished_at,
				(SELECT count(*) FROM wb.transfer_group_targets AS group_target
				 WHERE group_target.transfer_id = transfer.id
				   AND group_target.overall_outcome <> 'running'),
				(SELECT count(*) FROM wb.transfer_item_targets AS item_target
				 WHERE item_target.transfer_id = transfer.id
				   AND item_target.state = 'terminal')
			FROM wb.transfers AS transfer
			WHERE transfer.id = $1;
		`, transferID).Scan(&phase, &outcome, &finishedAt, &terminalGroups, &terminalItems)
		if err != nil {
			t.Fatalf("прочитать состояние переноса: %v", err)
		}
		if phase != wantPhase || outcome != wantOutcome ||
			terminalGroups != wantTerminalCount || terminalItems != wantTerminalCount {
			t.Fatalf(
				"состояние переноса: phase=%s outcome=%s групп=%d карточек=%d; ожидалось %s/%s и %d терминальных целей",
				phase, outcome, terminalGroups, terminalItems,
				wantPhase, wantOutcome, wantTerminalCount,
			)
		}
		if (finishedAt != nil) != (wantPhase == "finished") {
			t.Fatalf("время завершения переноса не соответствует фазе %s: %v", phase, finishedAt)
		}
	}

	applyRejection(firstGroupTargetID)
	assertTransfer("active", "running", 1)

	applyRejection(secondGroupTargetID)
	assertTransfer("finished", "rejected", 2)
}

// TestPostgres_MediaOnlyPlanFinishesTransfer проверяет план с одной медиаоперацией для уже существующей карточки.
func TestPostgres_MediaOnlyPlanFinishesTransfer(t *testing.T) {
	testPostgresMediaPlanFinishesTransfer(t, false)
}

// TestPostgres_ProductThenMediaPlanFinishesTransfer проверяет выбор медиа после завершённого товарного действия.
func TestPostgres_ProductThenMediaPlanFinishesTransfer(t *testing.T) {
	testPostgresMediaPlanFinishesTransfer(t, true)
}

func testPostgresMediaPlanFinishesTransfer(t *testing.T, withProductAction bool) {
	pool, uow := getTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	transferID, firstGroupTargetID, secondGroupTargetID, firstTargetID, secondTargetID := setupTestTransferData(
		t, ctx, pool, "phase_media_plan",
	)
	mediaGroupTargetID, rejectedGroupTargetID := firstGroupTargetID, secondGroupTargetID
	mediaTargetID := firstTargetID
	if withProductAction {
		mediaGroupTargetID, rejectedGroupTargetID = secondGroupTargetID, firstGroupTargetID
		mediaTargetID = secondTargetID
	}
	transferRepository := transfer_postgres_repository.New(pool)
	publicationRepository := cardpublication_postgres_repository.New(pool)
	finishedAt := time.Now().UTC()

	for _, command := range []transfer_preparation.ApplyPreparationResultCommand{
		{
			TransferID:    transferID,
			GroupTargetID: rejectedGroupTargetID,
			Status:        transfer_preparation.PreparationResultRejected,
			OutcomeCode:   "invalid_card",
			StartedAt:     finishedAt.Add(-time.Second),
			FinishedAt:    finishedAt,
		},
		{
			TransferID:    transferID,
			GroupTargetID: mediaGroupTargetID,
			Status:        transfer_preparation.PreparationResultSucceeded,
			OutcomeCode:   "prepared",
			StartedAt:     finishedAt.Add(-time.Second),
			FinishedAt:    finishedAt,
			Artifact: &transfer_preparation.PreparationArtifact{
				SubjectID:        1,
				RequestPayload:   []byte(`[{"subjectID":1,"variants":[{"vendorCode":"ART-1"}]}]`),
				MetadataSnapshot: preparationSnapshot(t, finishedAt, 1, 0),
				LimitsFree:       1,
				LimitsObservedAt: finishedAt,
			},
		},
	} {
		if err := uow.WithinTransaction(
			ctx,
			func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
				return transferRepository.ApplyPreparationResult(ctx, tx, command)
			},
		); err != nil {
			t.Fatalf("подготовить цель группы %d: %v", command.GroupTargetID, err)
		}
	}

	var sellerID string
	if err := pool.QueryRow(ctx, `
		SELECT seller_id FROM wb.transfer_targets
		WHERE transfer_id = $1 AND id = $2;
	`, transferID, mediaTargetID).Scan(&sellerID); err != nil {
		t.Fatalf("прочитать кабинет для медиа: %v", err)
	}
	var itemTargetID int64
	if err := pool.QueryRow(ctx, `
		SELECT id FROM wb.transfer_item_targets
		WHERE transfer_id = $1 AND group_target_id = $2;
	`, transferID, mediaGroupTargetID).Scan(&itemTargetID); err != nil {
		t.Fatalf("прочитать карточку для медиа: %v", err)
	}

	var planID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.publication_plans (transfer_id, state, started_at)
		VALUES ($1, 'executing', CURRENT_TIMESTAMP)
		RETURNING id;
	`, transferID).Scan(&planID); err != nil {
		t.Fatalf("создать медиа-план: %v", err)
	}
	var preflightObservationID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.publication_observations (
			transfer_id, target_id, seller_id, kind,
			normal_count, trash_count, snapshot, observed_at
		) VALUES ($1, $2, $3, 'normal_trash_preflight', 1, 0, '{}'::jsonb, CURRENT_TIMESTAMP)
		RETURNING id;
	`, transferID, mediaTargetID, sellerID).Scan(&preflightObservationID); err != nil {
		t.Fatalf("создать предварительное наблюдение: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO wb.product_identities (
			seller_id, vendor_code_key, normalization_version, state, nm_id
		) VALUES ($1, 'ART-1', 1, 'remote_present', 12345);
	`, sellerID); err != nil {
		t.Fatalf("сохранить идентификатор существующей карточки: %v", err)
	}
	var authorizationID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_live_authorizations (
			transfer_id, plan_id, state, trusted_actor_id, trusted_actor_name,
			requested_at, approved_at, expires_at
		) VALUES (
			$1, $2, 'authorized', 1, 'Интеграционный тест',
			CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP + INTERVAL '1 day'
		) RETURNING id;
	`, transferID, planID).Scan(&authorizationID); err != nil {
		t.Fatalf("разрешить медиа-план: %v", err)
	}
	publicationStatus := "skipped"
	if withProductAction {
		publicationStatus = "succeeded"
	}
	if _, err := pool.Exec(ctx, `
		UPDATE wb.transfer_group_targets
		SET publication_plan_id = $2, publication_status = $3,
		    media_status = 'running', revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1;
	`, mediaGroupTargetID, planID, publicationStatus); err != nil {
		t.Fatalf("перевести группу на медиа: %v", err)
	}
	var productActionID int64
	if withProductAction {
		if err := pool.QueryRow(ctx, `
			INSERT INTO wb.publication_actions (
				transfer_id, plan_id, target_id, position, kind,
				state, outcome_class, outcome_code, request_payload,
				authorization_id, started_at, finished_at
			) VALUES (
				$1, $2, $3, 1, 'create_group',
				'terminal', 'success', 'PRODUCT_PUBLISHED', $4,
				$5, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
			) RETURNING id;
		`, transferID, planID, mediaTargetID,
			[]byte(`{"cards":[{"vendorCode":"ART-1"}]}`), authorizationID,
		).Scan(&productActionID); err != nil {
			t.Fatalf("создать завершённое товарное действие: %v", err)
		}
		var productMemberID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO wb.publication_action_members (
				transfer_id, action_id, target_id, group_target_id,
				transfer_item_target_id, request_member_index, vendor_code,
				outcome_class, outcome_code, nm_id, finished_at
			) VALUES (
				$1, $2, $3, $4, $5, 0, 'ART-1',
				'success', 'product_published', 12345, CURRENT_TIMESTAMP
			) RETURNING id;
		`, transferID, productActionID, mediaTargetID,
			mediaGroupTargetID, itemTargetID,
		).Scan(&productMemberID); err != nil {
			t.Fatalf("создать участника товарного действия: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO wb.publication_attributions (
				transfer_id, group_target_id, action_id, action_member_id,
				seller_id, nm_id, preflight_observation_id,
				level, direct_correlation_key, safe_reason_code
			) VALUES (
				$1, $2, $3, $4, $5, 12345, $6,
				'direct', 'ART-1', 'product_confirmed'
			);
		`, transferID, mediaGroupTargetID, productActionID,
			productMemberID, sellerID, preflightObservationID,
		); err != nil {
			t.Fatalf("связать созданный товар с действием: %v", err)
		}
	}
	var sourceActionID any
	itemOutcomeClass := "skipped"
	itemOutcomeCode := "remote_present"
	if withProductAction {
		sourceActionID = productActionID
		itemOutcomeClass = "success"
		itemOutcomeCode = "product_published"
	}
	if _, err := pool.Exec(ctx, `
		UPDATE wb.transfer_item_targets
		SET state = 'terminal', outcome_class = $2,
		    outcome_code = $3, nm_id = 12345,
		    source_action_id = $4,
		    finished_at = CURRENT_TIMESTAMP, revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1;
	`, itemTargetID, itemOutcomeClass, itemOutcomeCode, sourceActionID); err != nil {
		t.Fatalf("завершить товарную часть карточки: %v", err)
	}
	mediaActionPosition := 1
	if withProductAction {
		mediaActionPosition = 2
	}
	var actionID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.publication_actions (
			transfer_id, plan_id, target_id, position, kind, request_payload
		) VALUES ($1, $2, $3, $4, 'upload_media', $5)
		RETURNING id;
	`, transferID, planID, mediaTargetID, mediaActionPosition,
		[]byte(`{"links":["https://example.com/photo.jpg"]}`),
	).Scan(&actionID); err != nil {
		t.Fatalf("создать медиа-действие: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO wb.publication_action_members (
			transfer_id, action_id, target_id, group_target_id,
			transfer_item_target_id, request_member_index, vendor_code
		) VALUES ($1, $2, $3, $4, $5, 0, 'ART-1');
	`, transferID, actionID, mediaTargetID, mediaGroupTargetID, itemTargetID); err != nil {
		t.Fatalf("создать участника медиа-действия: %v", err)
	}

	var phase, outcome string
	if err := pool.QueryRow(ctx, `
		SELECT phase, outcome FROM wb.transfers WHERE id = $1;
	`, transferID).Scan(&phase, &outcome); err != nil {
		t.Fatalf("прочитать перенос во время медиа: %v", err)
	}
	if phase != "active" || outcome != "running" {
		t.Fatalf("перенос завершился до медиа-результата: %s/%s", phase, outcome)
	}

	mediaCommand := transfer_publication.ApplyPublicationMediaResultCommand{
		TransferID:    transferID,
		PlanID:        planID,
		ActionID:      actionID,
		GroupTargetID: mediaGroupTargetID,
		OutcomeClass:  transfer_model.ResultSkipped,
		OutcomeCode:   "MEDIA_SKIPPED",
	}
	err := uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		return transferRepository.ApplyPublicationMediaResult(ctx, tx, mediaCommand)
	})
	if !errors.Is(err, transfer_preparation.ErrPreparationResultConflict) {
		t.Fatalf("незавершённое медиа-действие должно блокировать результат, получено: %v", err)
	}

	candidates, err := publicationRepository.ListDispatchableMediaActions(ctx, 0, 100)
	if err != nil {
		t.Fatalf("найти доступные медиа-действия: %v", err)
	}
	pendingCandidates, err := publicationRepository.ListPendingMediaActions(ctx, 0, 100)
	if err != nil {
		t.Fatalf("найти ожидающие медиа-действия: %v", err)
	}
	for _, selection := range []struct {
		name       string
		candidates []cardpublication_execution.ProductActionCandidate
	}{
		{name: "доступных", candidates: candidates},
		{name: "ожидающих", candidates: pendingCandidates},
	} {
		found := 0
		for _, listed := range selection.candidates {
			if listed.TransferID == transferID && listed.ActionID == actionID &&
				listed.PlanID == planID && int64(listed.AuthorizationID) == authorizationID {
				found++
			}
		}
		if found != 1 {
			t.Errorf("медиа-действие встретилось в списке %s %d раз вместо одного", selection.name, found)
		}
	}

	candidate := cardpublication_execution.ProductActionCandidate{
		TransferID:            transferID,
		PlanID:                planID,
		ActionID:              actionID,
		TargetID:              mediaTargetID,
		SellerID:              core_domain.SellerID(sellerID),
		AuthorizationID:       transfer_authorization.LiveAuthorizationID(authorizationID),
		AuthorizationRevision: 0,
	}
	resultErr := uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		action, err := publicationRepository.LockMediaAction(ctx, tx, candidate)
		if err != nil {
			return err
		}
		if withProductAction && action.AttributionID <= 0 {
			return errors.New("медиа-действие потеряло привязку к завершённому товарному действию")
		}
		if !withProductAction && action.AttributionID != 0 {
			return errors.New("медиа-действие существующей карточки получило чужую привязку")
		}
		result, err := publicationRepository.FinishMediaWithoutAttempt(ctx, tx, action, "already_has_photos")
		if err != nil {
			return err
		}
		if !result.Terminal || result.OutcomeClass != transfer_model.ResultSkipped {
			return errors.New("медиа-группа не получила терминальный результат")
		}
		mediaCommand.OutcomeClass = result.OutcomeClass
		mediaCommand.OutcomeCode = result.OutcomeCode
		return transferRepository.ApplyPublicationMediaResult(ctx, tx, mediaCommand)
	})
	if resultErr != nil {
		t.Fatalf("завершить медиа-план и перенос: %v", resultErr)
	}

	var finalPhase, finalOutcome, mediaStatus, planState string
	if err := pool.QueryRow(ctx, `
		SELECT transfer.phase, transfer.outcome, group_target.media_status, plan.state
		FROM wb.transfers AS transfer
		JOIN wb.transfer_group_targets AS group_target
		  ON group_target.transfer_id = transfer.id AND group_target.id = $2
		JOIN wb.publication_plans AS plan
		  ON plan.transfer_id = transfer.id AND plan.id = $3
		WHERE transfer.id = $1;
	`, transferID, mediaGroupTargetID, planID).Scan(
		&finalPhase, &finalOutcome, &mediaStatus, &planState,
	); err != nil {
		t.Fatalf("прочитать итог медиа-плана: %v", err)
	}
	if finalPhase != "finished" || finalOutcome != "partial" ||
		mediaStatus != "skipped" || planState != "terminal" {
		t.Fatalf(
			"неверный итог медиа-плана: phase=%s outcome=%s media=%s plan=%s",
			finalPhase, finalOutcome, mediaStatus, planState,
		)
	}
}
