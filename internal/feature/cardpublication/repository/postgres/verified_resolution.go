package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
)

func (repository *Repository) ApplyVerifiedCardResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	subject cardpublication_service.ManualResolutionSubject,
	decision cardpublication_service.ManualResolutionDecision,
) error {
	if tx == nil || decision.Kind != cardpublication_service.ManualMarkRemotePresent ||
		decision.OutcomeClass != "success" || decision.OutcomeCode != "VERIFIED_IN_WB" ||
		decision.NMID <= 0 || decision.IMTID <= 0 || decision.SubjectID <= 0 ||
		decision.EvidenceObservationID <= 0 {
		return errors.New("verified card result is invalid")
	}
	command := cardpublication_service.ApplyManualResolutionCommand{
		Subject:  subject,
		Decision: decision,
	}
	if err := applyManualMemberResult(ctx, tx, command); err != nil {
		return fmt.Errorf("apply verified card member result: %w", err)
	}
	if err := applyManualEvidence(ctx, tx, command); err != nil {
		return fmt.Errorf("record verified card evidence: %w", err)
	}
	if _, err := reduceManualPublicationAction(ctx, tx, command); err != nil {
		return err
	}
	return reduceManualPublicationPlan(ctx, tx, subject.TransferID, subject.PlanID)
}

func resolutionActionCodes(command cardpublication_service.ApplyManualResolutionCommand) [4]string {
	if command.Decision.OutcomeCode == "VERIFIED_IN_WB" {
		return [4]string{
			"VERIFIED_IN_WB_REMAINS_UNRESOLVED",
			"VERIFIED_IN_WB_REJECTED",
			"VERIFIED_IN_WB_PARTIAL",
			"VERIFIED_IN_WB",
		}
	}
	return [4]string{
		"MANUAL_RESOLUTION_REMAINS_UNRESOLVED",
		"MANUAL_RESOLUTION_REJECTED",
		"MANUAL_RESOLUTION_PARTIAL",
		"MANUAL_RESOLUTION_SUCCEEDED",
	}
}
