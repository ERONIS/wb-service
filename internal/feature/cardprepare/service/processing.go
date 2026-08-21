package cardprepare_service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

const preparationTransferPageSize = 100

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
}

func NewProcessor(
	repository PreparationRepository,
	coordinator *Coordinator,
	preparer *Preparer,
	transferSource TransferSource,
	resultApplier TransferResultApplier,
	uow core_postgres_transaction.UnitOfWork,
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
	}
}

func (processor *Processor) ProcessPending(ctx context.Context) error {
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
) error {
	if transfer.Phase != transfer_service.PhasePreparing ||
		transfer.Outcome != transfer_service.OutcomeRunning {
		return nil
	}
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
		return err
	}
	if err := processor.transferSource.BeginPreparation(ctx, transfer.ID); err != nil {
		return err
	}
	works, err := processor.repository.ListPreparationWork(ctx, transfer.ID)
	if err != nil {
		return err
	}
	if int64(len(works)) != transfer.GroupTargetsCount {
		return ErrPreparationMismatch
	}
	var firstErr error
	for _, work := range works {
		if work.Status == WorkStatusPending {
			stored, prepareErr := processor.prepareWork(ctx, work)
			if prepareErr != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if firstErr == nil {
					firstErr = fmt.Errorf(
						"prepare group-target ID='%d': %w",
						work.GroupTargetID,
						prepareErr,
					)
				}
				continue
			}
			work = stored
		}
		command, err := work.TransferResult()
		if err != nil {
			return err
		}
		if err := processor.resultApplier.Apply(ctx, command); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf(
					"apply group-target ID='%d' result: %w",
					work.GroupTargetID,
					err,
				)
			}
		}
	}
	return firstErr
}

func (processor *Processor) prepareWork(
	ctx context.Context,
	work PreparationWork,
) (PreparationWork, error) {
	source, err := processor.transferSource.LoadPreparationSource(
		ctx,
		work.TransferID,
		work.GroupTargetID,
	)
	if err != nil {
		return PreparationWork{}, err
	}
	if source.SourceGroupID != work.SourceGroupID ||
		source.TargetID != work.TargetID ||
		CabinetID(source.CabinetID) != work.CabinetID {
		return PreparationWork{}, ErrPreparationMismatch
	}

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
	result, err := processor.preparer.PrepareTarget(ctx, work.CabinetID, group)
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
	var stored PreparationWork
	if err := processor.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			var err error
			stored, err = processor.repository.SavePreparationResult(ctx, tx, command)
			return err
		},
	); err != nil {
		return PreparationWork{}, fmt.Errorf("save card preparation result: %w", err)
	}
	return stored, nil
}

var _ interface {
	ProcessPending(context.Context) error
} = (*Processor)(nil)
