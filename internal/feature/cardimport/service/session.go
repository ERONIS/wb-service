package cardimport_service

import (
	"context"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

// Begin создаёт collecting-сессию или возвращает уже открытую.
func (s *Service) Begin(
	ctx context.Context,
	command BeginCommand,
) (Session, error) {
	if err := command.validate(); err != nil {
		return Session{}, err
	}

	session, err := s.repository.GetOrCreateCollectingSession(
		ctx,
		command.AuthorTelegramID,
		command.Purpose,
	)
	if err != nil {
		return Session{}, fmt.Errorf(
			"get or create collecting cardimport session: %w",
			err,
		)
	}

	if err := validateSessionOwner(session, command.AuthorTelegramID); err != nil {
		return Session{}, err
	}
	if session.Status != SessionStatusCollecting {
		return Session{}, fmt.Errorf(
			"cardimport session is not collecting: %w",
			core_errors.ErrConflict,
		)
	}
	if session.Purpose != command.Purpose {
		return Session{}, fmt.Errorf(
			"another cardimport purpose is already active: %w",
			core_errors.ErrConflict,
		)
	}

	return session, nil
}

func (s *Service) Get(
	ctx context.Context,
	command SessionCommand,
) (Session, error) {
	if err := command.validate(); err != nil {
		return Session{}, err
	}

	session, err := s.repository.GetSession(
		ctx,
		command.AuthorTelegramID,
		command.SessionID,
	)
	if err != nil {
		return Session{}, fmt.Errorf("get cardimport session: %w", err)
	}
	if err := validateSessionOwner(session, command.AuthorTelegramID); err != nil {
		return Session{}, err
	}

	return session, nil
}

func (s *Service) GetActiveView(
	ctx context.Context,
	authorTelegramID int64,
) (SessionView, error) {
	if authorTelegramID <= 0 {
		return SessionView{}, invalidTelegramID(authorTelegramID)
	}

	view, err := s.repository.GetCollectingSessionView(
		ctx,
		authorTelegramID,
	)
	if err != nil {
		return SessionView{}, fmt.Errorf(
			"get collecting cardimport session view: %w",
			err,
		)
	}
	if err := validateSessionView(view, authorTelegramID); err != nil {
		return SessionView{}, err
	}

	return view, nil
}

func (s *Service) GetView(
	ctx context.Context,
	command SessionCommand,
) (SessionView, error) {
	if err := command.validate(); err != nil {
		return SessionView{}, err
	}

	view, err := s.repository.GetSessionView(
		ctx,
		command.AuthorTelegramID,
		command.SessionID,
	)
	if err != nil {
		return SessionView{}, fmt.Errorf(
			"get cardimport session view: %w",
			err,
		)
	}
	if err := validateSessionView(view, command.AuthorTelegramID); err != nil {
		return SessionView{}, err
	}

	return view, nil
}

// Cancel идемпотентно отменяет принадлежащую пользователю сессию.
func (s *Service) Cancel(
	ctx context.Context,
	command SessionCommand,
) error {
	if err := command.validate(); err != nil {
		return err
	}

	session, err := s.repository.CancelSession(
		ctx,
		command.AuthorTelegramID,
		command.SessionID,
	)
	if err != nil {
		return fmt.Errorf("cancel cardimport session: %w", err)
	}
	if err := validateSessionOwner(session, command.AuthorTelegramID); err != nil {
		return err
	}
	if session.Status != SessionStatusCancelled {
		return fmt.Errorf(
			"cardimport session was not cancelled: %w",
			core_errors.ErrConflict,
		)
	}

	return nil
}

func validateSessionOwner(
	session Session,
	authorTelegramID int64,
) error {
	if err := session.Validate(); err != nil {
		return fmt.Errorf("validate cardimport session: %w", err)
	}
	if session.AuthorTelegramID != authorTelegramID {
		return fmt.Errorf(
			"cardimport session author does not match command: %w",
			core_errors.ErrConflict,
		)
	}

	return nil
}

func validateSessionView(view SessionView, authorTelegramID int64) error {
	if err := validateSessionOwner(view.Session, authorTelegramID); err != nil {
		return err
	}
	if view.CardsCount < 0 || view.ErrorsCount < 0 {
		return fmt.Errorf(
			"invalid cardimport session counters: %w",
			core_errors.ErrConflict,
		)
	}

	for _, fileView := range view.Files {
		if err := fileView.File.Validate(); err != nil {
			return fmt.Errorf("validate cardimport file view: %w", err)
		}
		if fileView.File.SessionID != view.ID ||
			fileView.CardsCount < 0 ||
			fileView.ErrorsCount < 0 {
			return fmt.Errorf(
				"invalid cardimport file view: %w",
				core_errors.ErrConflict,
			)
		}
	}

	return nil
}
