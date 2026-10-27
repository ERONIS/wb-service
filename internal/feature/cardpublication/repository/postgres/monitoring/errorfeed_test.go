package monitoring

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_monitoring "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/monitoring"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type errorBatchInsertCall struct {
	query     string
	arguments []any
}

type errorBatchInsertTx struct {
	pgx.Tx
	calls     []errorBatchInsertCall
	affected  []int64
	failureAt int
	failure   error
}

var _ core_postgres_transaction.DBTX = (*errorBatchInsertTx)(nil)

func (tx *errorBatchInsertTx) Exec(_ context.Context, query string, arguments ...any) (pgconn.CommandTag, error) {
	index := len(tx.calls)
	tx.calls = append(tx.calls, errorBatchInsertCall{query: query, arguments: append([]any(nil), arguments...)})
	if index == tx.failureAt && tx.failure != nil {
		return pgconn.CommandTag{}, tx.failure
	}
	return pgconn.NewCommandTag(fmt.Sprintf("INSERT 0 %d", tx.affected[index])), nil
}

func TestInsertErrorBatchesUsesBoundedStatementsAndAffectedRows(t *testing.T) {
	const sellerID = core_domain.SellerID("00000000-0000-0000-0000-000000000001")
	observedAt := time.Now().UTC()
	batches := make([]cardpublication_monitoring.ErrorBatchEvidence, errorFeedInsertBatchSize*2+1)
	for index := range batches {
		batches[index] = cardpublication_monitoring.ErrorBatchEvidence{
			SellerID: sellerID, BatchUUID: fmt.Sprintf("batch-%d", index), UpdatedAt: observedAt,
			VendorCodes: []string{"item"}, RejectedVendorCodes: []string{"item"}, ErrorCodes: []string{"wb_error_1"},
		}
	}
	tx := &errorBatchInsertTx{affected: []int64{255, 0, 1}, failureAt: -1}
	inserted, err := insertErrorBatches(context.Background(), tx, batches, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 256 || len(tx.calls) != 3 {
		t.Fatalf("вставлено %d строк за %d запросов, ожидалось 256 строк и 3 запроса", inserted, len(tx.calls))
	}
	for index, call := range tx.calls {
		wantArguments := errorFeedInsertBatchSize * 7
		if index == 2 {
			wantArguments = 7
		}
		if len(call.arguments) != wantArguments || !strings.Contains(call.query,
			"ON CONFLICT (seller_id, batch_uuid, batch_updated_at) DO NOTHING") ||
			!strings.Contains(call.query, fmt.Sprintf("$%d)", wantArguments)) {
			t.Fatalf("запрос %d: неверный размер или SQL: аргументов %d", index, len(call.arguments))
		}
		if call.arguments[6] != observedAt {
			t.Fatalf("запрос %d: потеряно время наблюдения", index)
		}
	}
	if tx.calls[1].arguments[1] != fmt.Sprintf("batch-%d", errorFeedInsertBatchSize) {
		t.Fatal("порядок записей на границе порций нарушен")
	}
	if empty, err := insertErrorBatches(context.Background(), tx, nil, observedAt); err != nil || empty != 0 || len(tx.calls) != 3 {
		t.Fatalf("пустая порция вызвала SQL: inserted=%d, err=%v, calls=%d", empty, err, len(tx.calls))
	}

	failure := errors.New("database unavailable")
	failingTx := &errorBatchInsertTx{affected: []int64{1}, failureAt: 1, failure: failure}
	if _, err := insertErrorBatches(context.Background(), failingTx, batches, observedAt); !errors.Is(err, failure) {
		t.Fatalf("ошибка второй порции потеряна: %v", err)
	}
}

func TestPostgresSaveErrorFeedBulkInsertReplay(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN не задан")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("подключиться к PostgreSQL: %v", err)
	}
	defer connection.Close(context.Background())
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatalf("начать транзакцию: %v", err)
	}
	defer tx.Rollback(context.Background())

	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		t.Fatal(err)
	}
	sellerID := core_domain.SellerID(fmt.Sprintf("%x-%x-%x-%x-%x",
		identity[0:4], identity[4:6], identity[6:8], identity[8:10], identity[10:16]))
	polledAt := time.Now().UTC().Truncate(time.Microsecond)
	batchTime := polledAt.Add(-time.Minute)
	batches := make([]cardpublication_monitoring.ErrorBatchEvidence, errorFeedInsertBatchSize+3)
	for index := range batches {
		batchIndex := index
		if index == 1 || index == errorFeedInsertBatchSize+1 {
			batchIndex = 0
		}
		batches[index] = cardpublication_monitoring.ErrorBatchEvidence{
			SellerID: sellerID, BatchUUID: fmt.Sprintf("batch-%d", batchIndex), UpdatedAt: batchTime,
			VendorCodes: []string{"vendor"}, RejectedVendorCodes: []string{"vendor"},
			ErrorCodes: []string{"wb_error_1"},
		}
	}
	command := cardpublication_monitoring.SaveErrorFeedCommand{
		SellerID: sellerID,
		Previous: cardpublication_monitoring.ErrorCursor{SellerID: sellerID},
		Next:     cardpublication_monitoring.ErrorCursor{SellerID: sellerID, UpdatedAt: polledAt},
		Batches:  batches, PolledAt: polledAt,
	}
	repository := &MonitoringRepository{}
	saved, err := repository.SaveErrorFeed(ctx, tx, command)
	if err != nil {
		t.Fatalf("сохранить ленту ошибок: %v", err)
	}
	wantInserted := int64(len(batches) - 2)
	var stored int64
	if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM wb.publication_error_batches WHERE seller_id = $1", sellerID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != wantInserted || saved.Revision != wantInserted || !saved.UpdatedAt.Equal(polledAt) {
		t.Fatalf("неверные строки или курсор: stored=%d revision=%d updated=%s", stored, saved.Revision, saved.UpdatedAt)
	}

	command.Previous = saved
	command.Next = cardpublication_monitoring.ErrorCursor{SellerID: sellerID, UpdatedAt: polledAt.Add(-time.Hour)}
	command.PolledAt = polledAt.Add(time.Millisecond)
	replayed, err := repository.SaveErrorFeed(ctx, tx, command)
	if err != nil {
		t.Fatalf("повторить ленту ошибок: %v", err)
	}
	if replayed.Revision != wantInserted || !replayed.UpdatedAt.Equal(polledAt) {
		t.Fatalf("повтор изменил ревизию или вернул курсор назад: %+v", replayed)
	}
	command.Previous = cardpublication_monitoring.ErrorCursor{SellerID: sellerID}
	if _, err := repository.SaveErrorFeed(ctx, tx, command); err == nil {
		t.Fatal("устаревшая ревизия курсора принята")
	}
}
