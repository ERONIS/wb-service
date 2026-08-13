package platform_outbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	platform_transaction "github.com/ERONIS/wb-service/internal/platform/transaction"

	"github.com/jackc/pgx/v5"
)

var ErrLeaseLost = errors.New("outbox lease or fence is no longer current")

type Lease struct {
	EventID      string
	Owner        string
	Fence        int64
	ProcessEpoch int64
	Until        time.Time
}

type ClaimedEvent struct {
	Event
	PayloadDigest []byte
	Attempts      int
	Lease         Lease
}

type DeliveryStore interface {
	Claim(
		ctx context.Context,
		eventTypes []string,
		owner string,
		now time.Time,
		leaseDuration time.Duration,
	) (ClaimedEvent, bool, error)

	Complete(
		ctx context.Context,
		lease Lease,
		now time.Time,
	) error

	Reschedule(
		ctx context.Context,
		lease Lease,
		now time.Time,
		availableAt time.Time,
		safeErrorCode string,
	) error

	DeadLetter(
		ctx context.Context,
		lease Lease,
		now time.Time,
		safeErrorCode string,
	) error
}

type PostgresStore struct {
	db           platform_transaction.DBTX
	singletonKey string
	processEpoch int64
}

func NewPostgresStore(
	db platform_transaction.DBTX,
	singletonKey string,
	processEpoch int64,
) *PostgresStore {
	if db == nil {
		panic("platform outbox store DBTX is nil")
	}
	if strings.TrimSpace(singletonKey) != singletonKey || singletonKey == "" {
		panic("platform outbox store singleton key is empty or not normalized")
	}
	if len(singletonKey) > 128 {
		panic("platform outbox store singleton key exceeds 128 bytes")
	}
	if processEpoch <= 0 {
		panic("platform outbox store process epoch is not positive")
	}

	return &PostgresStore{
		db:           db,
		singletonKey: singletonKey,
		processEpoch: processEpoch,
	}
}

func (s *PostgresStore) Claim(
	ctx context.Context,
	eventTypes []string,
	owner string,
	now time.Time,
	leaseDuration time.Duration,
) (ClaimedEvent, bool, error) {
	if len(eventTypes) == 0 {
		return ClaimedEvent{}, false, errors.New(
			"claim outbox event: event types are empty",
		)
	}
	if err := validateOwner(owner); err != nil {
		return ClaimedEvent{}, false, fmt.Errorf("claim outbox event: %w", err)
	}
	if now.IsZero() {
		return ClaimedEvent{}, false, errors.New(
			"claim outbox event: current time is empty",
		)
	}
	if leaseDuration <= 0 {
		return ClaimedEvent{}, false, errors.New(
			"claim outbox event: lease duration is not positive",
		)
	}
	for _, eventType := range eventTypes {
		if err := validateEventType(eventType); err != nil {
			return ClaimedEvent{}, false, fmt.Errorf(
				"claim outbox event: %w",
				err,
			)
		}
	}

	now = now.UTC()
	leaseUntil := now.Add(leaseDuration)
	const query = `
		WITH runtime AS MATERIALIZED (
			SELECT epoch
			FROM wb.platform_runtime_epoch
			WHERE singleton_key = $5
				AND epoch = $6
		),
		candidate AS MATERIALIZED (
			SELECT event.event_id
			FROM wb.platform_outbox AS event
			CROSS JOIN runtime
			WHERE event.event_type = ANY($3)
				AND (
					(
						event.status = 'pending'
						AND event.available_at <= $4
					)
					OR (
						event.status = 'processing'
						AND event.lease_until <= $4
					)
				)
			ORDER BY event.available_at, event.event_id
			FOR UPDATE OF event SKIP LOCKED
			LIMIT 1
		)
		UPDATE wb.platform_outbox AS event
		SET
			status = 'processing',
			attempts = event.attempts + 1,
			lease_owner = $1,
			lease_until = $2,
			fence = event.fence + 1,
			process_epoch = $6,
			updated_at = $4
		FROM candidate, runtime
		WHERE event.event_id = candidate.event_id
		RETURNING
			event.event_id,
			event.event_type,
			event.aggregate_id,
			event.aggregate_revision,
			event.schema_version,
			event.payload,
			event.payload_digest,
			event.available_at,
			event.attempts,
			event.fence,
			event.process_epoch,
			event.lease_until;
	`

	var claimed ClaimedEvent
	err := s.db.QueryRow(
		ctx,
		query,
		owner,
		leaseUntil,
		eventTypes,
		now,
		s.singletonKey,
		s.processEpoch,
	).Scan(
		&claimed.ID,
		&claimed.Type,
		&claimed.AggregateID,
		&claimed.AggregateRevision,
		&claimed.SchemaVersion,
		&claimed.Payload,
		&claimed.PayloadDigest,
		&claimed.AvailableAt,
		&claimed.Attempts,
		&claimed.Lease.Fence,
		&claimed.Lease.ProcessEpoch,
		&claimed.Lease.Until,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		current, verifyErr := s.currentEpoch(ctx)
		if verifyErr != nil {
			return ClaimedEvent{}, false, verifyErr
		}
		if !current {
			return ClaimedEvent{}, false, ErrStaleProcessEpoch
		}
		return ClaimedEvent{}, false, nil
	}
	if err != nil {
		return ClaimedEvent{}, false, fmt.Errorf("claim outbox event: %w", err)
	}

	claimed.Lease.EventID = claimed.ID
	claimed.Lease.Owner = owner
	if err := claimed.validate(); err != nil {
		return ClaimedEvent{}, false, fmt.Errorf(
			"validate claimed outbox event: %w",
			err,
		)
	}

	return claimed, true, nil
}

func (s *PostgresStore) Complete(
	ctx context.Context,
	lease Lease,
	now time.Time,
) error {
	return s.finish(
		ctx,
		lease,
		now,
		"delivered",
		now,
		"",
	)
}

func (s *PostgresStore) Reschedule(
	ctx context.Context,
	lease Lease,
	now time.Time,
	availableAt time.Time,
	safeErrorCode string,
) error {
	if availableAt.IsZero() || availableAt.Before(now) {
		return errors.New("reschedule outbox event: invalid available time")
	}
	if err := validateSafeCode(safeErrorCode); err != nil {
		return fmt.Errorf("reschedule outbox event: %w", err)
	}

	return s.finish(
		ctx,
		lease,
		now,
		"pending",
		availableAt,
		safeErrorCode,
	)
}

func (s *PostgresStore) DeadLetter(
	ctx context.Context,
	lease Lease,
	now time.Time,
	safeErrorCode string,
) error {
	if err := validateSafeCode(safeErrorCode); err != nil {
		return fmt.Errorf("dead-letter outbox event: %w", err)
	}

	return s.finish(
		ctx,
		lease,
		now,
		"dead_letter",
		now,
		safeErrorCode,
	)
}

func (s *PostgresStore) finish(
	ctx context.Context,
	lease Lease,
	now time.Time,
	status string,
	availableAt time.Time,
	safeErrorCode string,
) error {
	if err := lease.validate(); err != nil {
		return fmt.Errorf("finish outbox event: %w", err)
	}
	if now.IsZero() {
		return errors.New("finish outbox event: current time is empty")
	}
	if lease.ProcessEpoch != s.processEpoch {
		return ErrLeaseLost
	}

	const query = `
		WITH runtime AS MATERIALIZED (
			SELECT epoch
			FROM wb.platform_runtime_epoch
			WHERE singleton_key = $9
				AND epoch = $6
		)
		UPDATE wb.platform_outbox AS event
		SET
			status = $7,
			available_at = $8,
			lease_owner = NULL,
			lease_until = NULL,
			delivered_at = CASE
				WHEN $7 = 'delivered' THEN $5
				ELSE NULL
			END,
			last_error_code = NULLIF($10, ''),
			updated_at = $5
		FROM runtime
		WHERE event.event_id = $1
			AND event.status = 'processing'
			AND event.lease_owner = $2
			AND event.fence = $3
			AND event.lease_until = $4
			AND event.lease_until > $5
			AND event.process_epoch = $6;
	`
	result, err := s.db.Exec(
		ctx,
		query,
		lease.EventID,
		lease.Owner,
		lease.Fence,
		lease.Until.UTC(),
		now.UTC(),
		s.processEpoch,
		status,
		availableAt.UTC(),
		s.singletonKey,
		safeErrorCode,
	)
	if err != nil {
		return fmt.Errorf("finish outbox event: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}

	return nil
}

func (s *PostgresStore) currentEpoch(ctx context.Context) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1
			FROM wb.platform_runtime_epoch
			WHERE singleton_key = $1 AND epoch = $2
		);
	`
	var current bool
	if err := s.db.QueryRow(
		ctx,
		query,
		s.singletonKey,
		s.processEpoch,
	).Scan(&current); err != nil {
		return false, fmt.Errorf("verify outbox process epoch: %w", err)
	}
	return current, nil
}

func (event ClaimedEvent) validate() error {
	if err := event.Event.validate(len(event.Payload)); err != nil {
		return err
	}
	if len(event.PayloadDigest) != 32 {
		return errors.New("payload digest has invalid length")
	}
	if event.Attempts <= 0 {
		return errors.New("attempts must be positive")
	}
	if event.Lease.EventID != event.ID {
		return errors.New("lease event ID differs from event")
	}
	return event.Lease.validate()
}

func (lease Lease) validate() error {
	switch {
	case strings.TrimSpace(lease.EventID) != lease.EventID || lease.EventID == "":
		return errors.New("lease event ID is empty or not normalized")
	case len(lease.EventID) > maxEventIDLength:
		return errors.New("lease event ID is too long")
	case validateOwner(lease.Owner) != nil:
		return validateOwner(lease.Owner)
	case lease.Fence <= 0:
		return errors.New("lease fence must be positive")
	case lease.ProcessEpoch <= 0:
		return errors.New("lease process epoch must be positive")
	case lease.Until.IsZero():
		return errors.New("lease deadline is empty")
	default:
		return nil
	}
}

func validateOwner(owner string) error {
	if strings.TrimSpace(owner) != owner || owner == "" {
		return errors.New("lease owner is empty or not normalized")
	}
	if len(owner) > 128 {
		return errors.New("lease owner is too long")
	}
	return nil
}

func validateEventType(eventType string) error {
	if strings.TrimSpace(eventType) != eventType || eventType == "" {
		return errors.New("event type is empty or not normalized")
	}
	if len(eventType) > maxEventTypeLength {
		return errors.New("event type is too long")
	}
	return nil
}

func validateSafeCode(code string) error {
	if strings.TrimSpace(code) != code || code == "" {
		return errors.New("safe error code is empty or not normalized")
	}
	if len(code) > 128 {
		return errors.New("safe error code is too long")
	}
	for _, character := range code {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '-' || character == '.' {
			continue
		}
		return errors.New("safe error code contains unsupported characters")
	}
	return nil
}
