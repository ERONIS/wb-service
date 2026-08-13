package platform_outbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	platform_transaction "github.com/ERONIS/wb-service/internal/platform/transaction"

	"github.com/jackc/pgx/v5"
)

var ErrInboxConflict = errors.New("inbox event conflict")

type Inbox struct{}

func NewInbox() *Inbox {
	return &Inbox{}
}

// Record must be called in the same caller-owned transaction as consumer
// state changes. applied=false means this business event was already committed
// and the caller must not apply its state transition again.
func (inbox *Inbox) Record(
	ctx context.Context,
	tx platform_transaction.DBTX,
	consumer string,
	event ClaimedEvent,
	appliedRevision int64,
) (applied bool, err error) {
	if tx == nil {
		return false, errors.New("record inbox event: DBTX is nil")
	}
	if err := validateConsumer(consumer); err != nil {
		return false, fmt.Errorf("record inbox event: %w", err)
	}
	if err := event.validate(); err != nil {
		return false, fmt.Errorf("record inbox event: %w", err)
	}
	if appliedRevision < 0 {
		return false, errors.New("record inbox event: applied revision is negative")
	}

	const insert = `
		INSERT INTO wb.platform_inbox (
			consumer,
			event_id,
			event_type,
			aggregate_id,
			aggregate_revision,
			schema_version,
			payload_digest,
			applied_revision
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT DO NOTHING;
	`
	result, err := tx.Exec(
		ctx,
		insert,
		consumer,
		event.ID,
		event.Type,
		event.AggregateID,
		event.AggregateRevision,
		event.SchemaVersion,
		event.PayloadDigest,
		appliedRevision,
	)
	if err != nil {
		return false, fmt.Errorf("insert inbox event: %w", err)
	}
	if result.RowsAffected() == 1 {
		return true, nil
	}

	const selectExisting = `
		SELECT
			event_id,
			event_type,
			aggregate_id,
			aggregate_revision,
			schema_version,
			payload_digest,
			applied_revision
		FROM wb.platform_inbox
		WHERE consumer = $1
			AND (
				event_id = $2
				OR (
					event_type = $3
					AND aggregate_id = $4
					AND aggregate_revision = $5
				)
			)
		ORDER BY (event_id = $2) DESC
		LIMIT 1;
	`
	var (
		storedID                string
		storedType              string
		storedAggregateID       string
		storedAggregateRevision int64
		storedSchemaVersion     int
		storedPayloadDigest     []byte
		storedAppliedRevision   int64
	)
	err = tx.QueryRow(
		ctx,
		selectExisting,
		consumer,
		event.ID,
		event.Type,
		event.AggregateID,
		event.AggregateRevision,
	).Scan(
		&storedID,
		&storedType,
		&storedAggregateID,
		&storedAggregateRevision,
		&storedSchemaVersion,
		&storedPayloadDigest,
		&storedAppliedRevision,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf(
			"%w: conflicting row disappeared",
			ErrInboxConflict,
		)
	}
	if err != nil {
		return false, fmt.Errorf("select existing inbox event: %w", err)
	}

	sameBusinessEvent := storedType == event.Type &&
		storedAggregateID == event.AggregateID &&
		storedAggregateRevision == event.AggregateRevision
	sameEventID := storedID == event.ID
	if (!sameBusinessEvent && sameEventID) ||
		storedSchemaVersion != event.SchemaVersion ||
		!bytes.Equal(storedPayloadDigest, event.PayloadDigest) ||
		storedAppliedRevision != appliedRevision {
		return false, fmt.Errorf(
			"%w: consumer=%q event=%q aggregate=%q revision=%d",
			ErrInboxConflict,
			consumer,
			event.ID,
			event.AggregateID,
			event.AggregateRevision,
		)
	}

	return false, nil
}

func validateConsumer(consumer string) error {
	if strings.TrimSpace(consumer) != consumer || consumer == "" {
		return errors.New("inbox consumer is empty or not normalized")
	}
	if len(consumer) > 128 {
		return errors.New("inbox consumer is too long")
	}
	return nil
}
