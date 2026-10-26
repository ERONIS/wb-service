package cardimport_workflow

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"
)

// Finalize замораживает сессию и сохраняет готовую пачку транзакционно.
func (s *Service) Finalize(
	ctx context.Context,
	user domain.User,
	command cardimport_contract.FinalizeCommand,
) (cardimport_model.BatchHeader, error) {
	command = command.Normalized()
	if err := user.Validate(); err != nil {
		return cardimport_model.BatchHeader{}, err
	}
	if err := command.Validate(); err != nil {
		return cardimport_model.BatchHeader{}, err
	}

	finalized, existing, err := s.sessions.PrepareFinalization(ctx, user, command)
	if err != nil {
		return cardimport_model.BatchHeader{}, err
	}
	if existing.ID > 0 {
		return existing, nil
	}
	finished := false
	defer func() {
		if !finished {
			s.sessions.FinishFinalization(command.SessionID, cardimport_model.BatchHeader{}, core_errors.ErrConflict)
		}
	}()
	var batch cardimport_model.BatchHeader
	err = s.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			var err error
			batch, err = s.repository.PersistFinalized(
				ctx,
				tx,
				finalized,
			)
			if err != nil {
				return err
			}
			return nil
		},
	)
	finished = true
	s.sessions.FinishFinalization(command.SessionID, batch, err)
	if err != nil {
		return cardimport_model.BatchHeader{}, fmt.Errorf("finalize cardimport session: %w", err)
	}

	return batch, nil
}

// GetBatch возвращает заголовок завершённой пачки.
func (s *Service) GetBatch(
	ctx context.Context,
	batchID cardpipeline.BatchID,
) (cardimport_model.BatchHeader, error) {
	if batchID <= 0 {
		return cardimport_model.BatchHeader{}, fmt.Errorf(
			"invalid cardimport batch ID '%d': %w",
			batchID,
			core_errors.ErrInvalidArgument,
		)
	}

	return s.repository.GetBatch(ctx, batchID)
}
