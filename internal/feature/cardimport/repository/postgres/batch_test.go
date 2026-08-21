package cardimport_postgres_repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type scriptedDBTX struct {
	rows          []pgx.Row
	queryRowCalls int
}

func (d *scriptedDBTX) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (d *scriptedDBTX) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	return nil, nil
}

func (d *scriptedDBTX) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	row := d.rows[d.queryRowCalls]
	d.queryRowCalls++
	return row
}

func (d *scriptedDBTX) CopyFrom(
	context.Context,
	pgx.Identifier,
	[]string,
	pgx.CopyFromSource,
) (int64, error) {
	return 0, nil
}

type scanRow func(...any) error

func (row scanRow) Scan(dest ...any) error {
	return row(dest...)
}

func TestFinalizedReplayChecksOwnerBeforeReturningBatch(t *testing.T) {
	t.Parallel()

	digest := cardimport_service.Digest{1}
	db := &scriptedDBTX{rows: []pgx.Row{
		finalizedSessionRow(10, 42, 7, "key", digest),
	}}
	repository := &Repository{}

	_, err := repository.Finalize(
		context.Background(),
		db,
		cardimport_service.TrustedActor{
			TelegramUserID: 11,
			DisplayName:    "Other User",
		},
		cardimport_service.FinalizeCommand{
			SessionID:        5,
			ExpectedRevision: 7,
			IdempotencyKey:   "key",
		},
		digest,
	)
	if !errors.Is(err, core_errors.ErrForbidden) {
		t.Fatalf("Finalize() error = %v, want forbidden", err)
	}
	if db.queryRowCalls != 1 {
		t.Fatalf("QueryRow calls = %d, batch must not be read", db.queryRowCalls)
	}
}

func TestFinalizedExactReplayReturnsFrozenBatch(t *testing.T) {
	t.Parallel()

	digest := cardimport_service.Digest{1}
	now := time.Now().UTC()
	snapshot := cardimport_service.AuthorSnapshot{
		TelegramUserID: 10,
		DisplayName:    "Test User",
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	checksum := cardimport_service.Digest{2}
	db := &scriptedDBTX{rows: []pgx.Row{
		finalizedSessionRow(10, 42, 7, "key", digest),
		scanRow(func(dest ...any) error {
			*(dest[0].(*int64)) = 42
			*(dest[1].(*cardimport_service.Purpose)) = cardimport_service.PurposeTransfer
			*(dest[2].(*int)) = 3
			*(dest[3].(*int)) = 2
			*(dest[4].(*int)) = 1
			*(dest[5].(*int)) = 1
			*(dest[6].(*[]byte)) = append([]byte(nil), checksum[:]...)
			*(dest[7].(*[]byte)) = append([]byte(nil), snapshotJSON...)
			*(dest[8].(*time.Time)) = now
			return nil
		}),
	}}
	repository := &Repository{}

	batch, err := repository.Finalize(
		context.Background(),
		db,
		cardimport_service.TrustedActor{
			TelegramUserID: 10,
			DisplayName:    "Test User",
		},
		cardimport_service.FinalizeCommand{
			SessionID:        5,
			ExpectedRevision: 7,
			IdempotencyKey:   "key",
		},
		digest,
	)
	if err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if batch.ID != 42 || batch.Checksum != checksum {
		t.Fatalf("Finalize() batch = %#v", batch)
	}
}

func finalizedSessionRow(
	owner int64,
	batchID int64,
	revision int64,
	key string,
	digest cardimport_service.Digest,
) pgx.Row {
	return scanRow(func(dest ...any) error {
		*(dest[0].(**int64)) = nil
		*(dest[1].(*int64)) = owner
		*(dest[2].(*cardimport_service.Purpose)) = cardimport_service.PurposeTransfer
		*(dest[3].(*cardimport_service.SessionStatus)) = cardimport_service.SessionStatusFinalized
		*(dest[4].(*int64)) = revision + 1
		*(dest[5].(**int64)) = &batchID
		*(dest[6].(**int64)) = &revision
		*(dest[7].(**string)) = &key
		*(dest[8].(*[]byte)) = append([]byte(nil), digest[:]...)
		return nil
	})
}
