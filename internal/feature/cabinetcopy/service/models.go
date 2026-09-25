package cabinetcopy_service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type SessionID int64
type CabinetID = domain.CabinetID

type Step string

const (
	StepSource     Step = "source"
	StepTarget     Step = "target"
	StepCount      Step = "count"
	StepCountInput Step = "count_input"
	StepTags       Step = "tags"
	StepReview     Step = "review"
	StepSubmitted  Step = "submitted"
	StepCancelled  Step = "cancelled"
)

func (step Step) IsValid() bool {
	switch step {
	case StepSource, StepTarget, StepCount, StepCountInput, StepTags,
		StepReview, StepSubmitted, StepCancelled:
		return true
	default:
		return false
	}
}

type Cabinet struct {
	ID   CabinetID
	Name string
}

type Tag struct {
	ID    int64
	Name  string
	Color string
}

type Session struct {
	ID               SessionID
	AuthorTelegramID int64
	Step             Step
	Revision         int64
	SourceCabinetID  CabinetID
	TargetCabinetID  CabinetID
	RequestedCount   int
	SelectedTagIDs   []int64
	SelectedTagNames []string
	PreparedCount    int
	BatchID          cardimport_service.BatchID
	TransferID       transfer_service.TransferID
	CreatedAt        time.Time
	UpdatedAt        time.Time
	SubmittedAt      *time.Time
}

func (session Session) Validate() error {
	if session.ID <= 0 || session.AuthorTelegramID <= 0 || !session.Step.IsValid() ||
		session.Revision < 0 || session.CreatedAt.IsZero() || session.UpdatedAt.IsZero() ||
		session.UpdatedAt.Before(session.CreatedAt) ||
		len(session.SelectedTagIDs) != len(session.SelectedTagNames) {
		return errors.New("cabinet copy session is invalid")
	}
	if session.SourceCabinetID != "" &&
		strings.TrimSpace(string(session.SourceCabinetID)) != string(session.SourceCabinetID) {
		return errors.New("cabinet copy source cabinet is invalid")
	}
	if session.TargetCabinetID != "" &&
		strings.TrimSpace(string(session.TargetCabinetID)) != string(session.TargetCabinetID) {
		return errors.New("cabinet copy target cabinet is invalid")
	}
	if session.SourceCabinetID != "" && session.SourceCabinetID == session.TargetCabinetID {
		return errors.New("cabinet copy source and target are equal")
	}
	if session.Step == StepSubmitted {
		if session.BatchID <= 0 || session.TransferID <= 0 || session.SubmittedAt == nil {
			return errors.New("submitted cabinet copy is incomplete")
		}
	} else if session.SubmittedAt != nil {
		return errors.New("active cabinet copy has submitted time")
	}
	return nil
}

type PreparedItem struct {
	Position int
	NMID     int64
	IMTID    int64
	Card     cardimport_service.AggregatedCard
}

func (item PreparedItem) Validate() error {
	if item.Position <= 0 || item.NMID <= 0 || item.IMTID < 0 ||
		strings.TrimSpace(item.Card.Variant.VendorCode) == "" {
		return fmt.Errorf("prepared cabinet copy item is invalid")
	}
	return nil
}

type Submission struct {
	Session  Session
	Batch    cardimport_service.BatchHeader
	Transfer transfer_service.TransferID
}
