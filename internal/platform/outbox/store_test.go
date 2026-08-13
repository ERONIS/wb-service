package platform_outbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type storeDBTX struct {
	rows     []pgx.Row
	rowIndex int
	execTag  pgconn.CommandTag
	execErr  error
	execArgs []any
}

func (db *storeDBTX) Exec(
	_ context.Context,
	_ string,
	args ...any,
) (pgconn.CommandTag, error) {
	db.execArgs = append([]any(nil), args...)
	return db.execTag, db.execErr
}

func (db *storeDBTX) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	return nil, nil
}

func (db *storeDBTX) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	row := db.rows[db.rowIndex]
	db.rowIndex++
	return row
}

func (db *storeDBTX) CopyFrom(
	context.Context,
	pgx.Identifier,
	[]string,
	pgx.CopyFromSource,
) (int64, error) {
	return 0, nil
}

type storeRow func(...any) error

func (row storeRow) Scan(dest ...any) error {
	return row(dest...)
}

func TestPostgresStoreClaimReturnsCurrentLeasedEvent(t *testing.T) {
	t.Parallel()

	now := testDispatcherTime()
	leaseUntil := now.Add(30 * time.Second)
	db := &storeDBTX{rows: []pgx.Row{
		storeRow(func(dest ...any) error {
			*(dest[0].(*string)) = "evt_test"
			*(dest[1].(*string)) = "test.event"
			*(dest[2].(*string)) = "aggregate:1"
			*(dest[3].(*int64)) = 1
			*(dest[4].(*int)) = 1
			*(dest[5].(*json.RawMessage)) = []byte(`{"value":1}`)
			*(dest[6].(*[]byte)) = make([]byte, 32)
			*(dest[7].(*time.Time)) = now
			*(dest[8].(*int)) = 2
			*(dest[9].(*int64)) = 3
			*(dest[10].(*int64)) = 7
			*(dest[11].(*time.Time)) = leaseUntil
			return nil
		}),
	}}
	store := NewPostgresStore(db, "wb-service", 7)

	claimed, found, err := store.Claim(
		context.Background(),
		[]string{"test.event"},
		"leader-1",
		now,
		30*time.Second,
	)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if !found || claimed.ID != "evt_test" {
		t.Fatalf("Claim() found/event = %v/%#v", found, claimed)
	}
	if claimed.Lease.Owner != "leader-1" ||
		claimed.Lease.Fence != 3 ||
		claimed.Lease.ProcessEpoch != 7 {
		t.Fatalf("Claim() lease = %#v", claimed.Lease)
	}
}

func TestPostgresStoreClaimRejectsStaleProcessEpoch(t *testing.T) {
	t.Parallel()

	db := &storeDBTX{rows: []pgx.Row{
		storeRow(func(...any) error { return pgx.ErrNoRows }),
		storeRow(func(dest ...any) error {
			*(dest[0].(*bool)) = false
			return nil
		}),
	}}
	store := NewPostgresStore(db, "wb-service", 7)

	_, _, err := store.Claim(
		context.Background(),
		[]string{"test.event"},
		"leader-1",
		testDispatcherTime(),
		30*time.Second,
	)
	if !errors.Is(err, ErrStaleProcessEpoch) {
		t.Fatalf("Claim() error = %v, want stale epoch", err)
	}
}

func TestPostgresStoreCompleteRequiresExactLeaseFence(t *testing.T) {
	t.Parallel()

	db := &storeDBTX{execTag: pgconn.NewCommandTag("UPDATE 0")}
	store := NewPostgresStore(db, "wb-service", 7)
	lease := Lease{
		EventID:      "evt_test",
		Owner:        "leader-1",
		Fence:        3,
		ProcessEpoch: 7,
		Until:        testDispatcherTime().Add(30 * time.Second),
	}

	err := store.Complete(context.Background(), lease, testDispatcherTime())
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("Complete() error = %v, want lease lost", err)
	}
}

func TestPostgresStoreCompletePersistsDeliveredState(t *testing.T) {
	t.Parallel()

	db := &storeDBTX{execTag: pgconn.NewCommandTag("UPDATE 1")}
	store := NewPostgresStore(db, "wb-service", 7)
	lease := Lease{
		EventID:      "evt_test",
		Owner:        "leader-1",
		Fence:        3,
		ProcessEpoch: 7,
		Until:        testDispatcherTime().Add(30 * time.Second),
	}

	if err := store.Complete(
		context.Background(),
		lease,
		testDispatcherTime(),
	); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got := db.execArgs[6]; got != "delivered" {
		t.Fatalf("stored status = %v, want delivered", got)
	}
}
