package resolution

import (
	"context"
	"fmt"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type VerifiedCardCommand struct {
	TransferID             transfer_service.TransferID
	ActionID               int64
	ActionMemberID         int64
	ExpectedActionRevision int64
	TransferItemTargetID   int64
	AttemptID              int64
	CabinetID              string
	VendorCode             string
}

type VerifiedCardEvidence struct {
	Card       contentapi.Card
	ObservedAt time.Time
}

type VerifiedCardRepository interface {
	LockResolutionSubject(context.Context, core_postgres_transaction.DBTX, ResolutionSubjectKey) (ManualResolutionSubject, error)
	InsertActionObservation(context.Context, core_postgres_transaction.DBTX, transfer_service.TransferID, string, model.ObservationDraft) (int64, error)
	LoadManualObservationEvidence(context.Context, core_postgres_transaction.DBTX, ManualResolutionSubject, int64) (ManualObservationEvidence, error)
	ApplyVerifiedCardResult(context.Context, core_postgres_transaction.DBTX, ManualResolutionSubject, ManualResolutionDecision) error
}

type VerifiedCardResolver struct {
	repository VerifiedCardRepository
	transfer   ManualTransferResultCorrector
	uow        core_postgres_transaction.UnitOfWork
}

func NewVerifiedCardResolver(
	repository VerifiedCardRepository,
	transfer ManualTransferResultCorrector,
	uow core_postgres_transaction.UnitOfWork,
) *VerifiedCardResolver {
	if repository == nil || transfer == nil || uow == nil {
		panic("verified card resolver dependency is nil")
	}
	return &VerifiedCardResolver{repository: repository, transfer: transfer, uow: uow}
}

func (resolver *VerifiedCardResolver) Resolve(
	ctx context.Context,
	command VerifiedCardCommand,
	evidence VerifiedCardEvidence,
) error {
	if ctx == nil || command.TransferID <= 0 || command.ActionID <= 0 ||
		command.ActionMemberID <= 0 || command.ExpectedActionRevision < 0 ||
		command.TransferItemTargetID <= 0 || command.AttemptID <= 0 ||
		command.CabinetID == "" || command.VendorCode == "" ||
		evidence.Card.VendorCode != command.VendorCode ||
		evidence.Card.NMID <= 0 || evidence.Card.IMTID <= 0 ||
		evidence.Card.SubjectID <= 0 || evidence.ObservedAt.IsZero() {
		return ErrManualEvidenceInvalid
	}

	return resolver.uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		subject, err := resolver.repository.LockResolutionSubject(ctx, tx, ResolutionSubjectKey{
			TransferID:             command.TransferID,
			ActionID:               command.ActionID,
			ActionMemberID:         command.ActionMemberID,
			ExpectedActionRevision: command.ExpectedActionRevision,
			AttemptID:              command.AttemptID,
		})
		if err != nil {
			return err
		}
		if subject.TransferItemTargetID != command.TransferItemTargetID ||
			subject.AttemptID != command.AttemptID ||
			string(subject.CabinetID) != command.CabinetID ||
			subject.VendorCode != command.VendorCode ||
			evidence.ObservedAt.Before(subject.AttemptStartedAt) ||
			(subject.IdentityNMID > 0 && subject.IdentityNMID != evidence.Card.NMID) {
			return ErrManualResolutionConflict
		}

		observation, err := catalog.DraftObservation(catalog.CatalogObservation{
			TargetID:   subject.TargetID,
			CabinetID:  subject.CabinetID,
			Normal:     []contentapi.Card{evidence.Card},
			ObservedAt: evidence.ObservedAt,
		})
		if err != nil {
			return err
		}
		observation.Digest = digestParts(
			"cardpublication-verified-observation:v1",
			observation.Payload,
			[]byte(evidence.ObservedAt.UTC().Format(time.RFC3339Nano)),
		)
		observationID, err := resolver.repository.InsertActionObservation(
			ctx, tx, subject.TransferID, "post_submission", observation,
		)
		if err != nil {
			return err
		}
		storedEvidence, err := resolver.repository.LoadManualObservationEvidence(ctx, tx, subject, observationID)
		if err != nil {
			return err
		}
		decision, err := decideManualRemotePresent(subject, storedEvidence)
		if err != nil {
			return err
		}
		if decision.NMID != evidence.Card.NMID || decision.IMTID != evidence.Card.IMTID ||
			decision.SubjectID != evidence.Card.SubjectID {
			return ErrManualEvidenceInvalid
		}
		decision.OutcomeCode = "VERIFIED_IN_WB"
		if err := resolver.repository.ApplyVerifiedCardResult(ctx, tx, subject, decision); err != nil {
			return err
		}
		if err := resolver.transfer.CorrectWithin(ctx, tx, transfer_service.CorrectPublicationItemResultCommand{
			TransferID:              subject.TransferID,
			ActionID:                subject.ActionID,
			GroupTargetID:           subject.GroupTargetID,
			TransferItemTargetID:    subject.TransferItemTargetID,
			ExpectedItemRevision:    subject.ItemTargetRevision,
			ExpectedOutcomeClass:    subject.MemberOutcomeClass,
			ExpectedOutcomeCode:     subject.MemberOutcomeCode,
			ExpectedNMID:            subject.MemberNMID,
			ExpectedAttentionClosed: subject.AttentionClosedAt != nil,
			OutcomeClass:            decision.OutcomeClass,
			OutcomeCode:             decision.OutcomeCode,
			NMID:                    decision.NMID,
		}); err != nil {
			return fmt.Errorf("correct verified card transfer result: %w", err)
		}
		return nil
	})
}
