package cardimport_service

import (
	"fmt"
	"strings"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

const (
	MaxFilesPerSession = 10
	MaxFileSize        = 20 << 20
)

type SessionID int64

type Purpose string

const (
	PurposeTransfer Purpose = "transfer"
	PurposeEdit     Purpose = "edit"
)

func (p Purpose) IsValid() bool {
	switch p {
	case PurposeTransfer, PurposeEdit:
		return true
	default:
		return false
	}
}

type SessionStatus string

const (
	SessionStatusCollecting SessionStatus = "collecting"
	SessionStatusFinalized  SessionStatus = "finalized"
	SessionStatusCancelled  SessionStatus = "cancelled"
)

func (s SessionStatus) IsValid() bool {
	switch s {
	case SessionStatusCollecting,
		SessionStatusFinalized,
		SessionStatusCancelled:
		return true
	default:
		return false
	}
}

// Session представляет сохранённую сессию приёма XLSX-файлов.
type Session struct {
	ID               SessionID
	AuthorTelegramID int64
	Purpose          Purpose
	Status           SessionStatus
	Revision         int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (s Session) Validate() error {
	switch {
	case s.ID <= 0:
		return invalidSession("session ID must be positive")
	case s.AuthorTelegramID <= 0:
		return invalidSession("author Telegram ID must be positive")
	case !s.Purpose.IsValid():
		return invalidSession("unknown purpose")
	case !s.Status.IsValid():
		return invalidSession("unknown status")
	case s.Revision < 0:
		return invalidSession("revision must not be negative")
	case s.CreatedAt.IsZero():
		return invalidSession("created time is empty")
	case s.UpdatedAt.IsZero():
		return invalidSession("updated time is empty")
	case s.UpdatedAt.Before(s.CreatedAt):
		return invalidSession("updated time precedes created time")
	default:
		return nil
	}
}

func invalidSession(message string) error {
	return fmt.Errorf(
		"invalid cardimport session: %s: %w",
		strings.TrimSpace(message),
		core_errors.ErrInvalidArgument,
	)
}

type FileID int64

type FileStatus string

const (
	FileStatusReserved  FileStatus = "reserved"
	FileStatusStored    FileStatus = "stored"
	FileStatusParsing   FileStatus = "parsing"
	FileStatusValid     FileStatus = "valid"
	FileStatusInvalid   FileStatus = "invalid"
	FileStatusAbandoned FileStatus = "abandoned"
)

func (s FileStatus) IsValid() bool {
	switch s {
	case FileStatusReserved,
		FileStatusStored,
		FileStatusParsing,
		FileStatusValid,
		FileStatusInvalid,
		FileStatusAbandoned:
		return true
	default:
		return false
	}
}

// File представляет durable-состояние одного Telegram-файла.
type File struct {
	ID                   FileID
	SessionID            SessionID
	TelegramFileID       string
	TelegramFileUniqueID string
	TelegramMessageID    int64
	OriginalFilename     string
	MIMEType             string
	DeclaredSize         int64
	StoredSize           int64
	SHA256               string
	Status               FileStatus
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func (f File) Validate() error {
	switch {
	case f.ID <= 0:
		return invalidFile("file ID must be positive")
	case f.SessionID <= 0:
		return invalidFile("session ID must be positive")
	case strings.TrimSpace(f.TelegramFileID) == "":
		return invalidFile("Telegram file ID is empty")
	case strings.TrimSpace(f.TelegramFileUniqueID) == "":
		return invalidFile("Telegram unique file ID is empty")
	case f.TelegramMessageID <= 0:
		return invalidFile("Telegram message ID must be positive")
	case strings.TrimSpace(f.OriginalFilename) == "":
		return invalidFile("original filename is empty")
	case f.DeclaredSize <= 0 || f.DeclaredSize > MaxFileSize:
		return invalidFile("declared size is outside allowed bounds")
	case !f.Status.IsValid():
		return invalidFile("unknown status")
	case f.CreatedAt.IsZero():
		return invalidFile("created time is empty")
	case f.UpdatedAt.IsZero():
		return invalidFile("updated time is empty")
	case f.UpdatedAt.Before(f.CreatedAt):
		return invalidFile("updated time precedes created time")
	}

	switch f.Status {
	case FileStatusReserved:
		if f.StoredSize != 0 || f.SHA256 != "" {
			return invalidFile("reserved file contains stored metadata")
		}
	case FileStatusStored,
		FileStatusParsing,
		FileStatusValid,
		FileStatusInvalid:
		if f.StoredSize <= 0 || f.StoredSize > MaxFileSize {
			return invalidFile("stored size is outside allowed bounds")
		}
		if len(f.SHA256) != 64 {
			return invalidFile("stored file SHA-256 is invalid")
		}
	}

	return nil
}

type FileView struct {
	File
	CardsCount  int
	ErrorsCount int
}

type SessionView struct {
	Session
	Files       []FileView
	Issues      []IssueView
	CardsCount  int
	ErrorsCount int
	Ready       bool
}

type IssueView struct {
	FileID   FileID
	Filename string
	Issue    ParseIssue
}

func invalidFile(message string) error {
	return fmt.Errorf(
		"invalid cardimport file: %s: %w",
		strings.TrimSpace(message),
		core_errors.ErrInvalidArgument,
	)
}
