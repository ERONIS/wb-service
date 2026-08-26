package cardpublication_service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

const MaxManualResolutionIdempotencyKeyLength = 160

var (
	ErrManualResolutionConflict = errors.New("publication manual resolution conflict")
	ErrManualEvidenceInvalid    = errors.New("publication manual evidence is invalid")
)

type ManualResolutionKind string

const (
	ManualMarkRemotePresent      ManualResolutionKind = "mark_remote_present"
	ManualMarkRejected           ManualResolutionKind = "mark_rejected"
	ManualCloseUnresolvedNoRetry ManualResolutionKind = "close_unresolved_no_retry"
)

func (kind ManualResolutionKind) IsValid() bool {
	switch kind {
	case ManualMarkRemotePresent, ManualMarkRejected, ManualCloseUnresolvedNoRetry:
		return true
	default:
		return false
	}
}

type ManualTrustedActor struct {
	ID          int64
	DisplayName string
}

func (actor ManualTrustedActor) normalized() ManualTrustedActor {
	actor.DisplayName = strings.TrimSpace(actor.DisplayName)
	return actor
}

func (actor ManualTrustedActor) validate() error {
	if actor.ID <= 0 || actor.DisplayName == "" || len([]rune(actor.DisplayName)) > 100 {
		return errors.New("manual resolution trusted actor is invalid")
	}
	return nil
}

func (actor ManualTrustedActor) digest() Digest {
	return digestParts(
		"cardpublication-manual-actor:v1",
		[]byte(strconv.FormatInt(actor.ID, 10)),
	)
}

type ManualResolutionCommand struct {
	TransferID             transfer_service.TransferID
	ActionID               int64
	ActionMemberID         int64
	ExpectedActionRevision int64
	Kind                   ManualResolutionKind
	EvidenceObservationID  int64
	EvidenceErrorBatchID   int64
	IdempotencyKey         string
}

func (command ManualResolutionCommand) normalized() ManualResolutionCommand {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	return command
}

func (command ManualResolutionCommand) validate() error {
	if command.TransferID <= 0 || command.ActionID <= 0 ||
		command.ActionMemberID <= 0 || command.ExpectedActionRevision < 0 ||
		!command.Kind.IsValid() || command.IdempotencyKey == "" ||
		len(command.IdempotencyKey) > MaxManualResolutionIdempotencyKeyLength {
		return errors.New("manual resolution command is invalid")
	}
	switch command.Kind {
	case ManualMarkRemotePresent:
		if command.EvidenceObservationID <= 0 || command.EvidenceErrorBatchID != 0 {
			return ErrManualEvidenceInvalid
		}
	case ManualMarkRejected:
		if command.EvidenceObservationID != 0 || command.EvidenceErrorBatchID <= 0 {
			return ErrManualEvidenceInvalid
		}
	case ManualCloseUnresolvedNoRetry:
		if command.EvidenceObservationID != 0 || command.EvidenceErrorBatchID != 0 {
			return ErrManualEvidenceInvalid
		}
	}
	return nil
}

func (command ManualResolutionCommand) digest() Digest {
	return digestParts(
		"cardpublication-manual-command:v1",
		[]byte(strconv.FormatInt(int64(command.TransferID), 10)),
		[]byte(strconv.FormatInt(command.ActionID, 10)),
		[]byte(strconv.FormatInt(command.ActionMemberID, 10)),
		[]byte(strconv.FormatInt(command.ExpectedActionRevision, 10)),
		[]byte(command.Kind),
		[]byte(strconv.FormatInt(command.EvidenceObservationID, 10)),
		[]byte(strconv.FormatInt(command.EvidenceErrorBatchID, 10)),
	)
}

type ManualResolution struct {
	ID                   int64
	TransferID           transfer_service.TransferID
	ActionID             int64
	ActionMemberID       int64
	Kind                 ManualResolutionKind
	ResultActionRevision int64
	OutcomeClass         transfer_service.ResultClass
	OutcomeCode          string
	NMID                 int64
	IMTID                int64
	SubjectID            int64
	CreatedAt            time.Time
}

type ManualResolutionSubject struct {
	TransferID               transfer_service.TransferID
	ActionID                 int64
	ActionMemberID           int64
	ActionRevision           int64
	PlanID                   int64
	TargetID                 int64
	CabinetID                CabinetID
	ActionKind               ActionKind
	RequestPayload           []byte
	PlanDigest               Digest
	GroupTargetID            int64
	TransferItemTargetID     int64
	ItemTargetRevision       int64
	VendorCode               string
	MemberOutcomeClass       transfer_service.ResultClass
	MemberOutcomeCode        string
	MemberNMID               int64
	AttemptID                int64
	PreflightObservationID   int64
	AttemptStartedAt         time.Time
	AttentionClosedAt        *time.Time
	IdentityState            string
	IdentityNMID             int64
	IdentityIMTID            int64
	IdentitySubjectID        int64
	IdentityActiveTransferID transfer_service.TransferID
	IdentityActiveActionID   int64
}

type ManualObservationEvidence struct {
	ID         int64
	Digest     Digest
	Payload    []byte
	ObservedAt time.Time
}

type ManualErrorEvidence struct {
	ID         int64
	ErrorCodes []string
}

type ManualResolutionDecision struct {
	Kind                  ManualResolutionKind
	OutcomeClass          transfer_service.ResultClass
	OutcomeCode           string
	NMID                  int64
	IMTID                 int64
	SubjectID             int64
	EvidenceObservationID int64
	EvidenceErrorBatchID  int64
	CloseAttentionNoRetry bool
}

type ApplyManualResolutionCommand struct {
	Actor         ManualTrustedActor
	ActorDigest   Digest
	Command       ManualResolutionCommand
	CommandDigest Digest
	Subject       ManualResolutionSubject
	Decision      ManualResolutionDecision
}

type ManualResolutionRepository interface {
	LoadManualResolutionReplay(
		context.Context,
		core_postgres_transaction.DBTX,
		string,
		Digest,
		Digest,
	) (ManualResolution, bool, error)

	LockManualResolutionSubject(
		context.Context,
		core_postgres_transaction.DBTX,
		ManualResolutionCommand,
	) (ManualResolutionSubject, error)

	LoadManualObservationEvidence(
		context.Context,
		core_postgres_transaction.DBTX,
		ManualResolutionSubject,
		int64,
	) (ManualObservationEvidence, error)

	LoadManualErrorEvidence(
		context.Context,
		core_postgres_transaction.DBTX,
		ManualResolutionSubject,
		int64,
	) (ManualErrorEvidence, error)

	ApplyManualResolution(
		context.Context,
		core_postgres_transaction.DBTX,
		ApplyManualResolutionCommand,
	) (ManualResolution, error)
}

type ManualTransferResultCorrector interface {
	CorrectWithin(
		context.Context,
		core_postgres_transaction.DBTX,
		transfer_service.CorrectPublicationItemResultCommand,
	) error
}

type ManualResolver struct {
	repository ManualResolutionRepository
	transfer   ManualTransferResultCorrector
	uow        core_postgres_transaction.UnitOfWork
}

func NewManualResolver(
	repository ManualResolutionRepository,
	transfer ManualTransferResultCorrector,
	uow core_postgres_transaction.UnitOfWork,
) *ManualResolver {
	if repository == nil || transfer == nil || uow == nil {
		panic("cardpublication manual resolver dependency is nil")
	}
	return &ManualResolver{repository: repository, transfer: transfer, uow: uow}
}

func (resolver *ManualResolver) Resolve(
	ctx context.Context,
	actor ManualTrustedActor,
	command ManualResolutionCommand,
) (ManualResolution, error) {
	if ctx == nil {
		return ManualResolution{}, errors.New("manual resolution context is nil")
	}
	actor = actor.normalized()
	command = command.normalized()
	if err := actor.validate(); err != nil {
		return ManualResolution{}, err
	}
	if err := command.validate(); err != nil {
		return ManualResolution{}, err
	}
	actorDigest, commandDigest := actor.digest(), command.digest()
	var resolution ManualResolution
	err := resolver.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			replay, found, err := resolver.repository.LoadManualResolutionReplay(
				ctx,
				tx,
				command.IdempotencyKey,
				actorDigest,
				commandDigest,
			)
			if err != nil {
				return err
			}
			if found {
				resolution = replay
				return nil
			}
			subject, err := resolver.repository.LockManualResolutionSubject(ctx, tx, command)
			if err != nil {
				if errors.Is(err, ErrManualResolutionConflict) {
					replay, found, replayErr := resolver.repository.LoadManualResolutionReplay(
						ctx,
						tx,
						command.IdempotencyKey,
						actorDigest,
						commandDigest,
					)
					if replayErr != nil {
						return replayErr
					}
					if found {
						resolution = replay
						return nil
					}
				}
				return err
			}
			decision, err := resolver.decide(ctx, tx, subject, command)
			if err != nil {
				return err
			}
			resolution, err = resolver.repository.ApplyManualResolution(
				ctx,
				tx,
				ApplyManualResolutionCommand{
					Actor:         actor,
					ActorDigest:   actorDigest,
					Command:       command,
					CommandDigest: commandDigest,
					Subject:       subject,
					Decision:      decision,
				},
			)
			if err != nil {
				return err
			}
			outcomeClass, outcomeCode, nmID := decision.OutcomeClass, decision.OutcomeCode, decision.NMID
			if decision.CloseAttentionNoRetry {
				outcomeClass = subject.MemberOutcomeClass
				outcomeCode = subject.MemberOutcomeCode
				nmID = subject.MemberNMID
			}
			return resolver.transfer.CorrectWithin(
				ctx,
				tx,
				transfer_service.CorrectPublicationItemResultCommand{
					TransferID:              subject.TransferID,
					ActionID:                subject.ActionID,
					GroupTargetID:           subject.GroupTargetID,
					TransferItemTargetID:    subject.TransferItemTargetID,
					ExpectedItemRevision:    subject.ItemTargetRevision,
					ExpectedOutcomeClass:    subject.MemberOutcomeClass,
					ExpectedOutcomeCode:     subject.MemberOutcomeCode,
					ExpectedNMID:            subject.MemberNMID,
					ExpectedAttentionClosed: subject.AttentionClosedAt != nil,
					OutcomeClass:            outcomeClass,
					OutcomeCode:             outcomeCode,
					NMID:                    nmID,
					CloseAttentionNoRetry:   decision.CloseAttentionNoRetry,
				},
			)
		},
	)
	if err != nil {
		return ManualResolution{}, fmt.Errorf("resolve publication evidence: %w", err)
	}
	return resolution, nil
}

func (resolver *ManualResolver) decide(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	subject ManualResolutionSubject,
	command ManualResolutionCommand,
) (ManualResolutionDecision, error) {
	switch command.Kind {
	case ManualMarkRemotePresent:
		evidence, err := resolver.repository.LoadManualObservationEvidence(
			ctx,
			tx,
			subject,
			command.EvidenceObservationID,
		)
		if err != nil {
			return ManualResolutionDecision{}, err
		}
		return decideManualRemotePresent(subject, evidence)
	case ManualMarkRejected:
		evidence, err := resolver.repository.LoadManualErrorEvidence(
			ctx,
			tx,
			subject,
			command.EvidenceErrorBatchID,
		)
		if err != nil || evidence.ID <= 0 || len(evidence.ErrorCodes) == 0 {
			if err != nil {
				return ManualResolutionDecision{}, err
			}
			return ManualResolutionDecision{}, ErrManualEvidenceInvalid
		}
		return ManualResolutionDecision{
			Kind:                 command.Kind,
			OutcomeClass:         transfer_service.ResultRejected,
			OutcomeCode:          "MANUAL_ERROR_LIST_REJECTED",
			EvidenceErrorBatchID: evidence.ID,
		}, nil
	case ManualCloseUnresolvedNoRetry:
		if subject.AttentionClosedAt != nil {
			return ManualResolutionDecision{}, ErrManualResolutionConflict
		}
		if subject.IdentityState != "blocked_uncertain" ||
			subject.IdentityActiveTransferID != subject.TransferID ||
			subject.IdentityActiveActionID != subject.ActionID {
			return ManualResolutionDecision{}, ErrManualResolutionConflict
		}
		return ManualResolutionDecision{
			Kind:                  command.Kind,
			OutcomeClass:          subject.MemberOutcomeClass,
			OutcomeCode:           "MANUAL_CLOSED_UNRESOLVED_NO_RETRY",
			CloseAttentionNoRetry: true,
		}, nil
	default:
		return ManualResolutionDecision{}, ErrManualResolutionConflict
	}
}

type manualCatalogPayload struct {
	Normal []contentapi.Card      `json:"normal"`
	Trash  []contentapi.TrashCard `json:"trash"`
}

func decideManualRemotePresent(
	subject ManualResolutionSubject,
	evidence ManualObservationEvidence,
) (ManualResolutionDecision, error) {
	if evidence.ID <= 0 || evidence.Digest == (Digest{}) || len(evidence.Payload) == 0 ||
		evidence.ObservedAt.IsZero() || evidence.ObservedAt.Before(subject.AttemptStartedAt) {
		return ManualResolutionDecision{}, ErrManualEvidenceInvalid
	}
	var payload manualCatalogPayload
	if err := json.Unmarshal(evidence.Payload, &payload); err != nil {
		return ManualResolutionDecision{}, ErrManualEvidenceInvalid
	}
	matching := make([]contentapi.Card, 0, 1)
	for _, card := range payload.Normal {
		if card.VendorCode == subject.VendorCode {
			matching = append(matching, card)
		}
	}
	for _, card := range payload.Trash {
		if card.VendorCode == subject.VendorCode {
			return ManualResolutionDecision{}, ErrManualEvidenceInvalid
		}
	}
	if len(matching) != 1 || matching[0].NMID <= 0 || matching[0].IMTID <= 0 ||
		matching[0].SubjectID <= 0 {
		return ManualResolutionDecision{}, ErrManualEvidenceInvalid
	}
	card := matching[0]
	switch subject.ActionKind {
	case ActionCreateGroup:
		var request contentapi.UploadCardsRequest
		if err := decodeExactJSON(subject.RequestPayload, &request); err != nil ||
			!uploadVendorMatchesSubject(request, subject.VendorCode, card.SubjectID) {
			return ManualResolutionDecision{}, ErrManualEvidenceInvalid
		}
	case ActionAddToGroup:
		var request contentapi.UploadCardsAddRequest
		if err := decodeExactJSON(subject.RequestPayload, &request); err != nil ||
			request.IMTID <= 0 || request.IMTID != card.IMTID ||
			countUploadVendor(request.CardsToAdd, subject.VendorCode) != 1 {
			return ManualResolutionDecision{}, ErrManualEvidenceInvalid
		}
	default:
		return ManualResolutionDecision{}, ErrManualEvidenceInvalid
	}
	return ManualResolutionDecision{
		Kind:                  ManualMarkRemotePresent,
		OutcomeClass:          transfer_service.ResultSuccess,
		OutcomeCode:           "MANUAL_REMOTE_PRESENT",
		NMID:                  card.NMID,
		IMTID:                 card.IMTID,
		SubjectID:             card.SubjectID,
		EvidenceObservationID: evidence.ID,
	}, nil
}

func countUploadVendor(cards []contentapi.UploadCard, vendorCode string) int {
	count := 0
	for _, card := range cards {
		if card.VendorCode == vendorCode {
			count++
		}
	}
	return count
}

func uploadVendorMatchesSubject(
	request contentapi.UploadCardsRequest,
	vendorCode string,
	subjectID int64,
) bool {
	matches := 0
	for _, group := range request {
		count := countUploadVendor(group.Variants, vendorCode)
		if count == 0 {
			continue
		}
		if count != 1 || group.SubjectID != subjectID {
			return false
		}
		matches++
	}
	return matches == 1
}
