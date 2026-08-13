package platform_outbox

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type inboxDBTX struct {
	inserted bool
	row      pgx.Row
}

func (db *inboxDBTX) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	if db.inserted {
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	return pgconn.NewCommandTag("INSERT 0 0"), nil
}

func (db *inboxDBTX) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	return nil, nil
}

func (db *inboxDBTX) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	return db.row
}

func (db *inboxDBTX) CopyFrom(
	context.Context,
	pgx.Identifier,
	[]string,
	pgx.CopyFromSource,
) (int64, error) {
	return 0, nil
}

type inboxRow struct {
	event    ClaimedEvent
	revision int64
}

func (row inboxRow) Scan(dest ...any) error {
	*(dest[0].(*string)) = row.event.ID
	*(dest[1].(*string)) = row.event.Type
	*(dest[2].(*string)) = row.event.AggregateID
	*(dest[3].(*int64)) = row.event.AggregateRevision
	*(dest[4].(*int)) = row.event.SchemaVersion
	*(dest[5].(*[]byte)) = append([]byte(nil), row.event.PayloadDigest...)
	*(dest[6].(*int64)) = row.revision
	return nil
}

func TestInboxRecordReturnsAppliedForNewEvent(t *testing.T) {
	t.Parallel()

	db := &inboxDBTX{inserted: true}
	applied, err := NewInbox().Record(
		context.Background(),
		db,
		"transfer",
		testClaimedEvent(1),
		3,
	)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if !applied {
		t.Fatal("Record() applied = false, want true")
	}
}

func TestInboxRecordTreatsSameBusinessEventAsDuplicate(t *testing.T) {
	t.Parallel()

	event := testClaimedEvent(1)
	stored := event
	stored.ID = "evt_original"
	db := &inboxDBTX{row: inboxRow{event: stored, revision: 3}}
	applied, err := NewInbox().Record(
		context.Background(),
		db,
		"transfer",
		event,
		3,
	)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if applied {
		t.Fatal("Record() applied = true, want duplicate false")
	}
}

func TestInboxRecordRejectsChangedPayload(t *testing.T) {
	t.Parallel()

	event := testClaimedEvent(1)
	stored := event
	stored.PayloadDigest = bytes.Repeat([]byte{1}, 32)
	db := &inboxDBTX{row: inboxRow{event: stored, revision: 3}}
	_, err := NewInbox().Record(
		context.Background(),
		db,
		"transfer",
		event,
		3,
	)
	if !errors.Is(err, ErrInboxConflict) {
		t.Fatalf("Record() error = %v, want inbox conflict", err)
	}
}
