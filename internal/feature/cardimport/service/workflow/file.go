package cardimport_workflow

import (
	"bytes"
	"context"
	"fmt"
	"io"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	cardimport_intake "github.com/ERONIS/wb-service/internal/feature/cardimport/service/intake"
	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"
)

// ReserveFile добавляет метаданные файла в собираемую сессию.
func (s *Service) ReserveFile(
	ctx context.Context,
	command cardimport_contract.ReserveFileCommand,
) (cardimport_model.File, error) {
	command = command.Normalized()
	if err := command.Validate(); err != nil {
		return cardimport_model.File{}, err
	}

	file, err := s.sessions.Reserve(ctx, command)
	if err != nil {
		return cardimport_model.File{}, fmt.Errorf("reserve cardimport file: %w", err)
	}
	if err := validateReservedFile(file, command); err != nil {
		return cardimport_model.File{}, err
	}

	return file, nil
}

// StoreFile считывает файл в память и отмечает его сохранённым.
func (s *Service) StoreFile(
	ctx context.Context,
	command cardimport_contract.StoreFileCommand,
	source io.Reader,
) (cardimport_model.File, error) {
	if err := command.Validate(); err != nil {
		return cardimport_model.File{}, err
	}
	if source == nil {
		return cardimport_model.File{}, cardimport_contract.InvalidFileArgument("source reader is nil")
	}

	content, err := readStoredContent(source)
	if err != nil {
		return cardimport_model.File{}, err
	}

	file, err := s.sessions.StoreFile(ctx, command, content)
	if err != nil {
		return cardimport_model.File{}, fmt.Errorf("store cardimport file: %w", err)
	}
	if err := file.Validate(); err != nil {
		return cardimport_model.File{}, fmt.Errorf("validate stored cardimport file: %w", err)
	}
	if file.ID != command.FileID || file.SessionID != command.SessionID {
		return cardimport_model.File{}, fmt.Errorf(
			"stored cardimport file does not match command: %w",
			core_errors.ErrConflict,
		)
	}
	if file.Status != cardimport_model.FileStatusStored {
		return cardimport_model.File{}, fmt.Errorf(
			"cardimport file was not stored: %w",
			core_errors.ErrConflict,
		)
	}

	return file, nil
}

// ParseFile разбирает XLSX и сохраняет карточки с замечаниями.
func (s *Service) ParseFile(
	ctx context.Context,
	command cardimport_contract.ParseFileCommand,
) (cardimport_model.SessionView, error) {
	if err := command.Validate(); err != nil {
		return cardimport_model.SessionView{}, err
	}

	content, err := s.sessions.Claim(ctx, command)
	if err != nil {
		return cardimport_model.SessionView{}, fmt.Errorf(
			"claim cardimport file for parsing: %w",
			err,
		)
	}

	parsed, parseErr := s.parser.Parse(
		command.FileID,
		bytes.NewReader(content),
	)
	if parseErr != nil {
		parsed = cardimport_model.ParsedFile{
			FileID: command.FileID,
			Issues: []cardimport_model.ParseIssue{{
				Severity: cardimport_model.IssueSeverityError,
				Code:     "xlsx_parse_failed",
				Message: fmt.Sprintf(
					"Не удалось разобрать XLSX: %v.",
					parseErr,
				),
			}},
		}
	}

	result := cardimport_intake.AggregateParsedFile(parsed)
	file, err := s.sessions.SaveParsed(ctx, command, result)
	if err != nil {
		return cardimport_model.SessionView{}, fmt.Errorf(
			"save parsed cardimport file: %w",
			err,
		)
	}
	if err := file.Validate(); err != nil {
		return cardimport_model.SessionView{}, fmt.Errorf(
			"validate parsed cardimport file: %w",
			err,
		)
	}

	expectedStatus := cardimport_model.FileStatusValid
	if result.HasErrors() {
		expectedStatus = cardimport_model.FileStatusInvalid
	}
	if file.Status != expectedStatus {
		return cardimport_model.SessionView{}, fmt.Errorf(
			"unexpected parsed file status %q: %w",
			file.Status,
			core_errors.ErrConflict,
		)
	}

	return s.GetView(ctx, cardimport_contract.SessionCommand{
		AuthorTelegramID: command.AuthorTelegramID,
		SessionID:        command.SessionID,
	})
}

// validateReservedFile сверяет зарезервированный файл с входной командой.
func validateReservedFile(
	file cardimport_model.File,
	command cardimport_contract.ReserveFileCommand,
) error {
	if err := file.Validate(); err != nil {
		return fmt.Errorf("validate reserved cardimport file: %w", err)
	}
	if file.SessionID != command.SessionID ||
		file.TelegramFileID != command.TelegramFileID ||
		file.TelegramFileUniqueID != command.TelegramFileUniqueID ||
		file.TelegramMessageID != command.TelegramMessageID ||
		file.OriginalFilename != command.OriginalFilename ||
		file.MIMEType != command.MIMEType ||
		file.DeclaredSize != command.DeclaredSize {
		return fmt.Errorf(
			"reserved cardimport file metadata conflict: %w",
			core_errors.ErrConflict,
		)
	}

	return nil
}

// readStoredContent ограниченно считывает байты файла.
func readStoredContent(source io.Reader) ([]byte, error) {
	var buffer bytes.Buffer

	written, err := io.Copy(
		&buffer,
		io.LimitReader(source, cardimport_model.MaxFileSize+1),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"read cardimport file: %w",
			err,
		)
	}
	if written == 0 {
		return nil, cardimport_contract.InvalidFileArgument("file is empty")
	}
	if written > cardimport_model.MaxFileSize {
		return nil, fmt.Errorf(
			"actual file size exceeds limit '%d': %w",
			cardimport_model.MaxFileSize,
			core_errors.ErrInvalidArgument,
		)
	}

	return append([]byte(nil), buffer.Bytes()...), nil
}
