package platform_outbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	platform_transaction "github.com/ERONIS/wb-service/internal/platform/transaction"

	"github.com/jackc/pgx/v5"
)

const (
	maxEventIDLength     = 128
	maxEventTypeLength   = 128
	maxAggregateIDLength = 256
	maxPayloadBytes      = 64 * 1024
)

var ErrBusinessEventConflict = errors.New("outbox business event conflict")
var ErrStaleProcessEpoch = errors.New("outbox process epoch is stale")

func NewEventID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("read random outbox event ID: %w", err)
	}

	return "evt_" + hex.EncodeToString(random[:]), nil
}

type Event struct {
	ID                string
	Type              string
	AggregateID       string
	AggregateRevision int64
	SchemaVersion     int
	Payload           json.RawMessage
	AvailableAt       time.Time
}

type StoredEvent struct {
	ID string
}

type Appender interface {
	Append(
		ctx context.Context,
		tx platform_transaction.DBTX,
		event Event,
	) (StoredEvent, error)
}

type Writer struct {
	now          func() time.Time
	singletonKey string
	processEpoch int64
}

func NewWriter(singletonKey string, processEpoch int64) *Writer {
	if strings.TrimSpace(singletonKey) != singletonKey || singletonKey == "" {
		panic("platform outbox singleton key is empty or not normalized")
	}
	if len(singletonKey) > 128 {
		panic("platform outbox singleton key exceeds 128 bytes")
	}
	if processEpoch <= 0 {
		panic("platform outbox process epoch is not positive")
	}

	return &Writer{
		now:          time.Now,
		singletonKey: singletonKey,
		processEpoch: processEpoch,
	}
}

func (w *Writer) Append(
	ctx context.Context,
	tx platform_transaction.DBTX,
	event Event,
) (StoredEvent, error) {
	if tx == nil {
		return StoredEvent{}, errors.New("append outbox event: DBTX is nil")
	}
	if len(event.Payload) > maxPayloadBytes {
		return StoredEvent{}, fmt.Errorf(
			"append outbox event: payload exceeds %d bytes",
			maxPayloadBytes,
		)
	}

	payload, err := canonicalPayload(event.Payload)
	if err != nil {
		return StoredEvent{}, fmt.Errorf("append outbox event: %w", err)
	}
	if err := event.validate(len(payload)); err != nil {
		return StoredEvent{}, fmt.Errorf("append outbox event: %w", err)
	}

	availableAt := event.AvailableAt
	if availableAt.IsZero() {
		availableAt = w.now().UTC()
	}
	payloadDigest := sha256.Sum256(payload)

	const query = `
		INSERT INTO wb.platform_outbox (
			event_id,
			event_type,
			aggregate_id,
			aggregate_revision,
			schema_version,
			payload,
			payload_digest,
			process_epoch,
			available_at
		)
		SELECT
			$1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9
		FROM wb.platform_runtime_epoch AS runtime
		WHERE runtime.singleton_key = $10
			AND runtime.epoch = $8
		ON CONFLICT (event_type, aggregate_id, aggregate_revision)
		DO UPDATE SET event_type = EXCLUDED.event_type
		RETURNING event_id, schema_version, payload_digest;
	`

	var (
		storedID            string
		storedSchemaVersion int
		storedPayloadDigest []byte
	)
	if err := tx.QueryRow(
		ctx,
		query,
		event.ID,
		event.Type,
		event.AggregateID,
		event.AggregateRevision,
		event.SchemaVersion,
		string(payload),
		payloadDigest[:],
		w.processEpoch,
		availableAt,
		w.singletonKey,
	).Scan(
		&storedID,
		&storedSchemaVersion,
		&storedPayloadDigest,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StoredEvent{}, ErrStaleProcessEpoch
		}
		return StoredEvent{}, fmt.Errorf("store outbox event: %w", err)
	}

	if storedSchemaVersion != event.SchemaVersion ||
		!bytes.Equal(storedPayloadDigest, payloadDigest[:]) {
		return StoredEvent{}, fmt.Errorf(
			"%w: type=%q aggregate=%q revision=%d",
			ErrBusinessEventConflict,
			event.Type,
			event.AggregateID,
			event.AggregateRevision,
		)
	}

	return StoredEvent{ID: storedID}, nil
}

func (e Event) validate(payloadSize int) error {
	switch {
	case strings.TrimSpace(e.ID) != e.ID || e.ID == "":
		return errors.New("event ID is empty or not normalized")
	case len(e.ID) > maxEventIDLength:
		return fmt.Errorf("event ID exceeds %d bytes", maxEventIDLength)
	case strings.TrimSpace(e.Type) != e.Type || e.Type == "":
		return errors.New("event type is empty or not normalized")
	case len(e.Type) > maxEventTypeLength:
		return fmt.Errorf("event type exceeds %d bytes", maxEventTypeLength)
	case strings.TrimSpace(e.AggregateID) != e.AggregateID || e.AggregateID == "":
		return errors.New("aggregate ID is empty or not normalized")
	case len(e.AggregateID) > maxAggregateIDLength:
		return fmt.Errorf("aggregate ID exceeds %d bytes", maxAggregateIDLength)
	case e.AggregateRevision < 0:
		return errors.New("aggregate revision is negative")
	case e.SchemaVersion <= 0:
		return errors.New("schema version must be positive")
	case payloadSize > maxPayloadBytes:
		return fmt.Errorf("payload exceeds %d bytes", maxPayloadBytes)
	default:
		return nil
	}
}

func canonicalPayload(payload json.RawMessage) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}

	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode canonical payload: %w", err)
	}

	return canonical, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
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
