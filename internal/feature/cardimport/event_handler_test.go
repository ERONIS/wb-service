package cardimport

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	platform_outbox "github.com/ERONIS/wb-service/internal/platform/outbox"
)

type batchConsumerStub struct {
	batchIDs []cardimport_service.BatchID
	err      error
}

func (consumer *batchConsumerStub) Start(
	_ context.Context,
	batchID cardimport_service.BatchID,
) error {
	consumer.batchIDs = append(consumer.batchIDs, batchID)
	return consumer.err
}

func TestBatchFinalizedHandlerStartsConsumer(t *testing.T) {
	t.Parallel()

	consumer := &batchConsumerStub{}
	handler := NewBatchFinalizedHandler(consumer)
	event := validBatchFinalizedEvent(t)

	if err := handler.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(consumer.batchIDs) != 1 || consumer.batchIDs[0] != 42 {
		t.Fatalf("consumer batch IDs = %#v", consumer.batchIDs)
	}
}

func TestBatchFinalizedHandlerRejectsMismatchedAggregate(t *testing.T) {
	t.Parallel()

	consumer := &batchConsumerStub{}
	handler := NewBatchFinalizedHandler(consumer)
	event := validBatchFinalizedEvent(t)
	event.AggregateID = "card-batch:43"

	if err := handler.Handle(context.Background(), event); err == nil {
		t.Fatal("Handle() error = nil, want permanent validation error")
	}
	if len(consumer.batchIDs) != 0 {
		t.Fatalf("consumer batch IDs = %#v, want none", consumer.batchIDs)
	}
}

func TestBatchFinalizedHandlerKeepsConsumerFailureRetryable(t *testing.T) {
	t.Parallel()

	testErr := errors.New("transfer unavailable")
	consumer := &batchConsumerStub{err: testErr}
	handler := NewBatchFinalizedHandler(consumer)

	err := handler.Handle(context.Background(), validBatchFinalizedEvent(t))
	if !errors.Is(err, testErr) {
		t.Fatalf("Handle() error = %v, want %v", err, testErr)
	}
}

func validBatchFinalizedEvent(t *testing.T) platform_outbox.ClaimedEvent {
	t.Helper()
	payload, err := json.Marshal(cardimport_service.BatchFinalizedPayload{
		BatchID:              42,
		Checksum:             "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ItemsCount:           3,
		GroupsCount:          2,
		SchemaVersion:        1,
		NormalizationVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return platform_outbox.ClaimedEvent{
		Event: platform_outbox.Event{
			ID:                "evt_batch",
			Type:              cardimport_service.BatchFinalizedEventType,
			AggregateID:       "card-batch:42",
			AggregateRevision: 1,
			SchemaVersion:     1,
			Payload:           payload,
		},
	}
}
