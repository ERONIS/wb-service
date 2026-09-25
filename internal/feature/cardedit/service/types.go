package cardedit_service

import (
	"context"
	"errors"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

var (
	ErrAlreadyActive = errors.New("card edit is already active")
	ErrQueueFull     = errors.New("card edit queue is full")
	ErrCardNotFound  = errors.New("card was not found")
	ErrUnsafeUpdate  = errors.New("card cannot be updated safely")
)

type Cabinet struct {
	ID                 domain.CabinetID
	Name               string
	BindingRevision    int64
	CapabilityRevision int64
	ClientGeneration   domain.ClientGeneration
}

type Target struct {
	Cabinet Cabinet
	NMID    int64
}

type Gateway interface {
	Cabinets(context.Context, int64) ([]Cabinet, error)
	CardsList(
		context.Context,
		domain.CabinetID,
		contentapi.CardsListQuery,
		contentapi.CardsListRequest,
	) (contentapi.CardsListResponse, error)
	SubjectCharacteristics(
		context.Context,
		domain.CabinetID,
		int64,
		contentapi.SubjectCharacteristicsQuery,
	) (contentapi.SubjectCharacteristicsResponse, error)
	CardsErrorList(
		context.Context,
		domain.CabinetID,
		contentapi.CardsErrorListQuery,
		contentapi.CardsErrorListRequest,
	) (contentapi.CardsErrorListResponse, error)
	UpdateCards(
		context.Context,
		domain.CabinetID,
		domain.ClientGeneration,
		contentapi.UpdateCardsRequest,
	) (contentapi.UpdateCardsResponse, error)
	SaveMediaByLinks(
		context.Context,
		domain.CabinetID,
		domain.ClientGeneration,
		contentapi.SaveMediaByLinksRequest,
	) (contentapi.SaveMediaByLinksResponse, error)
}

type Job struct {
	OwnerTelegramID int64
	VendorCode      string
	Card            cardpipeline.Card
	Targets         []Target
}

type TargetResult struct {
	CabinetName string
	NMID        int64
	Confirmed   bool
	Unconfirmed bool
	WBErrors    []string
	Err         error
}

type Result struct {
	VendorCode string
	Targets    []TargetResult
}

type Notifier interface {
	NotifyEditResult(int64, Result) error
}

type Config struct {
	PollInterval time.Duration
	MaxAttempts  int
	Workers      int
	QueueSize    int
}

func (config Config) Validate() error {
	switch {
	case config.PollInterval <= 0 || config.PollInterval > time.Hour:
		return errors.New("card edit poll interval must be between zero and one hour")
	case config.MaxAttempts <= 0 || config.MaxAttempts > 120:
		return errors.New("card edit max attempts must be between 1 and 120")
	case config.Workers <= 0 || config.Workers > 20:
		return errors.New("card edit workers must be between 1 and 20")
	case config.QueueSize < config.Workers || config.QueueSize > 1000:
		return errors.New("card edit queue size must be between workers and 1000")
	default:
		return nil
	}
}
