package planning

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_model "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
	transfer_publication "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
)

type Saver struct {
	repository       Repository
	resultRepository TransferResultRepository
	uow              core_postgres_transaction.UnitOfWork
}

func NewSaver(
	repository Repository,
	resultRepository TransferResultRepository,
	uow core_postgres_transaction.UnitOfWork,
) *Saver {
	if repository == nil || resultRepository == nil || uow == nil {
		panic("cardpublication saver dependency is nil")
	}
	return &Saver{
		repository:       repository,
		resultRepository: resultRepository,
		uow:              uow,
	}
}

func (saver *Saver) Save(ctx context.Context, draft cardpublication_model.PlanDraft) error {
	if ctx == nil {
		return errors.New("save publication plan: context is nil")
	}
	if err := saver.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			persisted, err := saver.repository.InsertPlan(ctx, tx, draft)
			if err != nil {
				return err
			}
			actionIDs := make(map[int]int64, len(persisted.Actions))
			for _, action := range persisted.Actions {
				actionIDs[action.Position] = action.ID
			}
			command := transfer_publication.ApplyPublicationPlanResultCommand{
				TransferID:  draft.TransferID,
				PlanID:      persisted.ID,
				ActionCount: len(draft.Actions),
				Groups:      make([]transfer_publication.PublicationPlannedGroup, 0, len(draft.Groups)),
				Items:       make([]transfer_publication.PublicationPlannedItem, 0, len(draft.Items)),
			}
			for _, group := range draft.Groups {
				command.Groups = append(command.Groups, transfer_publication.PublicationPlannedGroup{
					GroupTargetID:  group.GroupTargetID,
					Status:         group.Status,
					OutcomeClass:   group.OutcomeClass,
					OutcomeCode:    string(group.OutcomeCode),
					HasMediaAction: group.HasMediaAction,
				})
			}
			for _, item := range draft.Items {
				var actionID int64
				if item.ActionPosition > 0 {
					var exists bool
					actionID, exists = actionIDs[item.ActionPosition]
					if !exists {
						return errors.New("publication item action was not persisted")
					}
				}
				command.Items = append(command.Items, transfer_publication.PublicationPlannedItem{
					GroupTargetID:        item.GroupTargetID,
					TransferItemTargetID: item.TransferItemTargetID,
					SourceActionID:       actionID,
					OutcomeClass:         item.OutcomeClass,
					OutcomeCode:          string(item.OutcomeCode),
					NMID:                 item.NMID,
				})
			}
			return saver.resultRepository.ApplyPublicationPlanResult(ctx, tx, command)
		},
	); err != nil {
		return fmt.Errorf("save publication plan transaction: %w", err)
	}
	return nil
}
