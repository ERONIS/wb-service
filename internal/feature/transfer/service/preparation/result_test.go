package preparation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// mockDBTX реализует интерфейс DBTX для тестирования.
type mockDBTX struct{}

func (m *mockDBTX) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (m *mockDBTX) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}

func (m *mockDBTX) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return nil
}

func (m *mockDBTX) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

// mockUOW реализует UnitOfWork для тестирования.
type mockUOW struct {
	withinTxCalls int
	err           error
}

func (m *mockUOW) WithinTransaction(ctx context.Context, fn func(context.Context, core_postgres_transaction.DBTX) error) error {
	m.withinTxCalls++
	if m.err != nil {
		return m.err
	}
	return fn(ctx, &mockDBTX{})
}

// mockRepo реализует PreparationResultRepository для проверки вызовов.
type mockRepo struct {
	appliedCommand preparation.ApplyPreparationResultCommand
	applyCalls     int
	err            error
}

func (m *mockRepo) ApplyPreparationResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	cmd preparation.ApplyPreparationResultCommand,
) error {
	m.applyCalls++
	m.appliedCommand = cmd
	return m.err
}

func validArtifact() *preparation.PreparationArtifact {
	snapshot := []byte(`{"version":1}`)
	a := &preparation.PreparationArtifact{SubjectID: 1,
		RequestPayload: []byte(`[{"subjectID":1}]`), MetadataSnapshot: snapshot, LimitsObservedAt: time.Unix(1, 0)}
	return a
}

// TestPreparationResultApplier_Apply проверяет вызов Apply с открытием новой транзакции.
func TestPreparationResultApplier_Apply(t *testing.T) {
	uow := &mockUOW{}
	repo := &mockRepo{}
	applier := preparation.NewPreparationResultApplier(repo, uow)

	now := time.Now().UTC()
	cmd := preparation.ApplyPreparationResultCommand{
		TransferID:    1,
		GroupTargetID: 10,
		Status:        preparation.PreparationResultSucceeded,
		OutcomeCode:   "prepared",
		StartedAt:     now,
		FinishedAt:    now.Add(10 * time.Millisecond),
		Artifact:      validArtifact(),
		Revision:      1,
	}

	ctx := context.Background()
	if err := applier.Apply(ctx, cmd); err != nil {
		t.Fatalf("ожидался успешный вызов Apply, получена ошибка: %v", err)
	}

	if uow.withinTxCalls != 1 {
		t.Errorf("ожидался 1 вызов WithinTransaction, получено: %d", uow.withinTxCalls)
	}
	if repo.applyCalls != 1 {
		t.Errorf("ожидался 1 вызов ApplyPreparationResult, получено: %d", repo.applyCalls)
	}
	if repo.appliedCommand.GroupTargetID != 10 {
		t.Errorf("ожидался GroupTargetID=10, получено: %d", repo.appliedCommand.GroupTargetID)
	}
}

// TestPreparationResultApplier_ApplyTx проверяет выполнение ApplyTx во внешней транзакции.
func TestPreparationResultApplier_ApplyTx(t *testing.T) {
	uow := &mockUOW{}
	repo := &mockRepo{}
	applier := preparation.NewPreparationResultApplier(repo, uow)

	now := time.Now().UTC()
	cmd := preparation.ApplyPreparationResultCommand{
		TransferID:    1,
		GroupTargetID: 20,
		Status:        preparation.PreparationResultSucceeded,
		OutcomeCode:   "prepared",
		StartedAt:     now,
		FinishedAt:    now.Add(10 * time.Millisecond),
		Artifact:      validArtifact(),
		Revision:      1,
	}

	tx := &mockDBTX{}
	ctx := context.Background()

	// Проверка передачи nil tx
	if err := applier.ApplyTx(ctx, nil, cmd); err == nil {
		t.Error("ожидалась ошибка при nil tx в ApplyTx")
	}

	// Успешный вызов во внешней транзакции
	if err := applier.ApplyTx(ctx, tx, cmd); err != nil {
		t.Fatalf("ожидался успешный вызов ApplyTx, получена ошибка: %v", err)
	}

	// WithinTransaction НЕ должен вызываться
	if uow.withinTxCalls != 0 {
		t.Errorf("WithinTransaction не должен вызываться в ApplyTx, получено вызовов: %d", uow.withinTxCalls)
	}
	if repo.applyCalls != 1 {
		t.Errorf("ожидался 1 вызов ApplyPreparationResult, получено: %d", repo.applyCalls)
	}
	if repo.appliedCommand.GroupTargetID != 20 {
		t.Errorf("ожидался GroupTargetID=20, получено: %d", repo.appliedCommand.GroupTargetID)
	}
}

// TestPreparationResultApplier_ValidationErrors проверяет отклонение невалидных команд.
func TestPreparationResultApplier_ValidationErrors(t *testing.T) {
	uow := &mockUOW{}
	repo := &mockRepo{}
	applier := preparation.NewPreparationResultApplier(repo, uow)
	tx := &mockDBTX{}
	ctx := context.Background()

	// Невалидная команда (TransferID = 0)
	invalidCmd := preparation.ApplyPreparationResultCommand{}
	if err := applier.Apply(ctx, invalidCmd); err == nil {
		t.Error("Apply должен возвращать ошибку на невалидную команду")
	}
	if err := applier.ApplyTx(ctx, tx, invalidCmd); err == nil {
		t.Error("ApplyTx должен возвращать ошибку на невалидную команду")
	}

	now := time.Now().UTC()
	// Ошибка контекста
	validCmd := preparation.ApplyPreparationResultCommand{
		TransferID:    1,
		GroupTargetID: 1,
		Status:        preparation.PreparationResultRejected,
		OutcomeCode:   "rejected_code",
		StartedAt:     now,
		FinishedAt:    now.Add(10 * time.Millisecond),
		Revision:      1,
	}
	var nilCtx context.Context
	if err := applier.Apply(nilCtx, validCmd); err == nil {
		t.Error("Apply должен возвращать ошибку на nil context")
	}
	if err := applier.ApplyTx(nilCtx, tx, validCmd); err == nil {
		t.Error("ApplyTx должен возвращать ошибку на nil context")
	}

	// Проверка невалидных временных меток (FinishedAt раньше StartedAt)
	invalidTimingCmd := validCmd
	invalidTimingCmd.FinishedAt = now.Add(-10 * time.Millisecond)
	if err := applier.Apply(ctx, invalidTimingCmd); err == nil {
		t.Error("Apply должен отклонять команду, где FinishedAt раньше StartedAt")
	}

	// Проверка недопустимых полей для успеха (ItemPosition > 0 при succeeded)
	invalidSuccessCmd := preparation.ApplyPreparationResultCommand{
		TransferID:    1,
		GroupTargetID: 1,
		Status:        preparation.PreparationResultSucceeded,
		OutcomeCode:   "prepared",
		ItemPosition:  2,
		StartedAt:     now,
		FinishedAt:    now.Add(10 * time.Millisecond),
		Artifact:      validArtifact(),
		Revision:      1,
	}
	if err := applier.Apply(ctx, invalidSuccessCmd); err == nil {
		t.Error("Apply должен отклонять успешную подготовку с ненулевым ItemPosition")
	}
}

// TestPreparationResultApplier_ErrorPropagation проверяет проброс ошибки репозитория.
func TestPreparationResultApplier_ErrorPropagation(t *testing.T) {
	expectedErr := errors.New("ошибка базы данных")
	uow := &mockUOW{}
	repo := &mockRepo{err: expectedErr}
	applier := preparation.NewPreparationResultApplier(repo, uow)
	tx := &mockDBTX{}
	ctx := context.Background()

	now := time.Now().UTC()
	validCmd := preparation.ApplyPreparationResultCommand{
		TransferID:    1,
		GroupTargetID: 1,
		Status:        preparation.PreparationResultRejected,
		OutcomeCode:   "rejected_code",
		StartedAt:     now,
		FinishedAt:    now.Add(10 * time.Millisecond),
		Revision:      1,
	}

	if err := applier.ApplyTx(ctx, tx, validCmd); !errors.Is(err, expectedErr) {
		t.Errorf("ожидалась ошибка %v, получена: %v", expectedErr, err)
	}
}

func TestPreparationResultArtifactShape(t *testing.T) {
	cases := map[string]func(*preparation.PreparationArtifact){
		"payload":  func(a *preparation.PreparationArtifact) { a.RequestPayload = []byte(`{`) },
		"snapshot": func(a *preparation.PreparationArtifact) { a.MetadataSnapshot = []byte(`{`) },
		"subject":  func(a *preparation.PreparationArtifact) { a.SubjectID = 0 },
		"limits":   func(a *preparation.PreparationArtifact) { a.LimitsFree = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a := validArtifact()
			now := time.Now()
			cmd := preparation.ApplyPreparationResultCommand{TransferID: 1, GroupTargetID: 1,
				Status: preparation.PreparationResultSucceeded, OutcomeCode: "prepared", Revision: 1,
				StartedAt: now, FinishedAt: now, Artifact: a}
			if err := cmd.Validate(); err != nil {
				t.Fatal(err)
			}
			mutate(a)
			if err := cmd.Validate(); err == nil {
				t.Fatal("некорректный артефакт принят")
			}
		})
	}
}
