package cardimport_workflow

import (
	"context"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"
)

// Begin начинает сбор карточек или возвращает активную сессию автора.
func (s *Service) Begin(ctx context.Context, command cardimport_contract.BeginCommand) (cardimport_model.Session, error) {
	if err := command.Validate(); err != nil {
		return cardimport_model.Session{}, err
	}
	exists, err := s.repository.HasAuthor(ctx, command.AuthorTelegramID)
	if err != nil {
		return cardimport_model.Session{}, fmt.Errorf("check cardimport author: %w", err)
	}
	if !exists {
		return cardimport_model.Session{}, fmt.Errorf("cardimport author Telegram ID=%d: %w", command.AuthorTelegramID, core_errors.ErrNotFound)
	}
	session, err := s.sessions.Begin(ctx, command)
	if err != nil {
		return cardimport_model.Session{}, err
	}
	if err := validateSessionOwner(session, command.AuthorTelegramID); err != nil {
		return cardimport_model.Session{}, err
	}
	if session.Status != cardimport_model.SessionStatusCollecting {
		return cardimport_model.Session{}, fmt.Errorf("cardimport session is not collecting: %w", core_errors.ErrConflict)
	}
	return session, nil
}

// Cancel отменяет незавершённую сессию автора.
func (s *Service) Cancel(ctx context.Context, command cardimport_contract.SessionCommand) error {
	if err := command.Validate(); err != nil {
		return err
	}
	session, err := s.sessions.Cancel(ctx, command)
	if err != nil {
		return err
	}
	if err := validateSessionOwner(session, command.AuthorTelegramID); err != nil {
		return err
	}
	if session.Status != cardimport_model.SessionStatusCancelled {
		return fmt.Errorf("cardimport session was not cancelled: %w", core_errors.ErrConflict)
	}
	return nil
}

// Get возвращает сессию по автору и ID.
func (s *Service) Get(ctx context.Context, command cardimport_contract.SessionCommand) (cardimport_model.Session, error) {
	if err := command.Validate(); err != nil {
		return cardimport_model.Session{}, err
	}
	session, err := s.sessions.Get(ctx, command)
	if err != nil {
		return cardimport_model.Session{}, err
	}
	if err := validateSessionOwner(session, command.AuthorTelegramID); err != nil {
		return cardimport_model.Session{}, err
	}
	return session, nil
}

// GetActiveView возвращает текущую сессию автора вместе с результатами.
func (s *Service) GetActiveView(ctx context.Context, authorTelegramID int64) (cardimport_model.SessionView, error) {
	if authorTelegramID <= 0 {
		return cardimport_model.SessionView{}, cardimport_contract.InvalidTelegramID(authorTelegramID)
	}
	view, err := s.sessions.ActiveView(ctx, authorTelegramID)
	if err != nil {
		return cardimport_model.SessionView{}, err
	}
	if err := validateSessionView(view, authorTelegramID); err != nil {
		return cardimport_model.SessionView{}, err
	}
	return view, nil
}

// GetView возвращает состояние указанной сессии и её файлов.
func (s *Service) GetView(ctx context.Context, command cardimport_contract.SessionCommand) (cardimport_model.SessionView, error) {
	if err := command.Validate(); err != nil {
		return cardimport_model.SessionView{}, err
	}
	view, err := s.sessions.View(ctx, command)
	if err != nil {
		return cardimport_model.SessionView{}, err
	}
	if err := validateSessionView(view, command.AuthorTelegramID); err != nil {
		return cardimport_model.SessionView{}, err
	}
	return view, nil
}

// validateSessionOwner проверяет корректность сессии и её владельца.
func validateSessionOwner(session cardimport_model.Session, authorTelegramID int64) error {
	if err := session.Validate(); err != nil {
		return fmt.Errorf("validate cardimport session: %w", err)
	}
	if session.AuthorTelegramID != authorTelegramID {
		return fmt.Errorf("cardimport session author does not match command: %w", core_errors.ErrConflict)
	}
	return nil
}

// validateSessionView проверяет счётчики и файлы представления сессии.
func validateSessionView(view cardimport_model.SessionView, authorTelegramID int64) error {
	if err := validateSessionOwner(view.Session, authorTelegramID); err != nil {
		return err
	}
	if view.CardsCount < 0 || view.ReadyCardsCount < 0 || view.ReadyCardsCount > view.CardsCount || view.ErrorsCount < 0 {
		return fmt.Errorf("invalid cardimport session counters: %w", core_errors.ErrConflict)
	}
	for _, fileView := range view.Files {
		if err := fileView.File.Validate(); err != nil {
			return fmt.Errorf("validate cardimport file view: %w", err)
		}
		if fileView.File.SessionID != view.ID || fileView.CardsCount < 0 || fileView.ErrorsCount < 0 {
			return fmt.Errorf("invalid cardimport file view: %w", core_errors.ErrConflict)
		}
	}
	return nil
}
