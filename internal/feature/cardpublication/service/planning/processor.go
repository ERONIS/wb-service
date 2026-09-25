package planning

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
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
		logger:         core_logger.Fallback(loggers...),
	}
}

func (processor *Processor) ProcessPending(ctx context.Context) error {
	if ctx == nil {
		return errors.New("process pending publication plans: context is nil")
	}
	processor.processMu.Lock()
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
) error {
	source, err := processor.transferSource.LoadPublicationPlanningSource(
		ctx,
		transfer.ID,
	)
	if err != nil {
		return err
	}
	if len(source.Groups) == 0 {
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
	proposals, err := processor.proposalReader.LoadProposals(ctx, queries)
	if err != nil {
		return fmt.Errorf("load prepared publication proposals: %w", err)
	}
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
			vendorCodesByTarget[group.TargetID] = append(
				vendorCodesByTarget[group.TargetID],
				member.VendorCode,
			)
		}
	}

	observations := make(map[int64]CatalogObservation, len(vendorCodesByTarget))
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
		return err
	}
	for index, targetID := range targetIDs {
		observations[targetID] = loadedObservations[index]
	}
	draft, err := processor.planner.Build(transfer, loaded, observations)
	if err != nil {
		return err
	}
	return processor.saver.Save(ctx, draft)
}
