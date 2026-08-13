package cardimport_service

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	platform_outbox "github.com/ERONIS/wb-service/internal/platform/outbox"
	platform_transaction "github.com/ERONIS/wb-service/internal/platform/transaction"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type finalizeRepositoryStub struct {
	Repository
	result BatchHeader
	err    error
	tx     platform_transaction.DBTX
	actor  TrustedActor
	cmd    FinalizeCommand
}

func (r *finalizeRepositoryStub) Finalize(
	_ context.Context,
	tx platform_transaction.DBTX,
	actor TrustedActor,
	command FinalizeCommand,
	_ Digest,
) (BatchHeader, error) {
	r.tx = tx
	r.actor = actor
	r.cmd = command
	return r.result, r.err
}

type finalizeUOWStub struct {
	tx    platform_transaction.DBTX
	calls int
}

func (u *finalizeUOWStub) WithinTransaction(
	ctx context.Context,
	fn func(context.Context, platform_transaction.DBTX) error,
) error {
	u.calls++
	return fn(ctx, u.tx)
}

type finalizeOutboxStub struct {
	err   error
	tx    platform_transaction.DBTX
	event platform_outbox.Event
	calls int
}

func (o *finalizeOutboxStub) Append(
	_ context.Context,
	tx platform_transaction.DBTX,
	event platform_outbox.Event,
) (platform_outbox.StoredEvent, error) {
	o.calls++
	o.tx = tx
	o.event = event
	return platform_outbox.StoredEvent{ID: event.ID}, o.err
}

type finalizeDBTXStub struct{}

func (finalizeDBTXStub) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (finalizeDBTXStub) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	return nil, nil
}

func (finalizeDBTXStub) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	return nil
}

func (finalizeDBTXStub) CopyFrom(
	context.Context,
	pgx.Identifier,
	[]string,
	pgx.CopyFromSource,
) (int64, error) {
	return 0, nil
}

func TestFinalizeStoresBatchAndOutboxEventInSameTransaction(t *testing.T) {
	t.Parallel()

	tx := finalizeDBTXStub{}
	header := BatchHeader{
		ID:                   42,
		Purpose:              PurposeTransfer,
		ItemsCount:           3,
		GroupsCount:          2,
		SchemaVersion:        1,
		NormalizationVersion: 1,
		AuthorSnapshot: AuthorSnapshot{
			TelegramUserID: 10,
			DisplayName:    "Test User",
		},
		CreatedAt: time.Now(),
	}
	repository := &finalizeRepositoryStub{result: header}
	uow := &finalizeUOWStub{tx: tx}
	outbox := &finalizeOutboxStub{}
	service := New(repository, parserForTest{}, uow, outbox)

	actor := TrustedActor{TelegramUserID: 10, DisplayName: " Test User "}
	command := FinalizeCommand{
		SessionID:        5,
		ExpectedRevision: 7,
		IdempotencyKey:   " finalize-5-7 ",
	}
	got, err := service.Finalize(context.Background(), actor, command)
	if err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if got.ID != header.ID {
		t.Fatalf("Finalize() batch ID = %d, want %d", got.ID, header.ID)
	}
	if repository.tx != tx || outbox.tx != tx {
		t.Fatal("repository and outbox did not receive the same DBTX")
	}
	if repository.actor.DisplayName != "Test User" ||
		repository.cmd.IdempotencyKey != "finalize-5-7" {
		t.Fatal("Finalize() did not normalize actor/command")
	}
	if outbox.calls != 1 || outbox.event.Type != BatchFinalizedEventType {
		t.Fatalf("outbox event = %#v, calls = %d", outbox.event, outbox.calls)
	}
	if outbox.event.AggregateID != "card-batch:42" ||
		outbox.event.AggregateRevision != 1 {
		t.Fatalf("outbox business key = %q/%d", outbox.event.AggregateID, outbox.event.AggregateRevision)
	}
}

func TestFinalizeDoesNotAppendEventWhenRepositoryFails(t *testing.T) {
	t.Parallel()

	testErr := errors.New("repository failed")
	repository := &finalizeRepositoryStub{err: testErr}
	uow := &finalizeUOWStub{tx: finalizeDBTXStub{}}
	outbox := &finalizeOutboxStub{}
	service := New(repository, parserForTest{}, uow, outbox)

	_, err := service.Finalize(
		context.Background(),
		TrustedActor{TelegramUserID: 10, DisplayName: "Test User"},
		FinalizeCommand{SessionID: 5, ExpectedRevision: 7, IdempotencyKey: "key"},
	)
	if !errors.Is(err, testErr) {
		t.Fatalf("Finalize() error = %v, want %v", err, testErr)
	}
	if outbox.calls != 0 {
		t.Fatalf("outbox calls = %d, want 0", outbox.calls)
	}
}

type parserForTest struct{}

func (parserForTest) Parse(
	FileID,
	io.Reader,
) (ParsedFile, error) {
	return ParsedFile{}, nil
}
