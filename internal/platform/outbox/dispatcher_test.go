package platform_outbox

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type deliveryStoreStub struct {
	claimed         ClaimedEvent
	found           bool
	claimErr        error
	claimTypes      []string
	completeErr     error
	completeCalls   int
	rescheduleCalls int
	rescheduleAt    time.Time
	rescheduleCode  string
	deadLetterCalls int
	deadLetterCode  string
}

func (store *deliveryStoreStub) Claim(
	_ context.Context,
	eventTypes []string,
	_ string,
	_ time.Time,
	_ time.Duration,
) (ClaimedEvent, bool, error) {
	store.claimTypes = append([]string(nil), eventTypes...)
	return store.claimed, store.found, store.claimErr
}

func (store *deliveryStoreStub) Complete(
	context.Context,
	Lease,
	time.Time,
) error {
	store.completeCalls++
	return store.completeErr
}

func (store *deliveryStoreStub) Reschedule(
	_ context.Context,
	_ Lease,
	_ time.Time,
	availableAt time.Time,
	safeErrorCode string,
) error {
	store.rescheduleCalls++
	store.rescheduleAt = availableAt
	store.rescheduleCode = safeErrorCode
	return nil
}

func (store *deliveryStoreStub) DeadLetter(
	_ context.Context,
	_ Lease,
	_ time.Time,
	safeErrorCode string,
) error {
	store.deadLetterCalls++
	store.deadLetterCode = safeErrorCode
	return nil
}

func TestDispatcherClaimsOnlyRegisteredEventTypes(t *testing.T) {
	t.Parallel()

	store := &deliveryStoreStub{}
	dispatcher := newTestDispatcher(t, store,
		HandlerFunc{Type: "z.event", Func: successfulHandler},
		HandlerFunc{Type: "a.event", Func: successfulHandler},
	)

	_, _, err := dispatcher.claim(context.Background())
	if err != nil {
		t.Fatalf("claim() error = %v", err)
	}
	if !reflect.DeepEqual(store.claimTypes, []string{"a.event", "z.event"}) {
		t.Fatalf("claimed types = %#v", store.claimTypes)
	}
}

func TestDispatcherCompletesOnlyAfterSuccessfulHandler(t *testing.T) {
	t.Parallel()

	store := &deliveryStoreStub{}
	dispatcher := newTestDispatcher(t, store,
		HandlerFunc{Type: "test.event", Func: successfulHandler},
	)
	dispatcher.now = func() time.Time { return testDispatcherTime() }

	if err := dispatcher.deliver(context.Background(), testClaimedEvent(1)); err != nil {
		t.Fatalf("deliver() error = %v", err)
	}
	if store.completeCalls != 1 || store.rescheduleCalls != 0 || store.deadLetterCalls != 0 {
		t.Fatalf(
			"complete/retry/dead calls = %d/%d/%d",
			store.completeCalls,
			store.rescheduleCalls,
			store.deadLetterCalls,
		)
	}
}

func TestDispatcherReschedulesRetryableFailure(t *testing.T) {
	t.Parallel()

	store := &deliveryStoreStub{}
	dispatcher := newTestDispatcher(t, store, HandlerFunc{
		Type: "test.event",
		Func: func(context.Context, ClaimedEvent) error {
			return RetryableDeliveryError(
				"temporary_failure",
				7*time.Second,
				errors.New("temporary details"),
			)
		},
	})
	now := testDispatcherTime()
	dispatcher.now = func() time.Time { return now }

	if err := dispatcher.deliver(context.Background(), testClaimedEvent(1)); err != nil {
		t.Fatalf("deliver() error = %v", err)
	}
	if store.rescheduleCalls != 1 || store.rescheduleCode != "temporary_failure" {
		t.Fatalf(
			"reschedule calls/code = %d/%q",
			store.rescheduleCalls,
			store.rescheduleCode,
		)
	}
	if !store.rescheduleAt.Equal(now.Add(7 * time.Second)) {
		t.Fatalf("reschedule time = %v", store.rescheduleAt)
	}
}

func TestDispatcherDeadLettersPermanentFailure(t *testing.T) {
	t.Parallel()

	store := &deliveryStoreStub{}
	dispatcher := newTestDispatcher(t, store, HandlerFunc{
		Type: "test.event",
		Func: func(context.Context, ClaimedEvent) error {
			return PermanentDeliveryError(
				"invalid_event",
				errors.New("invalid details"),
			)
		},
	})
	dispatcher.now = func() time.Time { return testDispatcherTime() }

	if err := dispatcher.deliver(context.Background(), testClaimedEvent(1)); err != nil {
		t.Fatalf("deliver() error = %v", err)
	}
	if store.deadLetterCalls != 1 || store.deadLetterCode != "invalid_event" {
		t.Fatalf(
			"dead-letter calls/code = %d/%q",
			store.deadLetterCalls,
			store.deadLetterCode,
		)
	}
}

func TestDispatcherDeadLettersAfterMaximumAttempts(t *testing.T) {
	t.Parallel()

	store := &deliveryStoreStub{}
	dispatcher := newTestDispatcher(t, store, HandlerFunc{
		Type: "test.event",
		Func: func(context.Context, ClaimedEvent) error {
			return errors.New("unclassified transient details")
		},
	})
	dispatcher.config.MaxAttempts = 2
	dispatcher.now = func() time.Time { return testDispatcherTime() }

	if err := dispatcher.deliver(context.Background(), testClaimedEvent(2)); err != nil {
		t.Fatalf("deliver() error = %v", err)
	}
	if store.deadLetterCalls != 1 || store.deadLetterCode != "handler_failed" {
		t.Fatalf(
			"dead-letter calls/code = %d/%q",
			store.deadLetterCalls,
			store.deadLetterCode,
		)
	}
}

func TestDispatcherRedeliversAfterAckFailure(t *testing.T) {
	t.Parallel()

	handlerCalls := 0
	store := &deliveryStoreStub{completeErr: errors.New("connection lost")}
	dispatcher := newTestDispatcher(t, store, HandlerFunc{
		Type: "test.event",
		Func: func(context.Context, ClaimedEvent) error {
			handlerCalls++
			return nil
		},
	})
	dispatcher.now = func() time.Time { return testDispatcherTime() }
	claimed := testClaimedEvent(1)

	if err := dispatcher.deliver(context.Background(), claimed); err == nil {
		t.Fatal("first deliver() error = nil, want ack failure")
	}
	store.completeErr = nil
	claimed.Attempts++
	claimed.Lease.Fence++
	if err := dispatcher.deliver(context.Background(), claimed); err != nil {
		t.Fatalf("second deliver() error = %v", err)
	}
	if handlerCalls != 2 {
		t.Fatalf("handler calls = %d, want at-least-once replay", handlerCalls)
	}
}

func newTestDispatcher(
	t *testing.T,
	store DeliveryStore,
	handlers ...Handler,
) *Dispatcher {
	t.Helper()
	config := DefaultDispatcherConfig("test-owner")
	config.BaseRetryDelay = time.Second
	config.MaxRetryDelay = time.Minute
	dispatcher, err := NewDispatcher(store, config, handlers...)
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}
	return dispatcher
}

func successfulHandler(context.Context, ClaimedEvent) error {
	return nil
}

func testClaimedEvent(attempts int) ClaimedEvent {
	now := testDispatcherTime()
	return ClaimedEvent{
		Event: Event{
			ID:                "evt_test",
			Type:              "test.event",
			AggregateID:       "aggregate:1",
			AggregateRevision: 1,
			SchemaVersion:     1,
			Payload:           []byte(`{"value":1}`),
		},
		PayloadDigest: make([]byte, 32),
		Attempts:      attempts,
		Lease: Lease{
			EventID:      "evt_test",
			Owner:        "test-owner",
			Fence:        int64(attempts),
			ProcessEpoch: 1,
			Until:        now.Add(30 * time.Second),
		},
	}
}

func testDispatcherTime() time.Time {
	return time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
}
