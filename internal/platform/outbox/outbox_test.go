package platform_outbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeDBTX struct {
	queryCalls int
	row        pgx.Row
	args       []any
}

func (d *fakeDBTX) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (d *fakeDBTX) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	return nil, nil
}

func (d *fakeDBTX) QueryRow(
	_ context.Context,
	_ string,
	args ...any,
) pgx.Row {
	d.queryCalls++
	d.args = append([]any(nil), args...)
	return d.row
}

func (d *fakeDBTX) CopyFrom(
	context.Context,
	pgx.Identifier,
	[]string,
	pgx.CopyFromSource,
) (int64, error) {
	return 0, nil
}

type fakeRow struct {
	eventID       string
	schemaVersion int
	payloadDigest []byte
	err           error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*string)) = r.eventID
	*(dest[1].(*int)) = r.schemaVersion
	*(dest[2].(*[]byte)) = append([]byte(nil), r.payloadDigest...)
	return nil
}

func validEvent() Event {
	return Event{
		ID:                "event-1",
		Type:              "BatchFinalized",
		AggregateID:       "batch-1",
		AggregateRevision: 1,
		SchemaVersion:     1,
		Payload:           json.RawMessage(`{"batch_id":"batch-1","value":1}`),
	}
}

func newTestWriter() *Writer {
	return NewWriter("wb-service:v1", 1)
}

func TestAppendStoresCanonicalPayload(t *testing.T) {
	t.Parallel()

	event := validEvent()
	canonical, err := canonicalPayload(event.Payload)
	if err != nil {
		t.Fatalf("canonicalPayload() error = %v", err)
	}
	digest := sha256.Sum256(canonical)
	db := &fakeDBTX{row: fakeRow{
		eventID:       event.ID,
		schemaVersion: event.SchemaVersion,
		payloadDigest: digest[:],
	}}
	writer := newTestWriter()
	writer.now = func() time.Time {
		return time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	}

	stored, err := writer.Append(context.Background(), db, event)
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if stored.ID != event.ID {
		t.Fatalf("stored ID = %q, want %q", stored.ID, event.ID)
	}
	if db.queryCalls != 1 {
		t.Fatalf("query calls = %d, want 1", db.queryCalls)
	}
	if got := db.args[5].(string); got != string(canonical) {
		t.Fatalf("canonical payload = %q, want %q", got, canonical)
	}
	if got := db.args[6].([]byte); !bytes.Equal(got, digest[:]) {
		t.Fatalf("payload digest = %x, want %x", got, digest)
	}
}

func TestAppendReturnsExistingLogicalEvent(t *testing.T) {
	t.Parallel()

	event := validEvent()
	canonical, err := canonicalPayload(event.Payload)
	if err != nil {
		t.Fatalf("canonicalPayload() error = %v", err)
	}
	digest := sha256.Sum256(canonical)
	db := &fakeDBTX{row: fakeRow{
		eventID:       "existing-event",
		schemaVersion: event.SchemaVersion,
		payloadDigest: digest[:],
	}}

	stored, err := newTestWriter().Append(context.Background(), db, event)
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if stored.ID != "existing-event" {
		t.Fatalf("stored ID = %q, want existing-event", stored.ID)
	}
}

func TestAppendRejectsLogicalEventWithDifferentPayload(t *testing.T) {
	t.Parallel()

	event := validEvent()
	db := &fakeDBTX{row: fakeRow{
		eventID:       "existing-event",
		schemaVersion: event.SchemaVersion,
		payloadDigest: bytes.Repeat([]byte{0xff}, sha256.Size),
	}}

	_, err := newTestWriter().Append(context.Background(), db, event)
	if !errors.Is(err, ErrBusinessEventConflict) {
		t.Fatalf("Append() error = %v, want business event conflict", err)
	}
}

func TestAppendValidatesBeforeDatabaseCall(t *testing.T) {
	t.Parallel()

	event := validEvent()
	event.ID = ""
	db := &fakeDBTX{}

	_, err := newTestWriter().Append(context.Background(), db, event)
	if err == nil {
		t.Fatal("Append() error = nil, want validation error")
	}
	if db.queryCalls != 0 {
		t.Fatalf("query calls = %d, want 0", db.queryCalls)
	}
}

func TestAppendRejectsStaleProcessEpoch(t *testing.T) {
	t.Parallel()

	db := &fakeDBTX{row: fakeRow{err: pgx.ErrNoRows}}

	_, err := newTestWriter().Append(context.Background(), db, validEvent())
	if !errors.Is(err, ErrStaleProcessEpoch) {
		t.Fatalf("Append() error = %v, want stale process epoch", err)
	}
}

func TestCanonicalPayloadRejectsTrailingJSON(t *testing.T) {
	t.Parallel()

	_, err := canonicalPayload(json.RawMessage(`{} {}`))
	if err == nil {
		t.Fatal("canonicalPayload() error = nil, want trailing JSON error")
	}
}
