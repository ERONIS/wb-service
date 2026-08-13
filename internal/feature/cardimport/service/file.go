package cardimport_service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

func (s *Service) ReserveFile(
	ctx context.Context,
	command ReserveFileCommand,
) (File, error) {
	command = command.normalized()
	if err := command.validate(); err != nil {
		return File{}, err
	}

	file, err := s.repository.ReserveFile(
		ctx,
		command,
		MaxFilesPerSession,
	)
	if err != nil {
		return File{}, fmt.Errorf("reserve cardimport file: %w", err)
	}
	if err := validateReservedFile(file, command); err != nil {
		return File{}, err
	}

	return file, nil
}

// StoreFile ограниченно читает файл и durable сохраняет bytes с SHA-256.
// Разбор XLSX начинается только на следующем этапе и всегда читает stored blob.
func (s *Service) StoreFile(
	ctx context.Context,
	command StoreFileCommand,
	source io.Reader,
) (File, error) {
	if err := command.validate(); err != nil {
		return File{}, err
	}
	if source == nil {
		return File{}, invalidFileArgument("source reader is nil")
	}

	content, err := readStoredContent(source)
	if err != nil {
		return File{}, err
	}

	file, err := s.repository.StoreFile(ctx, command, content)
	if err != nil {
		return File{}, fmt.Errorf("store cardimport file: %w", err)
	}
	if err := file.Validate(); err != nil {
		return File{}, fmt.Errorf("validate stored cardimport file: %w", err)
	}
	if file.ID != command.FileID || file.SessionID != command.SessionID {
		return File{}, fmt.Errorf(
			"stored cardimport file does not match command: %w",
			core_errors.ErrConflict,
		)
	}
	if file.Status != FileStatusStored {
		return File{}, fmt.Errorf(
			"cardimport file was not stored: %w",
			core_errors.ErrConflict,
		)
	}

	return file, nil
}

func validateReservedFile(
	file File,
	command ReserveFileCommand,
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

func readStoredContent(source io.Reader) (StoredContent, error) {
	var buffer bytes.Buffer

	written, err := io.Copy(
		&buffer,
		io.LimitReader(source, MaxFileSize+1),
	)
	if err != nil {
		return StoredContent{}, fmt.Errorf(
			"read cardimport file: %w",
			err,
		)
	}
	if written == 0 {
		return StoredContent{}, invalidFileArgument("file is empty")
	}
	if written > MaxFileSize {
		return StoredContent{}, fmt.Errorf(
			"actual file size exceeds limit '%d': %w",
			MaxFileSize,
			core_errors.ErrInvalidArgument,
		)
	}

	content := append([]byte(nil), buffer.Bytes()...)

	return StoredContent{
		Bytes:  content,
		SHA256: sha256.Sum256(content),
	}, nil
}

// ParseFile всегда разбирает durable blob из PostgreSQL, а не Telegram stream.
func (s *Service) ParseFile(
	ctx context.Context,
	command ParseFileCommand,
) (SessionView, error) {
	if err := command.validate(); err != nil {
		return SessionView{}, err
	}

	content, err := s.repository.ClaimFileForParsing(ctx, command)
	if err != nil {
		return SessionView{}, fmt.Errorf(
			"claim cardimport file for parsing: %w",
			err,
		)
	}

	parsed, parseErr := s.parser.Parse(
		command.FileID,
		bytes.NewReader(content),
	)
	if parseErr != nil {
		parsed = ParsedFile{
			FileID: command.FileID,
			Issues: []ParseIssue{{
				Severity: IssueSeverityError,
				Code:     "xlsx_parse_failed",
				Message: fmt.Sprintf(
					"Не удалось разобрать XLSX: %v.",
					parseErr,
				),
			}},
		}
	}

	result := AggregateParsedFile(parsed)
	file, err := s.repository.SaveParsedFile(ctx, command, result)
	if err != nil {
		return SessionView{}, fmt.Errorf(
			"save parsed cardimport file: %w",
			err,
		)
	}
	if err := file.Validate(); err != nil {
		return SessionView{}, fmt.Errorf(
			"validate parsed cardimport file: %w",
			err,
		)
	}

	expectedStatus := FileStatusValid
	if result.HasErrors() {
		expectedStatus = FileStatusInvalid
	}
	if file.Status != expectedStatus {
		return SessionView{}, fmt.Errorf(
			"unexpected parsed file status %q: %w",
			file.Status,
			core_errors.ErrConflict,
		)
	}

	return s.GetView(ctx, SessionCommand{
		AuthorTelegramID: command.AuthorTelegramID,
		SessionID:        command.SessionID,
	})
}
