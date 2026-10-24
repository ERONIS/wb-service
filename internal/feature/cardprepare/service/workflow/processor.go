package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardprepare_model "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
	cardprepare_preparation "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/preparation"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	preparationTransferPageSize = 100
	preparationGroupBatchSize   = 100
	preparationConcurrency      = 8
)

type Processor struct {
	preparer       *cardprepare_preparation.Preparer
	transferSource TransferSource
	resultApplier  TransferResultApplier
	uow            core_postgres_transaction.UnitOfWork
	logger         *zap.Logger
	mu             sync.Mutex
	cursors        map[transfer_model.TransferID]transfer_preparation.PreparationPendingGroup
}

func NewProcessor(
	preparer *cardprepare_preparation.Preparer,
	transferSource TransferSource,
	resultApplier TransferResultApplier,
	uow core_postgres_transaction.UnitOfWork,
	loggers ...*zap.Logger,
) *Processor {
	if preparer == nil || transferSource == nil || resultApplier == nil || uow == nil {
		panic("cardprepare processor dependency is nil")
	}
	return &Processor{
		preparer:       preparer,
		transferSource: transferSource,
		resultApplier:  resultApplier,
		uow:            uow,
		logger:         core_logger.Fallback(loggers...),
		cursors:        make(map[transfer_model.TransferID]transfer_preparation.PreparationPendingGroup),
	}
}

// ProcessPendingWithProgress выполняет обход готовых к подготовке переносов и возвращает флаг реального продвижения.
func (processor *Processor) ProcessPendingWithProgress(ctx context.Context) (bool, error) {
	progress := false
	err := processor.ProcessPendingWithNotify(ctx, func() { progress = true })
	return progress, err
}

// ProcessPendingWithNotify сообщает о сохранённой порции до завершения общего обхода.
func (processor *Processor) ProcessPendingWithNotify(ctx context.Context, onProgress func()) error {
	if ctx == nil {
		return errors.New("process pending card preparations: context is nil")
	}
	processor.mu.Lock()
	defer processor.mu.Unlock()
	seen := make(map[transfer_model.TransferID]bool)
	var afterID transfer_model.TransferID
	var firstErr error
	for {
		transfers, err := processor.transferSource.ListPreparing(ctx, afterID, preparationTransferPageSize)
		if err != nil {
			return fmt.Errorf("list card preparation transfers: %w", err)
		}
		for _, transfer := range transfers {
			seen[transfer.ID] = true
			completed, err := processor.processTransfer(ctx, transfer)
			if completed > 0 && onProgress != nil {
				onProgress()
			}
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("process card preparation for transfer ID='%d': %w", transfer.ID, err)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			afterID = transfer.ID
		}
		if len(transfers) < preparationTransferPageSize {
			for id := range processor.cursors {
				if !seen[id] {
					delete(processor.cursors, id)
				}
			}
			return firstErr
		}
	}
}

// ProcessPending реализует интерфейс Processor для обратной совместимости.
func (processor *Processor) ProcessPending(ctx context.Context) (err error) {
	_, err = processor.ProcessPendingWithProgress(ctx)
	return err
}

// processTransfer обрабатывает одну порцию, продолжая следующий обход с сохранённого курсора.
func (processor *Processor) processTransfer(ctx context.Context, transfer transfer_model.Transfer) (int, error) {
	if !preparationMayContinue(transfer.Phase) || transfer.Outcome != transfer_service.OutcomeRunning {
		return 0, nil
	}
	if err := processor.transferSource.BeginPreparation(ctx, transfer.ID); err != nil {
		return 0, err
	}
	pending, err := processor.transferSource.ListPendingPreparationGroups(ctx, transfer.ID,
		preparationGroupBatchSize, processor.cursors[transfer.ID])
	if err != nil {
		return 0, err
	}
	if len(pending) == 0 {
		delete(processor.cursors, transfer.ID)
		return 0, nil
	}
	results := make([]error, len(pending))
	completed := make([]bool, len(pending))
	var group errgroup.Group
	group.SetLimit(preparationConcurrency)
	for index, pendingGroup := range pending {
		group.Go(func() error {
			completed[index], results[index] = processor.preparePendingGroup(ctx, transfer.ID, pendingGroup, processor.preparer)
			return nil
		})
	}
	_ = group.Wait()
	// Курсор продвигается и после временных ошибок: повторяем их на следующем круге.
	processor.cursors[transfer.ID] = pending[len(pending)-1]
	if len(pending) < preparationGroupBatchSize {
		delete(processor.cursors, transfer.ID)
	}
	var firstErr error
	count := 0
	for index, err := range results {
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("prepare group-target ID='%d': %w", pending[index].GroupTargetID, err)
		}
		if completed[index] {
			count++
		}
	}
	if ctx.Err() != nil {
		return count, ctx.Err()
	}
	return count, firstErr
}

func preparationMayContinue(phase transfer_model.Phase) bool {
	switch phase {
	case transfer_service.PhasePreparing,
		transfer_service.PhaseAwaitingAuthorization,
		transfer_service.PhasePublishing,
		transfer_service.PhaseReconciling,
		transfer_service.PhaseMedia:
		return true
	default:
		return false
	}
}

// preparePendingGroup вычисляет результат подготовки группы карточек и атомарно сохраняет его в БД.
// Возвращает true после фиксации любого терминального результата.
func (processor *Processor) preparePendingGroup(
	ctx context.Context,
	transferID transfer_model.TransferID,
	group transfer_preparation.PreparationPendingGroup,
	preparer *cardprepare_preparation.Preparer,
) (bool, error) {
	source, err := processor.transferSource.LoadPreparationSource(
		ctx,
		transferID,
		group.GroupTargetID,
	)
	if err != nil {
		return false, err
	}
	if source.SourceGroupID != group.SourceGroupID ||
		source.TargetID != group.TargetID ||
		source.SellerID != group.SellerID {
		return false, ErrPreparationMismatch
	}

	srcGroup := cardprepare_model.SourceGroup{Items: make([]cardprepare_model.SourceItem, len(source.Items))}
	for index, item := range source.Items {
		srcGroup.Items[index] = cardprepare_model.SourceItem{Position: item.Position, Card: item.Card}

	}

	startedAt := time.Now().UTC()
	result, err := preparer.PrepareTarget(ctx, core_domain.SellerID(group.SellerID), srcGroup)
	finishedAt := time.Now().UTC()
	if err != nil {
		return false, err
	}
	if result.Outcome.Code == OutcomeTargetTemporarilyUnavailable {
		return false, errors.New(
			"WB target is temporarily unavailable for card preparation",
		)
	}
	status := transfer_service.PreparationResultRejected
	var artifact *transfer_preparation.PreparationArtifact
	if result.Outcome.Code == OutcomePrepared {
		if result.Proposal == nil || result.Proposal.Validate() != nil {
			return false, errors.New("prepared card proposal is invalid")
		}
		status = transfer_service.PreparationResultSucceeded
		snapshot, err := json.Marshal(result.Proposal.MetadataSnapshot)
		if err != nil {
			return false, fmt.Errorf("encode metadata snapshot: %w", err)
		}
		artifact = &transfer_preparation.PreparationArtifact{
			SubjectID:        result.Proposal.SubjectID,
			RequestPayload:   result.Proposal.EncodedRequest,
			MetadataSnapshot: snapshot,
			LimitsFree:       result.Proposal.Limits.FreeLimits,
			LimitsPaid:       result.Proposal.Limits.PaidLimits,
			LimitsObservedAt: result.Proposal.LimitsObservedAt,
		}
	} else if result.Outcome.Code == OutcomeCatalogContractError {
		status = transfer_service.PreparationResultUnresolved
	}

	transferCmd := transfer_preparation.ApplyPreparationResultCommand{
		TransferID:    transferID,
		GroupTargetID: group.GroupTargetID,
		Status:        status,
		OutcomeCode:   string(result.Outcome.Code),
		ItemPosition:  result.Outcome.ItemPosition,
		Field:         result.Outcome.Field,
		StartedAt:     startedAt,
		FinishedAt:    finishedAt,
		Artifact:      artifact,
		Revision:      1,
	}
	if err := transferCmd.Validate(); err != nil {
		return false, err
	}

	if err := processor.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			return processor.resultApplier.ApplyTx(ctx, tx, transferCmd)
		},
	); err != nil {
		return false, fmt.Errorf("apply card preparation result: %w", err)
	}

	return true, nil
}

var _ interface {
	ProcessPending(context.Context) error
	ProcessPendingWithProgress(context.Context) (bool, error)
} = (*Processor)(nil)
