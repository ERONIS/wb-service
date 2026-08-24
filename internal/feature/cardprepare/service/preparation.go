package cardprepare_service

import (
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

var ErrPreparationMismatch = fmt.Errorf(
	"card preparation identity mismatch: %w",
	core_errors.ErrConflict,
)

var ErrProposalNotFound = fmt.Errorf(
	"card preparation proposal: %w",
	core_errors.ErrNotFound,
)

type PreparationID int64
type PreparationGroupID int64

type StartPreparationCommand struct {
	TransferID           transfer_service.TransferID
	BatchID              cardimport_service.BatchID
	BatchSchemaVersion   int
	NormalizationVersion int
	ItemsCount           int
	GroupsCount          int
	TargetsCount         int
	BatchChecksum        cardimport_service.Digest
}

func (command StartPreparationCommand) Validate() error {
	if command.TransferID <= 0 || command.BatchID <= 0 ||
		command.BatchSchemaVersion <= 0 || command.NormalizationVersion <= 0 ||
		command.ItemsCount <= 0 || command.GroupsCount <= 0 ||
		command.GroupsCount > command.ItemsCount || command.TargetsCount <= 0 ||
		command.BatchChecksum == (cardimport_service.Digest{}) {
		return errors.New("start card preparation command is invalid")
	}
	return nil
}

type ProposalQuery struct {
	TransferID         transfer_service.TransferID
	GroupTargetID      int64
	PreparationGroupID PreparationGroupID
	ProposalRoot       Digest
}

func (query ProposalQuery) Validate() error {
	if query.TransferID <= 0 || query.GroupTargetID <= 0 ||
		query.PreparationGroupID <= 0 || query.ProposalRoot == (Digest{}) {
		return errors.New("card preparation proposal query is invalid")
	}
	return nil
}

type PreparedMember struct {
	TransferItemID int64
	ItemPosition   int
	VendorCode     string
}

type StoredProposal struct {
	PreparationID      PreparationID
	PreparationGroupID PreparationGroupID
	TransferID         transfer_service.TransferID
	GroupTargetID      int64
	SourceGroupID      int64
	TargetID           int64
	CabinetID          CabinetID
	Proposal           Proposal
	Members            []PreparedMember
}
