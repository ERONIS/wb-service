package planning

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type Saver struct {
	repository    Repository
	resultApplier TransferResultApplier
	uow           core_postgres_transaction.UnitOfWork
}

func NewSaver(
	repository Repository,
	resultApplier TransferResultApplier,
	uow core_postgres_transaction.UnitOfWork,
) *Saver {
	if repository == nil || resultApplier == nil || uow == nil {
		panic("cardpublication saver dependency is nil")
	}
	return &Saver{
		repository:    repository,
		resultApplier: resultApplier,
		uow:           uow,
	}
}

func (saver *Saver) Save(ctx context.Context, draft PlanDraft) error {
	if ctx == nil {
		return errors.New("save publication plan: context is nil")
	}
	if err := draft.Validate(); err != nil {
		return err
	}
	if err := saver.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			persisted, err := saver.repository.InsertPlan(ctx, tx, draft)
			if err != nil {
				return err
			}
			actionIDs := make(map[Digest]int64, len(persisted.Actions))
			for _, action := range persisted.Actions {
				actionIDs[action.Key] = action.ID
			}
			command := transfer_service.ApplyPublicationPlanResultCommand{
				TransferID:  draft.TransferID,
				PlanID:      persisted.ID,
				ActionCount: len(draft.Actions),
				Groups:      make([]transfer_service.PublicationPlannedGroup, 0, len(draft.Groups)),
				Items:       make([]transfer_service.PublicationPlannedItem, 0, len(draft.Items)),
			}
			for _, group := range draft.Groups {
				command.Groups = append(command.Groups, transfer_service.PublicationPlannedGroup{
					GroupTargetID:  group.GroupTargetID,
					Status:         group.Status,
					OutcomeClass:   group.OutcomeClass,
					OutcomeCode:    string(group.OutcomeCode),
					HasMediaAction: group.HasMediaAction,
				})
			}
			for _, item := range draft.Items {
				var actionID int64
				if item.ActionKey != (Digest{}) {
					var exists bool
					actionID, exists = actionIDs[item.ActionKey]
					if !exists {
						return errors.New("publication item action was not persisted")
					}
				}
				command.Items = append(command.Items, transfer_service.PublicationPlannedItem{
					GroupTargetID:        item.GroupTargetID,
					TransferItemTargetID: item.TransferItemTargetID,
					SourceActionID:       actionID,
					OutcomeClass:         item.OutcomeClass,
					OutcomeCode:          string(item.OutcomeCode),
					NMID:                 item.NMID,
				})
			}
			return saver.resultApplier.ApplyWithin(ctx, tx, command)
		},
	); err != nil {
		return fmt.Errorf("save publication plan transaction: %w", err)
	}
	return nil
}
