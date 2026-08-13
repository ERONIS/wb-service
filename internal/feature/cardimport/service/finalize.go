package cardimport_service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	platform_outbox "github.com/ERONIS/wb-service/internal/platform/outbox"
	platform_transaction "github.com/ERONIS/wb-service/internal/platform/transaction"
)

const BatchFinalizedEventType = "cardimport.BatchFinalized"

type BatchFinalizedPayload struct {
	BatchID              BatchID `json:"batchId"`
	Checksum             string  `json:"checksum"`
	ItemsCount           int     `json:"itemsCount"`
	GroupsCount          int     `json:"groupsCount"`
	SchemaVersion        int     `json:"schemaVersion"`
	NormalizationVersion int     `json:"normalizationVersion"`
}

func (s *Service) Finalize(
	ctx context.Context,
	actor TrustedActor,
	command FinalizeCommand,
) (BatchHeader, error) {
	actor = actor.normalized()
	command = command.normalized()
	if err := actor.validate(); err != nil {
		return BatchHeader{}, err
	}
	if err := command.validate(); err != nil {
		return BatchHeader{}, err
	}

	commandDigest := command.digest()
	var batch BatchHeader
	err := s.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx platform_transaction.DBTX) error {
			var err error
			batch, err = s.repository.Finalize(
				ctx,
				tx,
				actor,
				command,
				commandDigest,
			)
			if err != nil {
				return err
			}

			payload, err := json.Marshal(BatchFinalizedPayload{
				BatchID:              batch.ID,
				Checksum:             batch.Checksum.String(),
				ItemsCount:           batch.ItemsCount,
				GroupsCount:          batch.GroupsCount,
				SchemaVersion:        batch.SchemaVersion,
				NormalizationVersion: batch.NormalizationVersion,
			})
			if err != nil {
				return fmt.Errorf("marshal BatchFinalized event: %w", err)
			}

			eventID, err := platform_outbox.NewEventID()
			if err != nil {
				return fmt.Errorf("create BatchFinalized event ID: %w", err)
			}
			_, err = s.outbox.Append(ctx, tx, platform_outbox.Event{
				ID:                eventID,
				Type:              BatchFinalizedEventType,
				AggregateID:       "card-batch:" + strconv.FormatInt(int64(batch.ID), 10),
				AggregateRevision: 1,
				SchemaVersion:     1,
				Payload:           payload,
			})
			if err != nil {
				return fmt.Errorf("append BatchFinalized event: %w", err)
			}

			return nil
		},
	)
	if err != nil {
		return BatchHeader{}, fmt.Errorf("finalize cardimport session: %w", err)
	}

	return batch, nil
}

func (s *Service) GetBatch(
	ctx context.Context,
	batchID BatchID,
) (BatchHeader, error) {
	if batchID <= 0 {
		return BatchHeader{}, fmt.Errorf(
			"invalid cardimport batch ID '%d': %w",
			batchID,
			core_errors.ErrInvalidArgument,
		)
	}

	return s.repository.GetBatch(ctx, batchID)
}

func (s *Service) ListBatchItems(
	ctx context.Context,
	batchID BatchID,
	afterPosition int,
	limit int,
) ([]BatchItem, error) {
	switch {
	case batchID <= 0:
		return nil, fmt.Errorf(
			"invalid cardimport batch ID '%d': %w",
			batchID,
			core_errors.ErrInvalidArgument,
		)
	case afterPosition < 0:
		return nil, fmt.Errorf(
			"invalid cardimport batch cursor '%d': %w",
			afterPosition,
			core_errors.ErrInvalidArgument,
		)
	case limit <= 0 || limit > MaxBatchPageSize:
		return nil, fmt.Errorf(
			"invalid cardimport batch page limit '%d': %w",
			limit,
			core_errors.ErrInvalidArgument,
		)
	}

	return s.repository.ListBatchItems(
		ctx,
		batchID,
		afterPosition,
		limit,
	)
}
