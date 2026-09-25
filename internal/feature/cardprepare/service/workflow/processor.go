package workflow

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	core_observability "github.com/ERONIS/wb-service/internal/core/observability"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	preparationTransferPageSize = 100
	preparationConcurrency      = 8
)

type WorkStatus string

const (
	WorkStatusPending    WorkStatus = "pending"
	WorkStatusPrepared   WorkStatus = "prepared"
	WorkStatusRejected   WorkStatus = "rejected"
	WorkStatusUnresolved WorkStatus = "unresolved"
)

func (status WorkStatus) IsTerminal() bool {
	return status == WorkStatusPrepared || status == WorkStatusRejected ||
		status == WorkStatusUnresolved
}

type PreparationWork struct {
	ID            PreparationGroupID
	PreparationID PreparationID
	TransferID    transfer_service.TransferID
	GroupTargetID int64
	SourceGroupID int64
	TargetID      int64
	CabinetID     CabinetID
	Status        WorkStatus
	Revision      int64
	OutcomeCode   OutcomeCode
	OutcomeItem   int
	OutcomeField  string
	ProposalRoot  Digest
}

func (work PreparationWork) Validate() error {
	if work.ID <= 0 || work.PreparationID <= 0 || work.TransferID <= 0 ||
		work.GroupTargetID <= 0 || work.SourceGroupID <= 0 || work.TargetID <= 0 ||
		strings.TrimSpace(string(work.CabinetID)) != string(work.CabinetID) ||
		work.CabinetID == "" || len(work.CabinetID) > 128 ||
		work.Revision < 0 || work.OutcomeItem < 0 || len(work.OutcomeField) > 128 {
		return errors.New("card preparation work is invalid")
	}
	switch work.Status {
	case WorkStatusPending:
		if work.Revision != 0 || work.OutcomeCode != "" ||
			work.OutcomeItem != 0 || work.OutcomeField != "" ||
			work.ProposalRoot != (Digest{}) {
			return errors.New("pending card preparation work has a result")
		}
	case WorkStatusPrepared:
		if work.Revision != 1 || work.OutcomeCode != OutcomePrepared ||
			work.OutcomeItem != 0 || work.OutcomeField != "" ||
			work.ProposalRoot == (Digest{}) {
			return errors.New("prepared card preparation work is invalid")
		}
	case WorkStatusRejected, WorkStatusUnresolved:
		if work.Revision != 1 || !work.OutcomeCode.IsValid() ||
			work.OutcomeCode == OutcomePrepared ||
			work.OutcomeCode == OutcomeTargetTemporarilyUnavailable ||
			work.ProposalRoot != (Digest{}) {
			return errors.New("unsuccessful card preparation work is invalid")
		}
	default:
		return errors.New("card preparation work status is invalid")
	}
	return nil
}

type SavePreparationResultCommand struct {
	Work    PreparationWork
	Result  Result
	Members []PreparedMember
}

func (command SavePreparationResultCommand) Validate() error {
	if err := command.Work.Validate(); err != nil {
		return err
	}
	if command.Work.Status != WorkStatusPending || command.Work.Revision != 0 {
		return errors.New("card preparation result work is not pending")
	}
	if !command.Result.Outcome.Code.IsValid() ||
		command.Result.Outcome.Code == OutcomeTargetTemporarilyUnavailable ||
		command.Result.Outcome.ItemPosition < 0 ||
		strings.TrimSpace(command.Result.Outcome.Field) != command.Result.Outcome.Field ||
		len(command.Result.Outcome.Field) > 128 {
		return errors.New("card preparation result outcome is invalid")
	}

	if command.Result.Outcome.Code == OutcomePrepared {
		if command.Result.Proposal == nil ||
			command.Result.Outcome.ItemPosition != 0 ||
			command.Result.Outcome.Field != "" ||
			command.Result.Proposal.ValidateIntegrity() != nil ||
			len(command.Members) != len(command.Result.Proposal.Request[0].Variants) {
			return errors.New("prepared card result is invalid")
		}
		seenItemIDs := make(map[int64]struct{}, len(command.Members))
		for index, member := range command.Members {
			if member.TransferItemID <= 0 || member.ItemPosition <= 0 ||
				strings.TrimSpace(member.VendorCode) != member.VendorCode ||
				member.VendorCode == "" ||
				(index > 0 && member.ItemPosition <= command.Members[index-1].ItemPosition) ||
				member.VendorCode !=
					command.Result.Proposal.Request[0].Variants[index].VendorCode {
				return fmt.Errorf("prepared member at index %d is invalid", index)
			}
			if _, exists := seenItemIDs[member.TransferItemID]; exists {
				return fmt.Errorf("prepared member at index %d is duplicated", index)
			}
			seenItemIDs[member.TransferItemID] = struct{}{}
		}
		return nil
	}

	if command.Result.Proposal != nil || len(command.Members) != 0 {
		return errors.New("unsuccessful card result contains proposal artifacts")
	}
	return nil
}

func (work PreparationWork) TransferResult() (
	transfer_service.ApplyPreparationResultCommand,
	error,
) {
	if err := work.Validate(); err != nil || !work.Status.IsTerminal() {
		return transfer_service.ApplyPreparationResultCommand{}, errors.New(
			"card preparation work has no terminal transfer result",
		)
	}
	status := transfer_service.PreparationResultRejected
	if work.Status == WorkStatusPrepared {
		status = transfer_service.PreparationResultSucceeded
	} else if work.Status == WorkStatusUnresolved {
		status = transfer_service.PreparationResultUnresolved
	}
	return transfer_service.ApplyPreparationResultCommand{
		TransferID:         work.TransferID,
		GroupTargetID:      work.GroupTargetID,
		PreparationID:      int64(work.PreparationID),
		PreparationGroupID: int64(work.ID),
		Status:             status,
		OutcomeCode:        string(work.OutcomeCode),
		ProposalRoot:       transfer_service.Digest(work.ProposalRoot),
		Revision:           work.Revision,
	}, nil
}

type Processor struct {
	repository     PreparationRepository
	coordinator    *Coordinator
	preparer       *Preparer
	transferSource TransferSource
	resultApplier  TransferResultApplier
	uow            core_postgres_transaction.UnitOfWork
	logger         *zap.Logger
}

func NewProcessor(
	repository PreparationRepository,
	coordinator *Coordinator,
	preparer *Preparer,
	transferSource TransferSource,
	resultApplier TransferResultApplier,
	uow core_postgres_transaction.UnitOfWork,
	loggers ...*zap.Logger,
) *Processor {
	if repository == nil || coordinator == nil || preparer == nil ||
		transferSource == nil || resultApplier == nil || uow == nil {
		panic("cardprepare processor dependency is nil")
	}
	return &Processor{
		repository:     repository,
		coordinator:    coordinator,
		preparer:       preparer,
		transferSource: transferSource,
		resultApplier:  resultApplier,
		uow:            uow,
		logger:         core_observability.Logger(loggers...),
	}
}

func (processor *Processor) ProcessPending(ctx context.Context) (err error) {
	startedAt := time.Now()
	pagesCount, transfersCount := 0, 0
	defer func() {
		logTiming := core_observability.LogTimingDebug
		if transfersCount > 0 {
			logTiming = core_observability.LogTiming
		}
		logTiming(
			processor.logger,
			"cardprepare",
			"preparation_poll",
			startedAt,
			err,
			zap.Int("pages_count", pagesCount),
			zap.Int("transfers_count", transfersCount),
		)
	}()
	if ctx == nil {
		return errors.New("process pending card preparations: context is nil")
	}
	var afterID transfer_service.TransferID
	var firstErr error
	for {
		transfers, err := processor.transferSource.ListPreparing(
			ctx,
			afterID,
			preparationTransferPageSize,
		)
		if err != nil {
			return fmt.Errorf("list card preparation transfers: %w", err)
		}
		pagesCount++
		transfersCount += len(transfers)
		for _, transfer := range transfers {
			if err := processor.processTransfer(ctx, transfer); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if firstErr == nil {
					firstErr = fmt.Errorf(
						"process card preparation for transfer ID='%d': %w",
						transfer.ID,
						err,
					)
				}
			}
			afterID = transfer.ID
		}
		if len(transfers) < preparationTransferPageSize {
			return firstErr
		}
	}
}

func (processor *Processor) processTransfer(
	ctx context.Context,
	transfer transfer_service.Transfer,
) (err error) {
	startedAt := time.Now()
	var (
		startDuration        time.Duration
		beginDuration        time.Duration
		listWorksDuration    time.Duration
		prepareWorksDuration time.Duration
		applyResultsDuration time.Duration
		worksWallDuration    time.Duration
		worksCount           int
		pendingWorksCount    int
		skipped              bool
	)
	defer func() {
		core_observability.LogTiming(
			processor.logger,
			"cardprepare",
			"prepare_transfer",
			startedAt,
			err,
			zap.Int64("transfer_id", int64(transfer.ID)),
			zap.Int64("batch_id", int64(transfer.BatchID)),
			zap.Int("items_count", transfer.ItemsCount),
			zap.Int("groups_count", transfer.GroupsCount),
			zap.Int("targets_count", transfer.TargetsCount),
			zap.Int("works_count", worksCount),
			zap.Int("pending_works_count", pendingWorksCount),
			zap.Bool("skipped", skipped),
			zap.Duration("start_preparation_duration", startDuration),
			zap.Duration("begin_transfer_preparation_duration", beginDuration),
			zap.Duration("list_works_duration", listWorksDuration),
			zap.Duration("works_wall_duration", worksWallDuration),
			zap.Duration("prepare_works_duration", prepareWorksDuration),
			zap.Duration("apply_results_duration", applyResultsDuration),
			zap.Int("preparation_concurrency", preparationConcurrency),
		)
	}()
	if !preparationMayContinue(transfer.Phase) ||
		transfer.Outcome != transfer_service.OutcomeRunning {
		skipped = true
		return nil
	}
	ctx = core_observability.WithCorrelation(ctx, core_observability.Correlation{
		TransferID: int64(transfer.ID), BatchID: int64(transfer.BatchID),
	})
	core_observability.LogStarted(processor.logger, "cardprepare", "prepare_transfer",
		zap.Int64("transfer_id", int64(transfer.ID)), zap.Int64("batch_id", int64(transfer.BatchID)))
	stepStartedAt := time.Now()
	if err := processor.coordinator.Start(ctx, StartPreparationCommand{
		TransferID:           transfer.ID,
		BatchID:              transfer.BatchID,
		BatchSchemaVersion:   transfer.BatchSchemaVersion,
		NormalizationVersion: transfer.BatchNormalizationVersion,
		ItemsCount:           transfer.ItemsCount,
		GroupsCount:          transfer.GroupsCount,
		TargetsCount:         transfer.TargetsCount,
		BatchChecksum:        transfer.BatchChecksum,
	}); err != nil {
		startDuration = time.Since(stepStartedAt)
		return err
	}
	startDuration = time.Since(stepStartedAt)
	stepStartedAt = time.Now()
	if err := processor.transferSource.BeginPreparation(ctx, transfer.ID); err != nil {
		beginDuration = time.Since(stepStartedAt)
		return err
	}
	beginDuration = time.Since(stepStartedAt)
	stepStartedAt = time.Now()
	works, err := processor.repository.ListPreparationWork(ctx, transfer.ID)
	listWorksDuration = time.Since(stepStartedAt)
	if err != nil {
		return err
	}
	worksCount = len(works)
	if int64(len(works)) != transfer.GroupTargetsCount {
		return ErrPreparationMismatch
	}
	for _, work := range works {
		if work.Status == WorkStatusPending {
			pendingWorksCount++
		}
	}
	works = fairPreparationOrder(works)
	worksStartedAt := time.Now()
	batchPreparer := processor.preparer.ForBatch()
	type workResult struct {
		prepareDuration time.Duration
		applyDuration   time.Duration
		err             error
	}
	results := make([]workResult, len(works))
	var group errgroup.Group
	group.SetLimit(preparationConcurrency)
	for index, work := range works {
		index, work := index, work
		group.Go(func() error {
			results[index] = processor.processPreparationWork(ctx, work, batchPreparer)
			return nil
		})
	}
	_ = group.Wait()
	worksWallDuration = time.Since(worksStartedAt)
	var firstErr error
	for _, result := range results {
		prepareWorksDuration += result.prepareDuration
		applyResultsDuration += result.applyDuration
		if firstErr == nil && result.err != nil {
			firstErr = result.err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return firstErr
}

func preparationMayContinue(phase transfer_service.Phase) bool {
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

func fairPreparationOrder(works []PreparationWork) []PreparationWork {
	byCabinet := make(map[CabinetID][]PreparationWork)
	cabinets := make([]CabinetID, 0)
	for _, work := range works {
		if _, exists := byCabinet[work.CabinetID]; !exists {
			cabinets = append(cabinets, work.CabinetID)
		}
		byCabinet[work.CabinetID] = append(byCabinet[work.CabinetID], work)
	}
	sort.Slice(cabinets, func(left, right int) bool { return cabinets[left] < cabinets[right] })
	result := make([]PreparationWork, 0, len(works))
	for position := 0; len(result) < len(works); position++ {
		for _, cabinetID := range cabinets {
			cabinetWorks := byCabinet[cabinetID]
			if position < len(cabinetWorks) {
				result = append(result, cabinetWorks[position])
			}
		}
	}
	return result
}

func (processor *Processor) processPreparationWork(
	ctx context.Context,
	work PreparationWork,
	preparer *Preparer,
) (result struct {
	prepareDuration time.Duration
	applyDuration   time.Duration
	err             error
}) {
	if work.Status == WorkStatusPending {
		startedAt := time.Now()
		stored, err := processor.prepareWork(ctx, work, preparer)
		result.prepareDuration = time.Since(startedAt)
		if err != nil {
			result.err = fmt.Errorf(
				"prepare group-target ID='%d': %w",
				work.GroupTargetID,
				err,
			)
			return result
		}
		work = stored
	}
	command, err := work.TransferResult()
	if err != nil {
		result.err = err
		return result
	}
	startedAt := time.Now()
	err = processor.resultApplier.Apply(ctx, command)
	result.applyDuration = time.Since(startedAt)
	if err != nil {
		result.err = fmt.Errorf(
			"apply group-target ID='%d' result: %w",
			work.GroupTargetID,
			err,
		)
	}
	return result
}

func (processor *Processor) prepareWork(
	ctx context.Context,
	work PreparationWork,
	preparer *Preparer,
) (stored PreparationWork, err error) {
	startedAt := time.Now()
	ctx = core_observability.WithCorrelation(ctx, core_observability.Correlation{
		TransferID: int64(work.TransferID), GroupTargetID: work.GroupTargetID,
	})
	core_observability.LogStarted(processor.logger, "cardprepare", "prepare_group_target",
		zap.Int64("transfer_id", int64(work.TransferID)), zap.Int64("group_target_id", work.GroupTargetID),
		zap.String("cabinet_id", string(work.CabinetID)))
	var loadSourceDuration, prepareTargetDuration, saveResultDuration time.Duration
	itemsCount := 0
	defer func() {
		core_observability.LogTiming(
			processor.logger,
			"cardprepare",
			"prepare_group_target",
			startedAt,
			err,
			zap.Int64("transfer_id", int64(work.TransferID)),
			zap.Int64("group_target_id", work.GroupTargetID),
			zap.Int64("target_id", work.TargetID),
			zap.String("cabinet_id", string(work.CabinetID)),
			zap.Int("items_count", itemsCount),
			zap.String("outcome_code", string(stored.OutcomeCode)),
			zap.Int("outcome_item_position", stored.OutcomeItem),
			zap.String("outcome_field", stored.OutcomeField),
			zap.Duration("load_source_duration", loadSourceDuration),
			zap.Duration("prepare_target_duration", prepareTargetDuration),
			zap.Duration("save_result_duration", saveResultDuration),
		)
	}()
	stepStartedAt := time.Now()
	source, err := processor.transferSource.LoadPreparationSource(
		ctx,
		work.TransferID,
		work.GroupTargetID,
	)
	loadSourceDuration = time.Since(stepStartedAt)
	if err != nil {
		return PreparationWork{}, err
	}
	if source.SourceGroupID != work.SourceGroupID ||
		source.TargetID != work.TargetID ||
		CabinetID(source.CabinetID) != work.CabinetID {
		return PreparationWork{}, ErrPreparationMismatch
	}
	itemsCount = len(source.Items)

	group := SourceGroup{Items: make([]SourceItem, len(source.Items))}
	members := make([]PreparedMember, len(source.Items))
	for index, item := range source.Items {
		group.Items[index] = SourceItem{Position: item.Position, Card: item.Card}
		members[index] = PreparedMember{
			TransferItemID: item.TransferItemID,
			ItemPosition:   item.Position,
			VendorCode:     item.VendorCode,
		}
	}
	stepStartedAt = time.Now()
	result, err := preparer.PrepareTarget(ctx, work.CabinetID, group)
	prepareTargetDuration = time.Since(stepStartedAt)
	if err != nil {
		return PreparationWork{}, err
	}
	if result.Outcome.Code == OutcomeTargetTemporarilyUnavailable {
		return PreparationWork{}, errors.New(
			"WB target is temporarily unavailable for card preparation",
		)
	}
	if result.Outcome.Code != OutcomePrepared {
		members = nil
	}

	command := SavePreparationResultCommand{
		Work:    work,
		Result:  result,
		Members: members,
	}
	if err := command.Validate(); err != nil {
		return PreparationWork{}, err
	}
	stepStartedAt = time.Now()
	if err := processor.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			var err error
			stored, err = processor.repository.SavePreparationResult(ctx, tx, command)
			return err
		},
	); err != nil {
		saveResultDuration = time.Since(stepStartedAt)
		return PreparationWork{}, fmt.Errorf("save card preparation result: %w", err)
	}
	saveResultDuration = time.Since(stepStartedAt)
	return stored, nil
}

var _ interface {
	ProcessPending(context.Context) error
} = (*Processor)(nil)
