package domain

import (
	"fmt"
	"strings"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

type CardImportPurpose string

const (
	CardImportPurposeTransfer CardImportPurpose = "transfer"
	CardImportPurposeEdit     CardImportPurpose = "edit"
)

func (p CardImportPurpose) IsValid() bool {
	return p == CardImportPurposeTransfer ||
		p == CardImportPurposeEdit
}

type CardImportStatus string

const (
	CardImportStatusReceived CardImportStatus = "received"
	CardImportStatusReady    CardImportStatus = "ready"
	CardImportStatusError    CardImportStatus = "error"
)

func (s CardImportStatus) IsValid() bool {
	return s == CardImportStatusReceived ||
		s == CardImportStatusReady ||
		s == CardImportStatusError

}

type CardImport struct {
	ID int64

	AuthorUserID     int64
	Purpose          CardImportPurpose
	OriginalFilename string
	TelegramFileID   string
	MIMEType         string

	Cards      []Card
	CardsCount int
	Status     CardImportStatus
	Error      string

	CreatedAt time.Time
	UpdatedAt time.Time
}

func CreateCardImport(
	authorUserID int64,
	purpose CardImportPurpose,
	filename string,
	telegramFileID string,
	mimeType string,
) (CardImport, error) {
	cardImport := CardImport{
		AuthorUserID:     authorUserID,
		Purpose:          purpose,
		OriginalFilename: strings.TrimSpace(filename),
		TelegramFileID:   strings.TrimSpace(telegramFileID),
		MIMEType:         strings.TrimSpace(mimeType),
		Cards:            make([]Card, 0),
		Status:           CardImportStatusReceived,
	}

	if err := cardImport.Validate(); err != nil {
		return CardImport{}, err
	}

	return cardImport, nil
}

func (i CardImport) Validate() error {
	switch {
	case i.AuthorUserID <= 0:
		return invalidCardImport("author user ID must be positive")

	case !i.Purpose.IsValid():
		return invalidCardImport("unknown import purpose")

	case strings.TrimSpace(i.OriginalFilename) == "":
		return invalidCardImport("filename is empty")

	case strings.TrimSpace(i.TelegramFileID) == "":
		return invalidCardImport("Telegram file ID is empty")

	case strings.TrimSpace(i.MIMEType) == "":
		return invalidCardImport("MIME type is empty")

	case !i.Status.IsValid():
		return invalidCardImport("unknown import status")

	case i.Cards == nil:
		return invalidCardImport("cards array is nil")

	case i.CardsCount != len(i.Cards):
		return invalidCardImport("cards count does not match payload")
	}

	switch i.Status {
	case CardImportStatusReceived:
		if len(i.Cards) != 0 || i.Error != "" {
			return invalidCardImport(
				"received import contains result",
			)
		}

	case CardImportStatusReady:
		if len(i.Cards) == 0 || i.Error != "" {
			return invalidCardImport(
				"ready import has invalid result",
			)
		}

	case CardImportStatusError:
		if len(i.Cards) != 0 ||
			strings.TrimSpace(i.Error) == "" {
			return invalidCardImport(
				"failed import has invalid result",
			)
		}
	}

	return nil
}
func invalidCardImport(message string) error {
	return fmt.Errorf(
		"invalid card import: %s: %w",
		message,
		core_errors.ErrInvalidArgument,
	)
}
func (i *CardImport) MarkReady(cards []Card) error {
	if i.Status != CardImportStatusReceived {
		return core_errors.ErrConflict
	}
	if len(cards) == 0 {
		return invalidCardImport("parsed cards are empty")
	}
	
	i.Cards = append([]Card(nil), cards...)
	i.CardsCount = len(cards)
	i.Status = CardImportStatusReady
	i.Error = ""

	return i.Validate()
}

func (i *CardImport) MarkError(cause error) error {
	if i.Status != CardImportStatusReceived {
		return core_errors.ErrConflict
	}
	if cause == nil || strings.TrimSpace(cause.Error()) == "" {
		return invalidCardImport("import error is empty")
	}

	i.Cards = make([]Card, 0)
	i.CardsCount = 0
	i.Status = CardImportStatusError
	i.Error = strings.TrimSpace(cause.Error())

	return i.Validate()
}
