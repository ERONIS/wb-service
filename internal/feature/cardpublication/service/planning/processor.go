package planning

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	core_observability "github.com/ERONIS/wb-service/internal/core/observability"
	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	publicationTransferPageSize = 100
)

type Processor struct {
	repository     Repository
	transferSource TransferSource
	proposalReader ProposalReader
	catalogReader  *CatalogReader
	planner        *Planner
	saver          *Saver
	logger         *zap.Logger
	processMu      sync.Mutex
}

func NewProcessor(
	repository Repository,
	transferSource TransferSource,
	proposalReader ProposalReader,
	catalogReader *CatalogReader,
	planner *Planner,
	saver *Saver,
	loggers ...*zap.Logger,
) *Processor {
	if repository == nil || transferSource == nil || proposalReader == nil ||
		catalogReader == nil || planner == nil || saver == nil {
		panic("cardpublication processor dependency is nil")
	}
	return &Processor{
		repository:     repository,
		transferSource: transferSource,
		proposalReader: proposalReader,
		catalogReader:  catalogReader,
		planner:        planner,
		saver:          saver,
		logger:         core_observability.Logger(loggers...),
	}
}

func (processor *Processor) ProcessPending(ctx context.Context) (err error) {
	startedAt := time.Now()
	pagesCount, transfersCount := 0, 0
	var lockWait time.Duration
	defer func() {
		logTiming := core_observability.LogTimingDebug
		if transfersCount > 0 {
			logTiming = core_observability.LogTiming
		}
		logTiming(
			processor.logger,
			"cardpublication",
			"planning_poll",
			startedAt,
			err,
			zap.Duration("lock_wait_duration", lockWait),
			zap.Int("pages_count", pagesCount),
			zap.Int("transfers_count", transfersCount),
		)
	}()
	if ctx == nil {
		return errors.New("process pending publication plans: context is nil")
	}
	lockStartedAt := time.Now()
	processor.processMu.Lock()
	lockWait = time.Since(lockStartedAt)
	defer processor.processMu.Unlock()

	var afterID transfer_service.TransferID
	var firstErr error
	for {
		transfers, err := processor.transferSource.ListAwaitingAuthorization(
			ctx,
			afterID,
			publicationTransferPageSize,
		)
		if err != nil {
			return fmt.Errorf("list publication planning transfers: %w", err)
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
						"plan publication for transfer ID='%d': %w",
						transfer.ID,
						err,
					)
				}
			}
			afterID = transfer.ID
		}
		if len(transfers) < publicationTransferPageSize {
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
		loadSourceDuration    time.Duration
		loadProposalsDuration time.Duration
		readCatalogDuration   time.Duration
		buildPlanDuration     time.Duration
		savePlanDuration      time.Duration
		groupsCount           int
		targetsCount          int
		vendorCodesCount      int
		actionsCount          int
		itemsCount            int
	)
	defer func() {
		core_observability.LogTiming(
			processor.logger,
			"cardpublication",
			"plan_transfer",
			startedAt,
			err,
			zap.Int64("transfer_id", int64(transfer.ID)),
			zap.Int64("batch_id", int64(transfer.BatchID)),
			zap.Int("groups_count", groupsCount),
			zap.Int("targets_count", targetsCount),
			zap.Int("vendor_codes_count", vendorCodesCount),
			zap.Int("items_count", itemsCount),
			zap.Int("actions_count", actionsCount),
			zap.Duration("load_source_duration", loadSourceDuration),
			zap.Duration("load_proposals_duration", loadProposalsDuration),
			zap.Duration("read_catalog_duration", readCatalogDuration),
			zap.Duration("build_plan_duration", buildPlanDuration),
			zap.Duration("save_plan_duration", savePlanDuration),
		)
	}()
	ctx = core_observability.WithCorrelation(ctx, core_observability.Correlation{
		TransferID: int64(transfer.ID), BatchID: int64(transfer.BatchID),
	})
	core_observability.LogStarted(processor.logger, "cardpublication", "plan_transfer",
		zap.Int64("transfer_id", int64(transfer.ID)), zap.Int64("batch_id", int64(transfer.BatchID)))
	stepStartedAt := time.Now()
	source, err := processor.transferSource.LoadPublicationPlanningSource(
		ctx,
		transfer.ID,
	)
	loadSourceDuration = time.Since(stepStartedAt)
	if err != nil {
		return err
	}
	groupsCount = len(source.Groups)
	// Another planner instance may have claimed the last ready wave while this
	// processor was loading the transfer. An empty source is a harmless retry.
	if groupsCount == 0 {
		return nil
	}
	queries := make([]cardprepare_service.ProposalQuery, len(source.Groups))
	for index, sourceGroup := range source.Groups {
		queries[index] = cardprepare_service.ProposalQuery{
			TransferID:         transfer.ID,
			GroupTargetID:      sourceGroup.GroupTargetID,
			PreparationGroupID: cardprepare_service.PreparationGroupID(sourceGroup.PreparationGroupID),
			ProposalRoot:       cardprepare_service.Digest(sourceGroup.ProposalRoot),
		}
	}
	stepStartedAt = time.Now()
	proposals, err := processor.proposalReader.LoadProposals(ctx, queries)
	if err != nil {
		loadProposalsDuration = time.Since(stepStartedAt)
		return fmt.Errorf("load prepared publication proposals: %w", err)
	}
	loadProposalsDuration = time.Since(stepStartedAt)
	if len(proposals) != len(source.Groups) {
		return errors.New("prepared publication proposal count differs")
	}
	loaded := make([]loadedPlanningGroup, len(source.Groups))
	for index, proposal := range proposals {
		if proposal.TransferID != transfer.ID {
			return errors.New("prepared proposal transfer identity differs")
		}
		loaded[index] = loadedPlanningGroup{
			Source: source.Groups[index], Proposal: proposal,
		}
	}

	vendorCodesByTarget := make(map[int64][]string)
	cabinetByTarget := make(map[int64]CabinetID)
	for _, group := range source.Groups {
		cabinetID := CabinetID(group.CabinetID)
		if existing, ok := cabinetByTarget[group.TargetID]; ok && existing != cabinetID {
			return errors.New("publication target has multiple cabinets")
		}
		cabinetByTarget[group.TargetID] = cabinetID
		for _, member := range group.Members {
			vendorCodesCount++
			vendorCodesByTarget[group.TargetID] = append(
				vendorCodesByTarget[group.TargetID],
				member.VendorCode,
			)
		}
	}
	targetsCount = len(vendorCodesByTarget)

	observations := make(map[int64]CatalogObservation, len(vendorCodesByTarget))
	stepStartedAt = time.Now()
	targetIDs := make([]int64, 0, len(vendorCodesByTarget))
	for targetID := range vendorCodesByTarget {
		targetIDs = append(targetIDs, targetID)
	}
	sort.Slice(targetIDs, func(left, right int) bool { return targetIDs[left] < targetIDs[right] })
	loadedObservations := make([]CatalogObservation, len(targetIDs))
	group, readCtx := errgroup.WithContext(ctx)
	for index, targetID := range targetIDs {
		index, targetID := index, targetID
		group.Go(func() error {
			observation, err := processor.catalogReader.ReadVendorCodes(
				readCtx,
				targetID,
				cabinetByTarget[targetID],
				vendorCodesByTarget[targetID],
			)
			if err == nil {
				loadedObservations[index] = observation
			}
			return err
		})
	}
	if err := group.Wait(); err != nil {
		readCatalogDuration = time.Since(stepStartedAt)
		return err
	}
	for index, targetID := range targetIDs {
		observations[targetID] = loadedObservations[index]
	}
	readCatalogDuration = time.Since(stepStartedAt)
	stepStartedAt = time.Now()
	draft, err := processor.planner.Build(transfer, loaded, observations)
	buildPlanDuration = time.Since(stepStartedAt)
	if err != nil {
		return err
	}
	actionsCount = len(draft.Actions)
	itemsCount = len(draft.Items)
	stepStartedAt = time.Now()
	err = processor.saver.Save(ctx, draft)
	savePlanDuration = time.Since(stepStartedAt)
	return err
}
