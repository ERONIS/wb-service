package transfer_postgres_repository_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardprepare_model "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
	transfer_postgres_repository "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
	transfer_workflow "github.com/ERONIS/wb-service/internal/feature/transfer/service/workflow"
)

type initializationTargetSnapshot struct {
	groupsCount int64
	groupsReady int64
	groupsJSON  string
	itemsCount  int64
	itemsReady  int64
	itemsJSON   string
}

type temporarilyUnavailablePreparer struct {
	calls atomic.Int64
}

func (preparer *temporarilyUnavailablePreparer) PrepareTarget(
	context.Context,
	domain.SellerID,
	cardprepare_model.SourceGroup,
) (cardprepare_model.Result, error) {
	preparer.calls.Add(1)
	return cardprepare_model.Result{}, fmt.Errorf("каталог временно недоступен")
}

// TestPostgres_InitializeStartsPreparationBeforeFirstPoll проверяет готовность целей после инициализации и отсутствие повторной активации при опросах.
func TestPostgres_InitializeStartsPreparationBeforeFirstPoll(t *testing.T) {
	pool, uow := getTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	var batchID, batchItemID, transferID int64
	sessionID := time.Now().UnixNano()
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.card_batches (
			source_session_id, author_telegram_id, finalized_from_revision,
			finalize_idempotency_key, items_count, groups_count
		) VALUES ($1, $1, 0, 'initialization-integration', 1, 1)
		RETURNING id;
	`, sessionID).Scan(&batchID); err != nil {
		t.Fatalf("создание пачки: %v", err)
	}
	const groupNumber = 1
	const cardJSON = `{"sourceRows":[1],"group":"Группа 1","variant":{"vendorCode":"ART-1"}}`
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.card_batch_items (
			batch_id, position, source_group_number, payload
		) VALUES ($1, 1, $2, $3::jsonb)
		RETURNING id;
	`, batchID, groupNumber, cardJSON).Scan(&batchItemID); err != nil {
		t.Fatalf("создание карточки пачки: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE wb.card_batches SET sealed = TRUE WHERE id = $1;`, batchID); err != nil {
		t.Fatalf("фиксация пачки: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.transfers (
			batch_id, phase, outcome, items_count, groups_count,
			targets_count, item_targets_count, group_targets_count
		) VALUES ($1, 'initializing', 'running', 1, 1, 2, 2, 2)
		RETURNING id;
	`, batchID).Scan(&transferID); err != nil {
		t.Fatalf("создание переноса: %v", err)
	}
	clientGeneration := int64(1)
	if _, err := pool.Exec(ctx, `
		INSERT INTO wb.transfer_targets (
			transfer_id, position, seller_id, client_generation,
			credential_expires_at, binding_revision, capability_revision,
			content_read, content_write
		) VALUES
			($1, 1, '00000000-0000-0000-0000-000000000001', $2, CURRENT_TIMESTAMP + interval '1 day', 1, 1, TRUE, TRUE),
			($1, 2, '00000000-0000-0000-0000-000000000002', $2, CURRENT_TIMESTAMP + interval '1 day', 1, 1, TRUE, TRUE);
	`, transferID, clientGeneration); err != nil {
		t.Fatalf("создание кабинетов переноса: %v", err)
	}

	command := transfer_workflow.InitializeTransferCommand{
		TransferID:  transfer_model.TransferID(transferID),
		BatchID:     cardpipeline.BatchID(batchID),
		ItemsCount:  1,
		GroupsCount: 1,
		Groups:      []transfer_workflow.InitializationGroup{{SourceGroupNumber: groupNumber, SourceGroupName: "Группа 1"}},
		Items: []transfer_workflow.InitializationItem{{
			BatchItemID: cardpipeline.BatchItemID(batchItemID),
			Position:    1,
			VendorCode:  "ART-1",
			Payload:     cardJSON,
		}},
	}
	if _, err := pool.Exec(ctx, `DELETE FROM wb.card_batch_items WHERE batch_id = $1;`, batchID); err == nil {
		t.Fatal("удаление исходной карточки до активной передачи должно быть запрещено")
	}
	repository := transfer_postgres_repository.New(pool)
	ready, err := repository.GetReadyBatch(ctx, command.BatchID)
	if err != nil {
		t.Fatalf("повторное чтение пачки при initializing: %v", err)
	}
	if ready.Header.ID != command.BatchID || len(ready.Items) != 1 || ready.Items[0].ID != command.Items[0].BatchItemID {
		t.Fatalf("пачка после перезапуска прочитана неверно: %+v", ready)
	}

	injectedFailure := errors.New("прерванная инициализация")
	err = uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		activated, err := repository.Initialize(ctx, tx, command)
		if err != nil {
			return err
		}
		if !activated {
			return errors.New("ожидалась активация переноса до отката")
		}
		var sourceCount, projectedCount int64
		if err := tx.QueryRow(ctx, `
			SELECT (SELECT COUNT(*) FROM wb.card_batch_items WHERE batch_id = $1),
			       (SELECT COUNT(*) FROM wb.transfer_items WHERE transfer_id = $2);
		`, batchID, transferID).Scan(&sourceCount, &projectedCount); err != nil {
			return err
		}
		if sourceCount != 0 || projectedCount != 1 {
			return fmt.Errorf("перед откатом ожидается переданная карточка: source=%d projected=%d", sourceCount, projectedCount)
		}
		return injectedFailure
	})
	if !errors.Is(err, injectedFailure) {
		t.Fatalf("ожидался откат после передачи карточки: %v", err)
	}
	var sourceCount, projectedCount int64
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM wb.card_batch_items WHERE batch_id = $1),
		       (SELECT COUNT(*) FROM wb.transfer_items WHERE transfer_id = $2);
	`, batchID, transferID).Scan(&sourceCount, &projectedCount); err != nil {
		t.Fatalf("чтение карточек после отката: %v", err)
	}
	if sourceCount != 1 || projectedCount != 0 {
		t.Fatalf("откат оставил неполную передачу: source=%d projected=%d", sourceCount, projectedCount)
	}
	ready, err = transfer_postgres_repository.New(pool).GetReadyBatch(ctx, command.BatchID)
	if err != nil || len(ready.Items) != 1 {
		t.Fatalf("после отката исходная пачка недоступна для повтора: items=%d err=%v", len(ready.Items), err)
	}
	var activated bool
	if err := uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		var err error
		activated, err = repository.Initialize(ctx, tx, command)
		return err
	}); err != nil {
		t.Fatalf("инициализация переноса: %v", err)
	}
	if !activated {
		t.Fatal("перенос не был активирован")
	}

	var phase, outcome string
	if err := pool.QueryRow(ctx, `SELECT phase, outcome FROM wb.transfers WHERE id = $1;`, transferID).Scan(&phase, &outcome); err != nil {
		t.Fatalf("чтение переноса после инициализации: %v", err)
	}
	if phase != "active" || outcome != "running" {
		t.Fatalf("ожидалась активная подготовка: phase=%q outcome=%q", phase, outcome)
	}
	var headerCount, copiedCount, correctCopyCount int64
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM wb.card_batches WHERE id = $1 AND sealed),
		       (SELECT COUNT(*) FROM wb.card_batch_items WHERE batch_id = $1),
		       (SELECT COUNT(*) FROM wb.transfer_items AS item
		        JOIN wb.transfer_groups AS group_row ON group_row.id = item.source_group_id
		        WHERE item.transfer_id = $2
		          AND item.batch_item_id = $3
		          AND item.position = 1
		          AND item.vendor_code = 'ART-1'
		          AND item.payload = $4::jsonb
		          AND group_row.source_group_number = $5);
	`, batchID, transferID, batchItemID, cardJSON, groupNumber).Scan(&headerCount, &sourceCount, &correctCopyCount); err != nil {
		t.Fatalf("чтение переданной карточки: %v", err)
	}
	if headerCount != 1 || sourceCount != 0 || correctCopyCount != 1 {
		t.Fatalf("карточка передана неверно: header=%d source=%d copied=%d", headerCount, sourceCount, correctCopyCount)
	}
	if err := uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		var err error
		activated, err = transfer_postgres_repository.New(pool).Initialize(ctx, tx, command)
		return err
	}); err != nil {
		t.Fatalf("повторный Initialize после передачи: %v", err)
	}
	if activated {
		t.Fatal("повторный Initialize ошибочно сообщил об активации")
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM wb.transfer_items WHERE transfer_id = $1;`, transferID).Scan(&copiedCount); err != nil {
		t.Fatalf("чтение карточек после повтора: %v", err)
	}
	if copiedCount != 1 {
		t.Fatalf("повторная инициализация создала дубликаты: %d", copiedCount)
	}
	reused, err := transfer_postgres_repository.New(pool).FindByBatch(ctx, command.BatchID)
	if err != nil || reused.ID != command.TransferID {
		t.Fatalf("повторное обнаружение пачки не нашло существующий перенос: transfer=%d err=%v", reused.ID, err)
	}
	header, err := transfer_postgres_repository.New(pool).GetReadyBatchHeader(ctx, command.BatchID)
	if err != nil || header.ID != command.BatchID {
		t.Fatalf("после передачи должен остаться заголовок пачки: header=%d err=%v", header.ID, err)
	}
	readyBatches, err := transfer_postgres_repository.New(pool).ListReadyBatches(ctx, nil, 1000)
	if err != nil {
		t.Fatalf("повторная выборка готовых пачек: %v", err)
	}
	for _, batch := range readyBatches {
		if batch.ID == command.BatchID {
			t.Fatalf("переданная пачка повторно попала в очередь: %d", batch.ID)
		}
	}
	initial := readInitializationTargetSnapshot(t, ctx, pool, transferID)
	if initial.groupsCount != 2 || initial.groupsReady != 2 || initial.itemsCount != 2 || initial.itemsReady != 2 {
		t.Fatalf("цели не готовы после Initialize: %+v", initial)
	}

	for attempt := 0; attempt < 2; attempt++ {
		// Новый процессор и репозиторий моделируют перезапуск до первой успешной подготовки.
		repository := transfer_postgres_repository.New(pool)
		preparer := &temporarilyUnavailablePreparer{}
		processor := transfer_preparation.NewProcessor(
			preparer,
			repository,
			repository,
			uow,
		)
		if err := processor.ProcessPending(ctx); err == nil {
			t.Fatal("ожидалась временная ошибка каталога")
		}
		if calls := preparer.calls.Load(); calls != 2 {
			t.Fatalf("после перезапуска должны выбираться обе группы: вызовов %d", calls)
		}
		current := readInitializationTargetSnapshot(t, ctx, pool, transferID)
		if current != initial {
			t.Fatalf("повторный опрос изменил цели: до=%+v после=%+v", initial, current)
		}
	}
}

func readInitializationTargetSnapshot(
	t *testing.T,
	ctx context.Context,
	pool core_postgres_pool.Pool,
	transferID int64,
) initializationTargetSnapshot {
	t.Helper()
	var snapshot initializationTargetSnapshot
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*),
			COUNT(*) FILTER (WHERE preparation_status = 'running' AND revision = 0 AND started_at IS NOT NULL),
			COALESCE(jsonb_agg(jsonb_build_object(
				'id', id, 'revision', revision, 'status', preparation_status,
				'started_at', started_at, 'updated_at', updated_at
			) ORDER BY id), '[]'::jsonb)::text
		FROM wb.transfer_group_targets WHERE transfer_id = $1;
	`, transferID).Scan(&snapshot.groupsCount, &snapshot.groupsReady, &snapshot.groupsJSON); err != nil {
		t.Fatalf("чтение групп после инициализации: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*),
			COUNT(*) FILTER (WHERE state = 'running' AND revision = 0 AND started_at IS NOT NULL),
			COALESCE(jsonb_agg(jsonb_build_object(
				'id', id, 'revision', revision, 'state', state,
				'started_at', started_at, 'updated_at', updated_at
			) ORDER BY id), '[]'::jsonb)::text
		FROM wb.transfer_item_targets WHERE transfer_id = $1;
	`, transferID).Scan(&snapshot.itemsCount, &snapshot.itemsReady, &snapshot.itemsJSON); err != nil {
		t.Fatalf("чтение карточек после инициализации: %v", err)
	}
	return snapshot
}
