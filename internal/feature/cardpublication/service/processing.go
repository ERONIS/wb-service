package cardpublication_service

import (
	"context"
	"errors"
	"fmt"
	"sync"

	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

const publicationTransferPageSize = 100

type Processor struct {
	repository     Repository
	transferSource TransferSource
	proposalReader ProposalReader
	catalogReader  *CatalogReader
	planner        *Planner
	saver          *Saver
	processMu      sync.Mutex
}

func NewProcessor(
	repository Repository,
	transferSource TransferSource,
	proposalReader ProposalReader,
	catalogReader *CatalogReader,
	planner *Planner,
	saver *Saver,
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
	exists, err := processor.repository.HasPlan(ctx, transfer.ID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	source, err := processor.transferSource.LoadPublicationPlanningSource(
		ctx,
		transfer.ID,
	)
	if err != nil {
		return err
	}
	loaded := make([]loadedPlanningGroup, 0, len(source.Groups))
	for _, group := range source.Groups {
		proposal, err := processor.proposalReader.LoadProposal(
			ctx,
			cardprepare_service.ProposalQuery{
				TransferID:         transfer.ID,
				GroupTargetID:      group.GroupTargetID,
				PreparationGroupID: cardprepare_service.PreparationGroupID(group.PreparationGroupID),
				ProposalRoot:       cardprepare_service.Digest(group.ProposalRoot),
			},
		)
		if err != nil {
			return fmt.Errorf("load prepared publication proposal: %w", err)
		}
		if proposal.TransferID != transfer.ID {
			return errors.New("prepared proposal transfer identity differs")
		}
		loaded = append(loaded, loadedPlanningGroup{Source: group, Proposal: proposal})
	}

	observations := make(map[int64]CatalogObservation)
	for _, group := range source.Groups {
		if _, exists := observations[group.TargetID]; exists {
			continue
		}
		observation, err := processor.catalogReader.Read(
			ctx,
			group.TargetID,
			CabinetID(group.CabinetID),
		)
		if err != nil {
			return err
		}
		observations[group.TargetID] = observation
	}
	draft, err := processor.planner.Build(transfer, loaded, observations)
	if err != nil {
		return err
	}
	return processor.saver.Save(ctx, draft)
}
