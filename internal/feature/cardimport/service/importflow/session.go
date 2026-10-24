package cardimport_flow

import (
	"context"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	cardimport_intake "github.com/ERONIS/wb-service/internal/feature/cardimport/service/intake"
)

// Begin начинает сбор карточек или возвращает активную сессию автора.
func (s *Service) Begin(ctx context.Context, command cardimport_contract.BeginCommand) (cardimport_intake.Session, error) {
	if err := command.Validate(); err != nil {
		return cardimport_intake.Session{}, err
	}
	exists, err := s.repository.HasAuthor(ctx, command.AuthorTelegramID)
	if err != nil {
		return cardimport_intake.Session{}, fmt.Errorf("check cardimport author: %w", err)
	}
	if !exists {
		return cardimport_intake.Session{}, fmt.Errorf("cardimport author Telegram ID=%d: %w", command.AuthorTelegramID, core_errors.ErrNotFound)
	}
	session, err := s.sessions.Begin(ctx, command)
	if err != nil {
		return cardimport_intake.Session{}, err
	}
	if err := validateSessionOwner(session, command.AuthorTelegramID); err != nil {
		return cardimport_intake.Session{}, err
	}
	if session.Status != cardimport_intake.SessionStatusCollecting {
		return cardimport_intake.Session{}, fmt.Errorf("cardimport session is not collecting: %w", core_errors.ErrConflict)
	}
	return session, nil
}

// Continue продолжает импорт после исключения ошибочных карточек.
func (s *Service) Continue(ctx context.Context, command cardimport_contract.ContinueCommand) (cardimport_intake.SessionView, error) {
	if err := command.Validate(); err != nil {
		return cardimport_intake.SessionView{}, err
	}
	session, err := s.sessions.ContinueAfterErrors(ctx, command)
	if err != nil {
		return cardimport_intake.SessionView{}, err
	}
	if err := validateSessionOwner(session, command.AuthorTelegramID); err != nil {
		return cardimport_intake.SessionView{}, err
	}
	if session.Status != cardimport_intake.SessionStatusCollecting || session.Revision != command.ExpectedRevision+1 {
		return cardimport_intake.SessionView{}, fmt.Errorf("cardimport session changed while continuing: %w", core_errors.ErrConflict)
	}
	return s.GetView(ctx, cardimport_contract.SessionCommand{AuthorTelegramID: command.AuthorTelegramID, SessionID: command.SessionID})
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
	if session.Status != cardimport_intake.SessionStatusCancelled {
		return fmt.Errorf("cardimport session was not cancelled: %w", core_errors.ErrConflict)
	}
	return nil
}

// Get возвращает сессию по автору и ID.
func (s *Service) Get(ctx context.Context, command cardimport_contract.SessionCommand) (cardimport_intake.Session, error) {
	if err := command.Validate(); err != nil {
		return cardimport_intake.Session{}, err
	}
	session, err := s.sessions.Get(ctx, command)
	if err != nil {
		return cardimport_intake.Session{}, err
	}
	if err := validateSessionOwner(session, command.AuthorTelegramID); err != nil {
		return cardimport_intake.Session{}, err
	}
	return session, nil
}

// GetActiveView возвращает текущую сессию автора вместе с результатами.
func (s *Service) GetActiveView(ctx context.Context, authorTelegramID int64) (cardimport_intake.SessionView, error) {
	if authorTelegramID <= 0 {
		return cardimport_intake.SessionView{}, cardimport_contract.InvalidTelegramID(authorTelegramID)
	}
	view, err := s.sessions.ActiveView(ctx, authorTelegramID)
	if err != nil {
		return cardimport_intake.SessionView{}, err
	}
	if err := validateSessionView(view, authorTelegramID); err != nil {
		return cardimport_intake.SessionView{}, err
	}
	return view, nil
}

// GetView возвращает состояние указанной сессии и её файлов.
func (s *Service) GetView(ctx context.Context, command cardimport_contract.SessionCommand) (cardimport_intake.SessionView, error) {
	if err := command.Validate(); err != nil {
		return cardimport_intake.SessionView{}, err
	}
	view, err := s.sessions.View(ctx, command)
	if err != nil {
		return cardimport_intake.SessionView{}, err
	}
	if err := validateSessionView(view, command.AuthorTelegramID); err != nil {
		return cardimport_intake.SessionView{}, err
	}
	return view, nil
}

// validateSessionOwner проверяет корректность сессии и её владельца.
func validateSessionOwner(session cardimport_intake.Session, authorTelegramID int64) error {
	if err := session.Validate(); err != nil {
		return fmt.Errorf("validate cardimport session: %w", err)
	}
	if session.AuthorTelegramID != authorTelegramID {
		return fmt.Errorf("cardimport session author does not match command: %w", core_errors.ErrConflict)
	}
	return nil
}

// validateSessionView проверяет счётчики и файлы представления сессии.
func validateSessionView(view cardimport_intake.SessionView, authorTelegramID int64) error {
	if err := validateSessionOwner(view.Session, authorTelegramID); err != nil {
		return err
	}
	if view.CardsCount < 0 || view.ErrorsCount < 0 {
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
