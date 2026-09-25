package workflow

import (
	"fmt"
	"strings"

	"github.com/ERONIS/wb-service/internal/core/canonicalhash"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

const MaxFinalizeIdempotencyKeyLength = 128

type BeginCommand struct {
	AuthorTelegramID int64
	Purpose          Purpose
}

func (c BeginCommand) validate() error {
	if c.AuthorTelegramID <= 0 {
		return invalidTelegramID(c.AuthorTelegramID)
	}
	if !c.Purpose.IsValid() {
		return fmt.Errorf(
			"invalid cardimport purpose '%s': %w",
			c.Purpose,
			core_errors.ErrInvalidArgument,
		)
	}

	return nil
}

type SessionCommand struct {
	AuthorTelegramID int64
	SessionID        SessionID
}

func (c SessionCommand) validate() error {
	if c.AuthorTelegramID <= 0 {
		return invalidTelegramID(c.AuthorTelegramID)
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

// ContinueCommand confirms that the user wants to keep importing all valid
// cards while skipping rows and cards that caused blocking errors.
type ContinueCommand struct {
	AuthorTelegramID int64
	SessionID        SessionID
	ExpectedRevision int64
}

func (c ContinueCommand) validate() error {
	if err := (SessionCommand{
		AuthorTelegramID: c.AuthorTelegramID,
		SessionID:        c.SessionID,
	}).validate(); err != nil {
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

type ReserveFileCommand struct {
	AuthorTelegramID     int64
	SessionID            SessionID
	TelegramFileID       string
	TelegramFileUniqueID string
	TelegramMessageID    int64
	OriginalFilename     string
	MIMEType             string
	DeclaredSize         int64
}

func (c ReserveFileCommand) normalized() ReserveFileCommand {
	c.TelegramFileID = strings.TrimSpace(c.TelegramFileID)
	c.TelegramFileUniqueID = strings.TrimSpace(c.TelegramFileUniqueID)
	c.OriginalFilename = strings.TrimSpace(c.OriginalFilename)
	c.MIMEType = strings.TrimSpace(c.MIMEType)

	return c
}

func (c ReserveFileCommand) validate() error {
	if err := (SessionCommand{
		AuthorTelegramID: c.AuthorTelegramID,
		SessionID:        c.SessionID,
	}).validate(); err != nil {
		return err
	}

	switch {
	case c.TelegramFileID == "":
		return invalidFileArgument("Telegram file ID is empty")
	case c.TelegramFileUniqueID == "":
		return invalidFileArgument("Telegram unique file ID is empty")
	case c.TelegramMessageID <= 0:
		return invalidFileArgument("Telegram message ID must be positive")
	case c.OriginalFilename == "":
		return invalidFileArgument("original filename is empty")
	case c.DeclaredSize <= 0:
		return invalidFileArgument("declared size must be positive")
	case c.DeclaredSize > MaxFileSize:
		return fmt.Errorf(
			"declared file size '%d' exceeds limit '%d': %w",
			c.DeclaredSize,
			MaxFileSize,
			core_errors.ErrInvalidArgument,
		)
	default:
		return nil
	}
}

type StoreFileCommand struct {
	AuthorTelegramID int64
	SessionID        SessionID
	FileID           FileID
}

type ParseFileCommand struct {
	AuthorTelegramID int64
	SessionID        SessionID
	FileID           FileID
}

type FinalizeCommand struct {
	SessionID        SessionID
	ExpectedRevision int64
	IdempotencyKey   string
}

func (c FinalizeCommand) normalized() FinalizeCommand {
	c.IdempotencyKey = strings.TrimSpace(c.IdempotencyKey)
	return c
}

func (c FinalizeCommand) validate() error {
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

func (c FinalizeCommand) digest() Digest {
	builder := canonicalhash.NewSHA256("cardimport-finalize-command:v1")
	builder.Int64(int64(c.SessionID))
	builder.Int64(c.ExpectedRevision)
	builder.String(c.IdempotencyKey)
	return Digest(builder.Sum())
}

func (c ParseFileCommand) validate() error {
	return (StoreFileCommand{
		AuthorTelegramID: c.AuthorTelegramID,
		SessionID:        c.SessionID,
		FileID:           c.FileID,
	}).validate()
}

func (c StoreFileCommand) validate() error {
	if err := (SessionCommand{
		AuthorTelegramID: c.AuthorTelegramID,
		SessionID:        c.SessionID,
	}).validate(); err != nil {
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

func invalidTelegramID(telegramID int64) error {
	return fmt.Errorf(
		"invalid author Telegram ID '%d': %w",
		telegramID,
		core_errors.ErrInvalidArgument,
	)
}

func invalidFileArgument(message string) error {
	return fmt.Errorf(
		"invalid cardimport file argument: %s: %w",
		message,
		core_errors.ErrInvalidArgument,
	)
}
