package transfer_postgres_repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardprepare_model "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
	transfer_postgres_repository "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type integrationPool struct {
	*pgxpool.Pool
}

func preparationSnapshot(t *testing.T, observedAt time.Time, free, paid int64) []byte {
	t.Helper()
	payload, err := json.Marshal(cardprepare_model.CatalogSnapshot{
		ObservedAt: observedAt,
		Limits: contentapi.CardsLimits{
			FreeLimits: free,
			PaidLimits: paid,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func (pool *integrationPool) OperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 10*time.Second)
}

func getTestPool(t *testing.T) (core_postgres_pool.Pool, core_postgres_transaction.UnitOfWork) {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN не задан")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pgxPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("создать тестовый пул PostgreSQL: %v", err)
	}
	if err := pgxPool.Ping(ctx); err != nil {
		pgxPool.Close()
		t.Fatalf("подключиться к тестовому PostgreSQL: %v", err)
	}
	var table string
	if err := pgxPool.QueryRow(ctx, "SELECT coalesce(to_regclass('wb.transfer_group_targets')::text, '')").Scan(&table); err != nil || table == "" {
		pgxPool.Close()
		t.Fatalf("миграции transfer не применены: table=%q err=%v", table, err)
	}
	pool := &integrationPool{Pool: pgxPool}
	uow := core_postgres_transaction.New(pool, pgx.TxOptions{})
	return pool, uow
}

func setupTestTransferData(
	t *testing.T,
	ctx context.Context,
	pool core_postgres_pool.Pool,
) (transferID transfer_model.TransferID, groupTargetID1 int64, groupTargetID2 int64, targetID1 int64, targetID2 int64) {
	t.Helper()

	// Создаём готовую пачку карточек.
	var batchID int64
	sessionID := time.Now().UnixNano()
	err := pool.QueryRow(ctx, `
		INSERT INTO wb.card_batches (
			source_session_id, author_telegram_id,
			finalized_from_revision, finalize_idempotency_key,
			items_count, groups_count, sealed
		)
		VALUES (
			$1, $1,
			0, 'integration-test', 1, 1, TRUE
		)
		RETURNING id;
	`, sessionID).Scan(&batchID)
	if err != nil {
		t.Fatalf("создание тестовой пачки карточек: %v", err)
	}

	// 3. Создаем transfer в статусе initializing для соблюдения инвариантов триггеров
	var tid int64
	err = pool.QueryRow(ctx, `
		INSERT INTO wb.transfers (
			batch_id, phase, outcome, items_count, groups_count,
			targets_count, item_targets_count, group_targets_count
		)
		VALUES ($1, 'initializing', 'running', 1, 1, 2, 2, 2)
		RETURNING id;
	`, batchID).Scan(&tid)
	if err != nil {
		t.Fatalf("создание тестового переноса: %v", err)
	}
	transferID = transfer_model.TransferID(tid)

	// 4. Создаем 2 цели (target cabinets)
	clientGen := int64(1)

	sellerID1 := domain.SellerID("00000000-0000-0000-0000-000000000001")
	sellerID2 := domain.SellerID("00000000-0000-0000-0000-000000000002")

	err = pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_targets (
			transfer_id, position, seller_id, client_generation,
			credential_expires_at, binding_revision, capability_revision, content_read, content_write
		)
		VALUES ($1, 1, $2, $3, CURRENT_TIMESTAMP + interval '1 day', 1, 1, true, true)
		RETURNING id;
	`, transferID, sellerID1, clientGen).Scan(&targetID1)
	if err != nil {
		t.Fatalf("создание target 1: %v", err)
	}

	err = pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_targets (
			transfer_id, position, seller_id, client_generation,
			credential_expires_at, binding_revision, capability_revision, content_read, content_write
		)
		VALUES ($1, 2, $2, $3, CURRENT_TIMESTAMP + interval '1 day', 1, 1, true, true)
		RETURNING id;
	`, transferID, sellerID2, clientGen).Scan(&targetID2)
	if err != nil {
		t.Fatalf("создание target 2: %v", err)
	}

	// 5. Создаем transfer_groups
	var groupID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_groups (
			transfer_id, source_group_number, source_group_name
		)
		VALUES ($1, $2, 'Группа 1')
		RETURNING id;
	`, transferID, 1).Scan(&groupID)
	if err != nil {
		t.Fatalf("создание transfer group: %v", err)
	}

	// 6. Создаем transfer_items
	var itemID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_items (
			transfer_id, batch_item_id, position, source_group_id, vendor_code, payload
		)
		VALUES ($1, 1, 1, $2, 'ART-1', '{"title": "Card 1"}'::jsonb)
		RETURNING id;
	`, transferID, groupID).Scan(&itemID)
	if err != nil {
		t.Fatalf("создание transfer item: %v", err)
	}

	// 7. Создаем transfer_group_targets в статусе running
	err = pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_group_targets (
			transfer_id, source_group_id, target_id, preparation_status, started_at
		)
		VALUES ($1, $2, $3, 'running', CURRENT_TIMESTAMP)
		RETURNING id;
	`, transferID, groupID, targetID1).Scan(&groupTargetID1)
	if err != nil {
		t.Fatalf("создание group target 1: %v", err)
	}

	err = pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_group_targets (
			transfer_id, source_group_id, target_id, preparation_status, started_at
		)
		VALUES ($1, $2, $3, 'running', CURRENT_TIMESTAMP)
		RETURNING id;
	`, transferID, groupID, targetID2).Scan(&groupTargetID2)
	if err != nil {
		t.Fatalf("создание group target 2: %v", err)
	}

	// 8. Создаем transfer_item_targets в статусе running
	_, err = pool.Exec(ctx, `
		INSERT INTO wb.transfer_item_targets (
			transfer_id, source_group_id, transfer_item_id, group_target_id, state, started_at
		)
		VALUES
			($1, $2, $3, $4, 'running', CURRENT_TIMESTAMP),
			($1, $2, $3, $5, 'running', CURRENT_TIMESTAMP);
	`, transferID, groupID, itemID, groupTargetID1, groupTargetID2)
	if err != nil {
		t.Fatalf("создание item targets: %v", err)
	}
	// Переводим перенос в активную фазу.
	_, err = pool.Exec(ctx, `
		UPDATE wb.transfers
		SET phase = 'active', updated_at = CURRENT_TIMESTAMP
		WHERE id = $1;
	`, transferID)
	if err != nil {
		t.Fatalf("перевод переноса в active: %v", err)
	}

	return transferID, groupTargetID1, groupTargetID2, targetID1, targetID2
}

// TestPostgres_PreparationResult_DiagnosticsAndIdempotency проверяет:
// 1) Сохранение диагностики (started_at, finished_at, item_position, field).
// 2) Идемпотентность повторного применения одинакового результата (no-op nil).
// 3) Возврат ErrPreparationResultConflict при конфликтующем повторном вызове.
func TestPostgres_PreparationResult_DiagnosticsAndIdempotency(t *testing.T) {
	pool, uow := getTestPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	transferID, groupTarget1, groupTarget2, _, _ := setupTestTransferData(t, ctx, pool)

	repo := transfer_postgres_repository.New(pool)

	startedAt := time.Now().UTC().Add(-100 * time.Millisecond)
	finishedAt := time.Now().UTC()

	// 1. Применяем успешный результат подготовки к groupTarget1
	successCmd := transfer_preparation.ApplyPreparationResultCommand{
		TransferID:    transferID,
		GroupTargetID: groupTarget1,
		Status:        transfer_preparation.PreparationResultSucceeded,
		OutcomeCode:   "prepared",
		StartedAt:     startedAt,
		FinishedAt:    finishedAt,
		Artifact: &transfer_preparation.PreparationArtifact{
			SubjectID:        12345,
			RequestPayload:   []byte(`[{"subjectID":12345,"variants":[{"vendorCode":"ART-1"}]}]`),
			MetadataSnapshot: preparationSnapshot(t, startedAt, 100, 0),
			LimitsFree:       100,
			LimitsPaid:       0,
			LimitsObservedAt: startedAt,
		},
	}

	err := uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		return repo.ApplyPreparationResult(ctx, tx, successCmd)
	})
	if err != nil {
		t.Fatalf("ожидалось успешное сохранение результата подготовки: %v", err)
	}

	// Проверяем БД: статус, отсутствие item_position и field, наличие временных меток и артефакта
	var (
		status      string
		revision    int64
		code        *string
		itemPos     *int
		field       *string
		dbStartedAt *time.Time
		dbFinAt     *time.Time
	)
	err = pool.QueryRow(ctx, `
		SELECT preparation_status, revision, preparation_result_code,
		       preparation_item_position, preparation_field,
		       preparation_started_at, preparation_finished_at
		FROM wb.transfer_group_targets
		WHERE id = $1;
	`, groupTarget1).Scan(&status, &revision, &code, &itemPos, &field, &dbStartedAt, &dbFinAt)
	if err != nil {
		t.Fatalf("чтение сохранённой группы: %v", err)
	}

	if status != "succeeded" || revision != 1 || code == nil || *code != "prepared" {
		t.Errorf("неверный статус или код в БД: status=%s, rev=%d, code=%v", status, revision, code)
	}
	if itemPos != nil || field != nil {
		t.Errorf("у успешной группы item_position и field должны быть NULL, получено: pos=%v, field=%v", itemPos, field)
	}
	if dbStartedAt == nil || dbFinAt == nil {
		t.Errorf("временные метки подготовки должны быть установлены")
	}

	// Проверяем наличие артефакта
	var artGroupTargetID int64
	err = pool.QueryRow(ctx, `
		SELECT group_target_id FROM wb.card_preparation_artifacts
		WHERE transfer_id = $1 AND group_target_id = $2;
	`, transferID, groupTarget1).Scan(&artGroupTargetID)
	if err != nil {
		t.Fatalf("артефакт подготовки не найден в таблице: %v", err)
	}

	// 2. Идемпотентность: повторный вызов с теми же параметрами ДОЛЖЕН вернуть nil (no-op)
	err = uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		return repo.ApplyPreparationResult(ctx, tx, successCmd)
	})
	if err != nil {
		t.Errorf("повторный идемпотентный вызов с теми же данными должен возвращать nil, получено: %v", err)
	}

	// 3. Конфликт: повторный вызов с другим результатом должен вернуть ErrPreparationResultConflict.
	conflictCmd := successCmd
	conflictCmd.Status = transfer_preparation.PreparationResultRejected
	conflictCmd.OutcomeCode = "different_result"
	conflictCmd.Artifact = nil

	err = uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		return repo.ApplyPreparationResult(ctx, tx, conflictCmd)
	})
	if !errors.Is(err, transfer_preparation.ErrPreparationResultConflict) {
		t.Errorf("ожидалась ошибка ErrPreparationResultConflict при конфликте, получено: %v", err)
	}

	// 4. Применяем отклонённый результат с детальной диагностикой к groupTarget2
	rejectCmd := transfer_preparation.ApplyPreparationResultCommand{
		TransferID:    transferID,
		GroupTargetID: groupTarget2,
		Status:        transfer_preparation.PreparationResultRejected,
		OutcomeCode:   "characteristic_missing",
		ItemPosition:  1,
		Field:         "Цвет",
		StartedAt:     startedAt,
		FinishedAt:    finishedAt,
	}

	err = uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		return repo.ApplyPreparationResult(ctx, tx, rejectCmd)
	})
	if err != nil {
		t.Fatalf("сохранение отклонённого результата: %v", err)
	}

	// Проверяем сохранение диагностики ошибки в БД
	err = pool.QueryRow(ctx, `
		SELECT preparation_status, revision, preparation_result_code,
		       preparation_item_position, preparation_field
		FROM wb.transfer_group_targets
		WHERE id = $1;
	`, groupTarget2).Scan(&status, &revision, &code, &itemPos, &field)
	if err != nil {
		t.Fatalf("чтение groupTarget2: %v", err)
	}

	if status != "rejected" || revision != 1 || code == nil || *code != "characteristic_missing" {
		t.Errorf("неверный статус/код для reject: %s, %v", status, code)
	}
	if itemPos == nil || *itemPos != 1 {
		t.Errorf("ожидался item_position=1, получено: %v", itemPos)
	}
	if field == nil || *field != "Цвет" {
		t.Errorf("ожидался field='Цвет', получено: %v", field)
	}

	// Идемпотентность для отклонённого результата
	err = uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		return repo.ApplyPreparationResult(ctx, tx, rejectCmd)
	})
	if err != nil {
		t.Errorf("повторный отклонённый результат должен быть идемпотентным, получено: %v", err)
	}
}

func TestPostgres_PreparationArtifactAndProjectionRollbackTogether(t *testing.T) {
	pool, uow := getTestPool(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	transferID, groupTargetID, _, _, _ := setupTestTransferData(t, ctx, pool)
	repository := transfer_postgres_repository.New(pool)

	startedAt := time.Now().UTC()
	metadata := preparationSnapshot(t, startedAt, 1, 0)
	payload := []byte(`[{"subjectID":12345,"variants":[{"vendorCode":"ART-1"}]}]`)
	command := transfer_preparation.ApplyPreparationResultCommand{
		TransferID:    transferID,
		GroupTargetID: groupTargetID,
		Status:        transfer_preparation.PreparationResultSucceeded,
		OutcomeCode:   "prepared",
		StartedAt:     startedAt,
		FinishedAt:    startedAt.Add(time.Millisecond),
		Artifact: &transfer_preparation.PreparationArtifact{
			SubjectID:        12345,
			RequestPayload:   payload,
			MetadataSnapshot: metadata,
			LimitsFree:       1,
			LimitsObservedAt: startedAt,
		},
	}
	if err := command.Validate(); err != nil {
		t.Fatalf("тестовая команда подготовки некорректна: %v", err)
	}
	rollbackReason := errors.New("rollback after preparation result")
	err := uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		if err := repository.ApplyPreparationResult(ctx, tx, command); err != nil {
			return err
		}
		return rollbackReason
	})
	if !errors.Is(err, rollbackReason) {
		t.Fatalf("ожидался откат после записи результата: %v", err)
	}

	var status string
	var revision int64
	if err := pool.QueryRow(ctx, `
		SELECT preparation_status, revision
		FROM wb.transfer_group_targets WHERE id = $1
	`, groupTargetID).Scan(&status, &revision); err != nil {
		t.Fatalf("прочитать проекцию после отката: %v", err)
	}
	var artifactCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM wb.card_preparation_artifacts
		WHERE transfer_id = $1 AND group_target_id = $2
	`, transferID, groupTargetID).Scan(&artifactCount); err != nil {
		t.Fatalf("прочитать артефакт после отката: %v", err)
	}
	if status != "running" || revision != 0 || artifactCount != 0 {
		t.Fatalf("результат пережил откат: status=%q revision=%d artifacts=%d", status, revision, artifactCount)
	}
}

// TestPostgres_ConcurrentWorkersOnSameBatch проверяет параллельную работу нескольких воркеров на одной пачке.
func TestPostgres_ConcurrentWorkersOnSameBatch(t *testing.T) {
	pool, uow := getTestPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	transferID, groupTarget1, _, _, _ := setupTestTransferData(t, ctx, pool)

	repo := transfer_postgres_repository.New(pool)

	startedAt := time.Now().UTC().Add(-50 * time.Millisecond)
	finishedAt := time.Now().UTC()

	cmd := transfer_preparation.ApplyPreparationResultCommand{
		TransferID:    transferID,
		GroupTargetID: groupTarget1,
		Status:        transfer_preparation.PreparationResultSucceeded,
		OutcomeCode:   "prepared",
		StartedAt:     startedAt,
		FinishedAt:    finishedAt,
		Artifact: &transfer_preparation.PreparationArtifact{
			SubjectID:        12345,
			RequestPayload:   []byte(`[{"subjectID":12345,"variants":[{"vendorCode":"ART-1"}]}]`),
			MetadataSnapshot: preparationSnapshot(t, startedAt, 10, 0),
			LimitsFree:       10,
			LimitsPaid:       0,
			LimitsObservedAt: startedAt,
		},
	}

	const workerCount = 10
	var wg sync.WaitGroup
	errs := make([]error, workerCount)

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
				return repo.ApplyPreparationResult(ctx, tx, cmd)
			})
		}(i)
	}

	wg.Wait()

	// Все параллельные вызовы должны завершиться успешно (один выполняет переход, остальные — идемпотентный no-op)
	for idx, err := range errs {
		if err != nil {
			t.Errorf("воркер %d получил ошибку: %v", idx, err)
		}
	}
}

// TestPostgres_FairWindowPartitioning проверяет чередование целей одной исходной группы.
func TestPostgres_FairWindowPartitioning(t *testing.T) {
	pool, _ := getTestPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	transferID, _, _, _, _ := setupTestTransferData(t, ctx, pool)

	repo := transfer_postgres_repository.New(pool)

	// Запрашиваем порцию групп
	groups, err := repo.ListPendingPreparationGroups(ctx, transferID, 10, transfer_preparation.PreparationPendingGroup{})
	if err != nil {
		t.Fatalf("ListPendingPreparationGroups вернул ошибку: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("ожидалось 2 группы, получено %d", len(groups))
	}

	// Проверяем, что группы принадлежат разным целевым кабинетам.
	if groups[0].TargetID == groups[1].TargetID {
		t.Errorf("группы должны быть от разных кабинетов, получен target_id=%d",
			groups[0].TargetID)
	}
}

func TestPostgres_PreparationRejectsMismatchedRequestItems(t *testing.T) {
	pool, uow := getTestPool(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	transferID, groupTargetID, _, _, _ := setupTestTransferData(t, ctx, pool)
	repository := transfer_postgres_repository.New(pool)
	now := time.Now().UTC()
	command := transfer_preparation.ApplyPreparationResultCommand{
		TransferID:    transferID,
		GroupTargetID: groupTargetID,
		Status:        transfer_preparation.PreparationResultSucceeded,
		OutcomeCode:   "prepared",
		StartedAt:     now,
		FinishedAt:    now,
		Artifact: &transfer_preparation.PreparationArtifact{
			SubjectID:        12345,
			RequestPayload:   []byte(`[{"subjectID":12345,"variants":[{"vendorCode":"OTHER"}]}]`),
			MetadataSnapshot: preparationSnapshot(t, now, 0, 0),
			LimitsObservedAt: now,
		},
	}
	if err := command.Validate(); err != nil {
		t.Fatalf("тестовая команда должна проходить структурную проверку: %v", err)
	}
	err := uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		return repository.ApplyPreparationResult(ctx, tx, command)
	})
	if !errors.Is(err, transfer_preparation.ErrPreparationResultConflict) {
		t.Fatalf("несовпадение карточек должно вызвать конфликт: %v", err)
	}
	var status string
	var artifactCount int
	if err := pool.QueryRow(ctx, `SELECT preparation_status FROM wb.transfer_group_targets WHERE id = $1`, groupTargetID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM wb.card_preparation_artifacts WHERE group_target_id = $1`, groupTargetID).Scan(&artifactCount); err != nil {
		t.Fatal(err)
	}
	if status != "running" || artifactCount != 0 {
		t.Fatalf("несогласованный результат сохранился: status=%s artifacts=%d", status, artifactCount)
	}
}
