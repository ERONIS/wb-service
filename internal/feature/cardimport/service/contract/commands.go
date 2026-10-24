package cardimport_contract

import (
	"fmt"
	"strings"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_intake "github.com/ERONIS/wb-service/internal/feature/cardimport/service/intake"
)

const MaxFinalizeIdempotencyKeyLength = 128

type BeginCommand struct {
	AuthorTelegramID int64
}

type SessionCommand struct {
	AuthorTelegramID int64
	SessionID        cardimport_intake.SessionID
}

type ContinueCommand struct {
	AuthorTelegramID int64
	SessionID        cardimport_intake.SessionID
	ExpectedRevision int64
}

type ReserveFileCommand struct {
	AuthorTelegramID     int64
	SessionID            cardimport_intake.SessionID
	TelegramFileID       string
	TelegramFileUniqueID string
	TelegramMessageID    int64
	OriginalFilename     string
	MIMEType             string
	DeclaredSize         int64
}

type StoreFileCommand struct {
	AuthorTelegramID int64
	SessionID        cardimport_intake.SessionID
	FileID           cardimport_intake.FileID
}

type ParseFileCommand struct {
	AuthorTelegramID int64
	SessionID        cardimport_intake.SessionID
	FileID           cardimport_intake.FileID
}

type FinalizeCommand struct {
	SessionID        cardimport_intake.SessionID
	ExpectedRevision int64
	IdempotencyKey   string
}

// Validate проверяет автора новой сессии.
func (c BeginCommand) Validate() error {
	if c.AuthorTelegramID <= 0 {
		return InvalidTelegramID(c.AuthorTelegramID)
	}
	return nil
}

// Normalized убирает пробелы по краям метаданных файла.
func (c ReserveFileCommand) Normalized() ReserveFileCommand {
	c.TelegramFileID = strings.TrimSpace(c.TelegramFileID)
	c.TelegramFileUniqueID = strings.TrimSpace(c.TelegramFileUniqueID)
	c.OriginalFilename = strings.TrimSpace(c.OriginalFilename)
	c.MIMEType = strings.TrimSpace(c.MIMEType)

	return c
}

// Validate проверяет метаданные файла перед резервированием.
func (c ReserveFileCommand) Validate() error {
	if err := (SessionCommand{
		AuthorTelegramID: c.AuthorTelegramID,
		SessionID:        c.SessionID,
	}).Validate(); err != nil {
		return err
	}

	switch {
	case c.TelegramFileID == "":
		return InvalidFileArgument("Telegram file ID is empty")
	case c.TelegramFileUniqueID == "":
		return InvalidFileArgument("Telegram unique file ID is empty")
	case c.TelegramMessageID <= 0:
		return InvalidFileArgument("Telegram message ID must be positive")
	case c.OriginalFilename == "":
		return InvalidFileArgument("original filename is empty")
	case c.DeclaredSize <= 0:
		return InvalidFileArgument("declared size must be positive")
	case c.DeclaredSize > cardimport_intake.MaxFileSize:
		return fmt.Errorf(
			"declared file size '%d' exceeds limit '%d': %w",
			c.DeclaredSize,
			cardimport_intake.MaxFileSize,
			core_errors.ErrInvalidArgument,
		)
	default:
		return nil
	}
}

// Validate проверяет владельца, сессию и ID файла.
func (c StoreFileCommand) Validate() error {
	if err := (SessionCommand{
		AuthorTelegramID: c.AuthorTelegramID,
		SessionID:        c.SessionID,
	}).Validate(); err != nil {
		return err
	}
	if c.FileID <= 0 {
		return fmt.Errorf(
			"invalid cardimport file ID '%d': %w",
			c.FileID,
			core_errors.ErrInvalidArgument,
		)
	}

	return nil
}

// Validate проверяет команду разбора сохранённого файла.
func (c ParseFileCommand) Validate() error {
	return (StoreFileCommand{
		AuthorTelegramID: c.AuthorTelegramID,
		SessionID:        c.SessionID,
		FileID:           c.FileID,
	}).Validate()
}

// Validate проверяет сессию и ожидаемую ревизию.
func (c ContinueCommand) Validate() error {
	if err := (SessionCommand{
		AuthorTelegramID: c.AuthorTelegramID,
		SessionID:        c.SessionID,
	}).Validate(); err != nil {
		return err
	}
	if c.ExpectedRevision < 0 {
		return fmt.Errorf(
			"invalid expected cardimport revision '%d': %w",
			c.ExpectedRevision,
			core_errors.ErrInvalidArgument,
		)
	}
	return nil
}

// Normalized нормализует ключ повторной финализации.
func (c FinalizeCommand) Normalized() FinalizeCommand {
	c.IdempotencyKey = strings.TrimSpace(c.IdempotencyKey)
	return c
}

// Validate проверяет данные и ключ финализации.
func (c FinalizeCommand) Validate() error {
	switch {
	case c.SessionID <= 0:
		return fmt.Errorf(
			"invalid cardimport session ID '%d': %w",
			c.SessionID,
			core_errors.ErrInvalidArgument,
		)
	case c.ExpectedRevision < 0:
		return fmt.Errorf(
			"invalid expected cardimport revision '%d': %w",
			c.ExpectedRevision,
			core_errors.ErrInvalidArgument,
		)
	case c.IdempotencyKey == "":
		return fmt.Errorf(
			"empty cardimport finalize idempotency key: %w",
			core_errors.ErrInvalidArgument,
		)
	case len(c.IdempotencyKey) > MaxFinalizeIdempotencyKeyLength:
		return fmt.Errorf(
			"cardimport finalize idempotency key is too long: %w",
			core_errors.ErrInvalidArgument,
		)
	default:
		return nil
	}
}

// Validate проверяет автора и ID существующей сессии.
func (c SessionCommand) Validate() error {
	if c.AuthorTelegramID <= 0 {
		return InvalidTelegramID(c.AuthorTelegramID)
	}
	if c.SessionID <= 0 {
		return fmt.Errorf(
			"invalid cardimport session ID '%d': %w",
			c.SessionID,
			core_errors.ErrInvalidArgument,
		)
	}

	return nil
}

// InvalidTelegramID создаёт ошибку некорректного ID автора.
func InvalidTelegramID(telegramID int64) error {
	return fmt.Errorf(
		"invalid author Telegram ID '%d': %w",
		telegramID,
		core_errors.ErrInvalidArgument,
	)
}

// InvalidFileArgument создаёт ошибку некорректных данных файла.
func InvalidFileArgument(message string) error {
	return fmt.Errorf(
		"invalid cardimport file argument: %s: %w",
		message,
		core_errors.ErrInvalidArgument,
	)
}
