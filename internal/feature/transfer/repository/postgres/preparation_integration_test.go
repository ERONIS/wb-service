package transfer_postgres_repository_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_postgres_repository "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	"github.com/jackc/pgx/v5"
)

func getTestPool(t *testing.T) (core_postgres_pool.Pool, core_postgres_transaction.UnitOfWork) {
	t.Helper()
	host := os.Getenv("POSTGRES_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("POSTGRES_PORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("POSTGRES_USER")
	if user == "" {
		user = "Eronis"
	}
	pass := os.Getenv("POSTGRES_PASSWORD")
	if pass == "" {
		pass = "1234"
	}
	db := os.Getenv("POSTGRES_DB")
	if db == "" {
		db = "test-db"
	}

	cfg := core_postgres_pool.Config{
		Host:     host,
		Port:     port,
		User:     user,
		Password: pass,
		Database: db,
		Timeout:  10 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := core_postgres_pool.NewConnectionPool(cfg, ctx)
	if err != nil {
		t.Skipf("Пропуск интеграционного теста: PostgreSQL недоступен (%v)", err)
		return nil, nil
	}
	uow := core_postgres_transaction.New(pool, pgx.TxOptions{})
	return pool, uow
}

func setupTestTransferData(
	t *testing.T,
	ctx context.Context,
	pool core_postgres_pool.Pool,
	prefix string,
) (transferID transfer_service.TransferID, groupTargetID1 int64, groupTargetID2 int64, targetID1 int64, targetID2 int64) {
	t.Helper()

	// 1. Создаем пользователя, если нет
	_, _ = pool.Exec(ctx, `
		INSERT INTO wb.users (id, username, full_name, role, status)
		VALUES (999999, 'test_user', 'Тестовый пользователь', 'admin', 'active')
		ON CONFLICT (id) DO NOTHING;
	`)

	// 2. Создаем batch
	var batchID int64
	checksum := make([]byte, 32)
	copy(checksum, []byte("test_checksum_"+prefix))
	refID := time.Now().UnixNano()
	err := pool.QueryRow(ctx, `
		INSERT INTO wb.card_batches (
			source_kind, source_reference_id, purpose, schema_version,
			normalization_version, items_count, groups_count, checksum, author_snapshot
		)
		VALUES ('wb_cabinet', $1, 'transfer', 1, 1, 1, 1, $2, '{}'::jsonb)
		RETURNING id;
	`, refID, checksum).Scan(&batchID)
	if err != nil {
		t.Fatalf("создание тестовой пачки карточек: %v", err)
	}

	// 3. Создаем transfer в статусе initializing для соблюдения инвариантов триггеров
	targetSetRoot := make([]byte, 32)
	copy(targetSetRoot, []byte("test_root_"+prefix))
	targetSnapshotRev := make([]byte, 32)
	copy(targetSnapshotRev, []byte("test_rev_"+prefix))
	var tid int64
	err = pool.QueryRow(ctx, `
		INSERT INTO wb.transfers (
			batch_id, phase, outcome, batch_schema_version, batch_normalization_version,
			batch_checksum, items_count, groups_count, cohort_name, target_snapshot_revision,
			target_set_root, targets_count, item_targets_count, group_targets_count
		)
		VALUES ($1, 'initializing', 'running', 1, 1, $2, 1, 1, 'default', $3, $4, 2, 2, 2)
		RETURNING id;
	`, batchID, checksum, targetSnapshotRev, targetSetRoot).Scan(&tid)
	if err != nil {
		t.Fatalf("создание тестового переноса: %v", err)
	}
	transferID = transfer_service.TransferID(tid)

	// 4. Создаем 2 цели (target cabinets)
	sellerKey1 := make([]byte, 32)
	copy(sellerKey1, []byte("seller_key_1_"+prefix))
	sellerKey2 := make([]byte, 32)
	copy(sellerKey2, []byte("seller_key_2_"+prefix))
	clientGen := make([]byte, 32)
	copy(clientGen, []byte("client_gen_"+prefix))

	cabID1 := fmt.Sprintf("cab_1_%s", prefix)
	cabID2 := fmt.Sprintf("cab_2_%s", prefix)

	err = pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_targets (
			transfer_id, position, cabinet_id, seller_key, client_generation,
			credential_expires_at, binding_revision, capability_revision, content_read, content_write
		)
		VALUES ($1, 1, $2, $3, $4, CURRENT_TIMESTAMP + interval '1 day', 1, 1, true, true)
		RETURNING id;
	`, transferID, cabID1, sellerKey1, clientGen).Scan(&targetID1)
	if err != nil {
		t.Fatalf("создание target 1: %v", err)
	}

	err = pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_targets (
			transfer_id, position, cabinet_id, seller_key, client_generation,
			credential_expires_at, binding_revision, capability_revision, content_read, content_write
		)
		VALUES ($1, 2, $2, $3, $4, CURRENT_TIMESTAMP + interval '1 day', 1, 1, true, true)
		RETURNING id;
	`, transferID, cabID2, sellerKey2, clientGen).Scan(&targetID2)
	if err != nil {
		t.Fatalf("создание target 2: %v", err)
	}

	// 5. Создаем transfer_groups
	groupKey := make([]byte, 32)
	copy(groupKey, []byte("group_key_"+prefix))
	var groupID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_groups (
			transfer_id, source_group_key, source_file_id, source_group_name
		)
		VALUES ($1, $2, 1, 'Группа 1')
		RETURNING id;
	`, transferID, groupKey).Scan(&groupID)
	if err != nil {
		t.Fatalf("создание transfer group: %v", err)
	}

	// 6. Создаем transfer_items
	var itemID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_items (
			transfer_id, batch_item_id, position, source_file_id, source_rows,
			source_group_id, vendor_code, payload
		)
		VALUES ($1, 1, 1, 1, '{1}', $2, 'ART-1', '{"title": "Card 1"}'::jsonb)
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
	// 9. Переводим transfer в фазу preparing
	_, err = pool.Exec(ctx, `
		UPDATE wb.transfers
		SET phase = 'preparing', updated_at = CURRENT_TIMESTAMP
		WHERE id = $1;
	`, transferID)
	if err != nil {
		t.Fatalf("перевод переноса в preparing: %v", err)
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
	prefix := fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
	transferID, groupTarget1, groupTarget2, _, _ := setupTestTransferData(t, ctx, pool, prefix)

	repo := transfer_postgres_repository.New(pool)

	startedAt := time.Now().UTC().Add(-100 * time.Millisecond)
	finishedAt := time.Now().UTC()

	var proposalRoot transfer_service.Digest
	copy(proposalRoot[:], []byte("proposal_root_success_00000001"))
	var semDigest, metaDigest transfer_service.Digest
	copy(semDigest[:], []byte("sem_digest_00000000000000000001"))
	metaDigest, _ = cardpipeline.DigestJSON("cardprepare-metadata:v1", json.RawMessage(`{"version": 1}`))
	proposalRoot = cardpipeline.DigestProposal(semDigest, metaDigest, []byte(`{"cards": [{"vendorCode": "ART-1"}]}`))

	// 1. Применяем успешный результат подготовки к groupTarget1
	successCmd := transfer_service.ApplyPreparationResultCommand{
		TransferID:    transferID,
		GroupTargetID: groupTarget1,
		Status:        transfer_service.PreparationResultSucceeded,
		OutcomeCode:   "prepared",
		StartedAt:     startedAt,
		FinishedAt:    finishedAt,
		ProposalRoot:  proposalRoot,
		Artifact: &transfer_service.PreparationArtifact{
			SubjectID:        12345,
			SemanticDigest:   semDigest,
			MetadataDigest:   metaDigest,
			ProposalRoot:     proposalRoot,
			RequestPayload:   []byte(`{"cards": [{"vendorCode": "ART-1"}]}`),
			MetadataSnapshot: []byte(`{"version": 1}`),
			LimitsFree:       100,
			LimitsPaid:       0,
			LimitsObservedAt: startedAt,
		},
		Revision: 1,
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
		dbPropRoot  []byte
		itemPos     *int
		field       *string
		dbStartedAt *time.Time
		dbFinAt     *time.Time
	)
	err = pool.QueryRow(ctx, `
		SELECT preparation_status, preparation_result_revision, preparation_result_code,
		       preparation_proposal_root, preparation_item_position, preparation_field,
		       preparation_started_at, preparation_finished_at
		FROM wb.transfer_group_targets
		WHERE id = $1;
	`, groupTarget1).Scan(&status, &revision, &code, &dbPropRoot, &itemPos, &field, &dbStartedAt, &dbFinAt)
	if err != nil {
		t.Fatalf("чтение сохранённой группы: %v", err)
	}

	if status != "succeeded" || revision != 1 || code == nil || *code != "prepared" {
		t.Errorf("неверный статус или код в БД: status=%s, rev=%d, code=%v", status, revision, code)
	}
	if !bytes.Equal(dbPropRoot, proposalRoot[:]) {
		t.Errorf("не совпадает proposal_root")
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

	// 3. Конфликт: повторный вызов с конфликтующим proposal_root ДОЛЖЕН вернуть ErrPreparationResultConflict
	conflictCmd := successCmd
	changedArtifact := *successCmd.Artifact
	conflictCmd.Artifact = &changedArtifact
	changedArtifact.RequestPayload = []byte(`{"cards": []}`)
	changedArtifact.ProposalRoot = cardpipeline.DigestProposal(changedArtifact.SemanticDigest, changedArtifact.MetadataDigest, changedArtifact.RequestPayload)
	conflictCmd.ProposalRoot = changedArtifact.ProposalRoot

	err = uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		return repo.ApplyPreparationResult(ctx, tx, conflictCmd)
	})
	if !errors.Is(err, transfer_service.ErrPreparationResultConflict) {
		t.Errorf("ожидалась ошибка ErrPreparationResultConflict при конфликте, получено: %v", err)
	}

	// 4. Применяем отклонённый результат с детальной диагностикой к groupTarget2
	rejectCmd := transfer_service.ApplyPreparationResultCommand{
		TransferID:    transferID,
		GroupTargetID: groupTarget2,
		Status:        transfer_service.PreparationResultRejected,
		OutcomeCode:   "characteristic_missing",
		ItemPosition:  1,
		Field:         "Цвет",
		StartedAt:     startedAt,
		FinishedAt:    finishedAt,
		Revision:      1,
	}

	err = uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		return repo.ApplyPreparationResult(ctx, tx, rejectCmd)
	})
	if err != nil {
		t.Fatalf("сохранение отклонённого результата: %v", err)
	}

	// Проверяем сохранение диагностики ошибки в БД
	err = pool.QueryRow(ctx, `
		SELECT preparation_status, preparation_result_revision, preparation_result_code,
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

// TestPostgres_ConcurrentWorkersOnSameBatch проверяет параллельную работу нескольких воркеров на одной пачке.
func TestPostgres_ConcurrentWorkersOnSameBatch(t *testing.T) {
	pool, uow := getTestPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	prefix := fmt.Sprintf("conc_%d", time.Now().UnixNano()%1000000)
	transferID, groupTarget1, _, _, _ := setupTestTransferData(t, ctx, pool, prefix)

	repo := transfer_postgres_repository.New(pool)

	startedAt := time.Now().UTC().Add(-50 * time.Millisecond)
	finishedAt := time.Now().UTC()

	var proposalRoot transfer_service.Digest
	copy(proposalRoot[:], []byte("proposal_root_concurrent_0000001"))
	var semDigest, metaDigest transfer_service.Digest
	copy(semDigest[:], []byte("sem_digest_concurrent_0000000001"))
	metaDigest, _ = cardpipeline.DigestJSON("cardprepare-metadata:v1", json.RawMessage(`{}`))
	proposalRoot = cardpipeline.DigestProposal(semDigest, metaDigest, []byte(`{"cards": []}`))

	cmd := transfer_service.ApplyPreparationResultCommand{
		TransferID:    transferID,
		GroupTargetID: groupTarget1,
		Status:        transfer_service.PreparationResultSucceeded,
		OutcomeCode:   "prepared",
		StartedAt:     startedAt,
		FinishedAt:    finishedAt,
		ProposalRoot:  proposalRoot,
		Artifact: &transfer_service.PreparationArtifact{
			SubjectID:        12345,
			SemanticDigest:   semDigest,
			MetadataDigest:   metaDigest,
			ProposalRoot:     proposalRoot,
			RequestPayload:   []byte(`{"cards": []}`),
			MetadataSnapshot: []byte(`{}`),
			LimitsFree:       10,
			LimitsPaid:       0,
			LimitsObservedAt: startedAt,
		},
		Revision: 1,
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
	prefix := fmt.Sprintf("win_%d", time.Now().UnixNano()%1000000)
	transferID, _, _, _, _ := setupTestTransferData(t, ctx, pool, prefix)

	repo := transfer_postgres_repository.New(pool)

	// Запрашиваем порцию групп
	groups, err := repo.ListPendingPreparationGroups(ctx, transferID, 10, transfer_service.PreparationPendingGroup{})
	if err != nil {
		t.Fatalf("ListPendingPreparationGroups вернул ошибку: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("ожидалось 2 группы, получено %d", len(groups))
	}

	// Проверяем, что группы принадлежат разным кабинетам (справедливое чередование)
	if groups[0].CabinetID == groups[1].CabinetID {
		t.Errorf("группы должны быть от разных кабинетов, получено: %s и %s",
			groups[0].CabinetID, groups[1].CabinetID)
	}
}
