package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type BatchSourceKind string

const BatchSourceWBCabinet BatchSourceKind = "wb_cabinet"

type CreateExternalBatchCommand struct {
	SourceKind        BatchSourceKind
	SourceReferenceID int64
	Cards             []AggregatedCard
}

type ExternalBatchDraft struct {
	SourceKind        BatchSourceKind
	SourceReferenceID int64
	Purpose           Purpose
	Items             []BatchItem
	GroupsCount       int
	Checksum          Digest
	AuthorSnapshot    AuthorSnapshot
}

func (draft ExternalBatchDraft) Validate() error {
	if draft.SourceKind != BatchSourceWBCabinet || draft.SourceReferenceID <= 0 ||
		draft.Purpose != PurposeTransfer || len(draft.Items) == 0 ||
		draft.GroupsCount <= 0 || draft.GroupsCount > len(draft.Items) ||
		draft.Checksum == (Digest{}) || draft.AuthorSnapshot.TelegramUserID <= 0 ||
		strings.TrimSpace(draft.AuthorSnapshot.DisplayName) == "" {
		return core_errors.InvalidArgument("external card batch draft is invalid")
	}
	for index, item := range draft.Items {
		if item.Position != index+1 || item.SourceFileID != FileID(draft.SourceReferenceID) {
			return core_errors.InvalidArgument("external card batch item %d is invalid", index+1)
		}
		if err := item.Validate(); err != nil {
			return err
		}
	}
	checksum, err := BatchChecksum(draft.Purpose, draft.GroupsCount, draft.Items)
	if err != nil || checksum != draft.Checksum {
		return core_errors.Conflict("external card batch checksum is invalid")
	}
	return nil
}

func (service *Service) CreateExternalBatch(
	ctx context.Context,
	user domain.User,
	command CreateExternalBatchCommand,
) (BatchHeader, error) {
	if err := core_errors.RequireContext(ctx); err != nil {
		return BatchHeader{}, err
	}
	if err := user.Validate(); err != nil {
		return BatchHeader{}, err
	}
	if command.SourceKind != BatchSourceWBCabinet || command.SourceReferenceID <= 0 || len(command.Cards) == 0 {
		return BatchHeader{}, core_errors.InvalidArgument("external card batch command is invalid")
	}

	items := make([]BatchItem, len(command.Cards))
	groups := make(map[SourceGroupKey]struct{})
	for index, source := range command.Cards {
		card := source
		card.SourceRows = []int{index + 1}
		card.Variant.VendorCode = strings.TrimSpace(card.Variant.VendorCode)
		if card.Variant.VendorCode == "" {
			return BatchHeader{}, core_errors.InvalidArgument("external card at position %d has empty vendor code", index+1)
		}
		key := NewSourceGroupKey(
			FileID(command.SourceReferenceID),
			card.Group,
			card.Variant.VendorCode,
		)
		groups[key] = struct{}{}
		items[index] = BatchItem{
			Position:       index + 1,
			SourceFileID:   FileID(command.SourceReferenceID),
			SourceRows:     append([]int(nil), card.SourceRows...),
			SourceGroupKey: key,
			VendorCode:     card.Variant.VendorCode,
			Payload:        card,
		}
	}
	checksum, err := BatchChecksum(PurposeTransfer, len(groups), items)
	if err != nil {
		return BatchHeader{}, fmt.Errorf("calculate external card batch checksum: %w", err)
	}

	var authorID *int64
	if user.ID > 0 {
		authorID = &user.ID
	}

	draft := ExternalBatchDraft{
		SourceKind:        command.SourceKind,
		SourceReferenceID: command.SourceReferenceID,
		Purpose:           PurposeTransfer,
		Items:             items,
		GroupsCount:       len(groups),
		Checksum:          checksum,
		AuthorSnapshot: AuthorSnapshot{
			UserID:         authorID,
			TelegramUserID: user.TelegramID,
			DisplayName:    user.FullName,
		},
	}
	if err := draft.Validate(); err != nil {
		return BatchHeader{}, err
	}

	var batch BatchHeader
	err = service.uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		var createErr error
		batch, createErr = service.repository.CreateExternalBatch(ctx, tx, draft)
		return createErr
	})
	if err != nil {
		return BatchHeader{}, fmt.Errorf("persist external card batch: %w", err)
	}
	return batch, nil
}
