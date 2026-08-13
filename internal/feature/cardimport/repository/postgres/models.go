package cardimport_postgres_repository

import (
	"encoding/hex"
	"time"

	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

type rowScanner interface {
	Scan(dest ...any) error
}

type sessionModel struct {
	ID               int64
	AuthorTelegramID int64
	Purpose          cardimport_service.Purpose
	Status           cardimport_service.SessionStatus
	Revision         int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (m *sessionModel) Scan(row rowScanner) error {
	return row.Scan(
		&m.ID,
		&m.AuthorTelegramID,
		&m.Purpose,
		&m.Status,
		&m.Revision,
		&m.CreatedAt,
		&m.UpdatedAt,
	)
}

func (m sessionModel) domain() cardimport_service.Session {
	return cardimport_service.Session{
		ID:               cardimport_service.SessionID(m.ID),
		AuthorTelegramID: m.AuthorTelegramID,
		Purpose:          m.Purpose,
		Status:           m.Status,
		Revision:         m.Revision,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
	}
}

type fileModel struct {
	ID                   int64
	SessionID            int64
	TelegramFileID       string
	TelegramFileUniqueID string
	TelegramMessageID    int64
	OriginalFilename     string
	MIMEType             string
	DeclaredSize         int64
	StoredSize           int64
	SHA256               []byte
	Status               cardimport_service.FileStatus
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func (m *fileModel) Scan(row rowScanner) error {
	return row.Scan(
		&m.ID,
		&m.SessionID,
		&m.TelegramFileID,
		&m.TelegramFileUniqueID,
		&m.TelegramMessageID,
		&m.OriginalFilename,
		&m.MIMEType,
		&m.DeclaredSize,
		&m.StoredSize,
		&m.SHA256,
		&m.Status,
		&m.CreatedAt,
		&m.UpdatedAt,
	)
}

func (m fileModel) domain() cardimport_service.File {
	return cardimport_service.File{
		ID:                   cardimport_service.FileID(m.ID),
		SessionID:            cardimport_service.SessionID(m.SessionID),
		TelegramFileID:       m.TelegramFileID,
		TelegramFileUniqueID: m.TelegramFileUniqueID,
		TelegramMessageID:    m.TelegramMessageID,
		OriginalFilename:     m.OriginalFilename,
		MIMEType:             m.MIMEType,
		DeclaredSize:         m.DeclaredSize,
		StoredSize:           m.StoredSize,
		SHA256:               hex.EncodeToString(m.SHA256),
		Status:               m.Status,
		CreatedAt:            m.CreatedAt,
		UpdatedAt:            m.UpdatedAt,
	}
}
