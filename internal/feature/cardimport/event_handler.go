package cardimport

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	platform_outbox "github.com/ERONIS/wb-service/internal/platform/outbox"
)

type batchFinalizedHandler struct {
	consumer cardimport_service.BatchConsumer
}

func NewBatchFinalizedHandler(
	consumer cardimport_service.BatchConsumer,
) platform_outbox.Handler {
	if consumer == nil {
		panic("cardimport BatchFinalized consumer is nil")
	}
	return &batchFinalizedHandler{consumer: consumer}
}

func (handler *batchFinalizedHandler) EventType() string {
	return cardimport_service.BatchFinalizedEventType
}

func (handler *batchFinalizedHandler) Handle(
	ctx context.Context,
	event platform_outbox.ClaimedEvent,
) error {
	if event.Type != cardimport_service.BatchFinalizedEventType ||
		event.SchemaVersion != 1 ||
		event.AggregateRevision != 1 {
		return invalidBatchFinalizedEvent(
			errors.New("event envelope is unsupported"),
		)
	}

	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	var payload cardimport_service.BatchFinalizedPayload
	if err := decoder.Decode(&payload); err != nil {
		return invalidBatchFinalizedEvent(fmt.Errorf(
			"decode payload: %w",
			err,
		))
	}
	if err := ensurePayloadEOF(decoder); err != nil {
		return invalidBatchFinalizedEvent(err)
	}
	if payload.BatchID <= 0 ||
		payload.ItemsCount <= 0 ||
		payload.GroupsCount <= 0 ||
		payload.GroupsCount > payload.ItemsCount ||
		payload.SchemaVersion != cardimport_service.BatchSchemaVersion ||
		payload.NormalizationVersion != cardimport_service.BatchNormalizationVersion ||
		!validChecksum(payload.Checksum) {
		return invalidBatchFinalizedEvent(
			errors.New("payload fields are invalid"),
		)
	}
	expectedAggregate := "card-batch:" + strconv.FormatInt(
		int64(payload.BatchID),
		10,
	)
	if event.AggregateID != expectedAggregate {
		return invalidBatchFinalizedEvent(
			errors.New("aggregate ID differs from payload batch ID"),
		)
	}

	if err := handler.consumer.Start(ctx, payload.BatchID); err != nil {
		return platform_outbox.RetryableDeliveryError(
			"batch_consumer_failed",
			0,
			fmt.Errorf("start transfer from finalized batch: %w", err),
		)
	}

	return nil
}

func validChecksum(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func invalidBatchFinalizedEvent(err error) error {
	return platform_outbox.PermanentDeliveryError(
		"invalid_batch_finalized_event",
		err,
	)
}

func ensurePayloadEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode trailing payload: %w", err)
	}
	return errors.New("payload contains multiple JSON values")
}
