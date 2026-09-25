package cabinetcopy_service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	"github.com/ERONIS/wb-service/internal/core/observability"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	"go.uber.org/zap"
)

var ErrNotEnoughCards = errors.New("not enough cards match cabinet copy filter")

type Service struct {
	repository Repository
	reader     Reader
	batches    BatchWriter
	transfers  TransferStarter
	uow        core_postgres_transaction.UnitOfWork
	maxCards   int
	logger     *zap.Logger
}

func New(
	repository Repository,
	reader Reader,
	batches BatchWriter,
	transfers TransferStarter,
	uow core_postgres_transaction.UnitOfWork,
	maxCards int,
	loggers ...*zap.Logger,
) *Service {
	if repository == nil || reader == nil || batches == nil || transfers == nil || uow == nil {
		panic("cabinet copy service dependency is nil")
	}
	if maxCards <= 0 {
		panic("cabinet copy max cards must be positive")
	}
	return &Service{
		repository: repository,
		reader:     reader,
		batches:    batches,
		transfers:  transfers,
		uow:        uow,
		maxCards:   maxCards,
		logger:     observability.Logger(loggers...),
	}
}

func (service *Service) MaxCards() int { return service.maxCards }

func (service *Service) Cabinets() []Cabinet {
	cabinets := service.reader.Cabinets()
	return sortedCabinets(cabinets)
}

func (service *Service) CabinetsForOwner(
	ctx context.Context,
	ownerTelegramID int64,
) ([]Cabinet, error) {
	if ctx == nil || ownerTelegramID <= 0 {
		return nil, core_errors.ErrInvalidArgument
	}
	ownerReader, ok := service.reader.(OwnerCabinetReader)
	if !ok {
		return service.Cabinets(), nil
	}
	cabinets, err := ownerReader.CabinetsForOwner(ctx, ownerTelegramID)
	if err != nil {
		return nil, err
	}
	return sortedCabinets(cabinets), nil
}

func sortedCabinets(cabinets []Cabinet) []Cabinet {
	result := append([]Cabinet(nil), cabinets...)
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result
}

func (service *Service) Begin(ctx context.Context, authorTelegramID int64) (Session, error) {
	if authorTelegramID <= 0 {
		return Session{}, core_errors.ErrInvalidArgument
	}
	return service.repository.GetOrCreate(ctx, authorTelegramID)
}

func (service *Service) Get(ctx context.Context, authorTelegramID int64, sessionID SessionID) (Session, error) {
	if authorTelegramID <= 0 || sessionID <= 0 {
		return Session{}, core_errors.ErrInvalidArgument
	}
	return service.repository.Get(ctx, authorTelegramID, sessionID)
}

func (service *Service) GetActive(ctx context.Context, authorTelegramID int64) (Session, error) {
	if authorTelegramID <= 0 {
		return Session{}, core_errors.ErrInvalidArgument
	}
	return service.repository.GetActive(ctx, authorTelegramID)
}

func (service *Service) SelectSource(
	ctx context.Context,
	authorTelegramID int64,
	sessionID SessionID,
	revision int64,
	cabinetID CabinetID,
) (Session, error) {
	available, err := service.hasCabinet(ctx, authorTelegramID, cabinetID)
	if err != nil {
		return Session{}, err
	}
	if !available {
		return Session{}, fmt.Errorf("source cabinet %q is unavailable: %w", cabinetID, core_errors.ErrInvalidArgument)
	}
	return service.repository.SetSource(ctx, authorTelegramID, sessionID, revision, cabinetID)
}

func (service *Service) SelectTarget(
	ctx context.Context,
	authorTelegramID int64,
	sessionID SessionID,
	revision int64,
	cabinetID CabinetID,
) (Session, error) {
	available, err := service.hasCabinet(ctx, authorTelegramID, cabinetID)
	if err != nil {
		return Session{}, err
	}
	if !available {
		return Session{}, fmt.Errorf("target cabinet %q is unavailable: %w", cabinetID, core_errors.ErrInvalidArgument)
	}
	session, err := service.repository.Get(ctx, authorTelegramID, sessionID)
	if err != nil {
		return Session{}, err
	}
	if session.Revision != revision || session.SourceCabinetID == cabinetID {
		return Session{}, core_errors.ErrConflict
	}
	return service.repository.SetTarget(ctx, authorTelegramID, sessionID, revision, cabinetID)
}

func (service *Service) RequestCountInput(
	ctx context.Context,
	authorTelegramID int64,
	sessionID SessionID,
	revision int64,
) (Session, error) {
	return service.repository.RequestCountInput(ctx, authorTelegramID, sessionID, revision)
}

func (service *Service) SelectCount(
	ctx context.Context,
	authorTelegramID int64,
	sessionID SessionID,
	revision int64,
	count int,
) (Session, error) {
	if count <= 0 || count > service.maxCards {
		return Session{}, fmt.Errorf("card count must be between 1 and %d: %w", service.maxCards, core_errors.ErrInvalidArgument)
	}
	return service.repository.SetCount(ctx, authorTelegramID, sessionID, revision, count)
}

func (service *Service) ListTags(
	ctx context.Context,
	authorTelegramID int64,
	sessionID SessionID,
) ([]Tag, error) {
	session, err := service.repository.Get(ctx, authorTelegramID, sessionID)
	if err != nil {
		return nil, err
	}
	if session.SourceCabinetID == "" {
		return nil, core_errors.ErrConflict
	}
	return service.reader.ListTags(ctx, session.SourceCabinetID)
}

func (service *Service) ToggleTag(
	ctx context.Context,
	authorTelegramID int64,
	sessionID SessionID,
	revision int64,
	tagID int64,
) (Session, error) {
	if tagID <= 0 {
		return Session{}, core_errors.ErrInvalidArgument
	}
	session, err := service.repository.Get(ctx, authorTelegramID, sessionID)
	if err != nil {
		return Session{}, err
	}
	if session.Revision != revision || session.Step != StepTags {
		return Session{}, core_errors.ErrConflict
	}
	tags, err := service.reader.ListTags(ctx, session.SourceCabinetID)
	if err != nil {
		return Session{}, err
	}
	available := make(map[int64]Tag, len(tags))
	for _, tag := range tags {
		available[tag.ID] = tag
	}
	selected := make(map[int64]Tag, len(session.SelectedTagIDs)+1)
	for index, selectedID := range session.SelectedTagIDs {
		name := ""
		if index < len(session.SelectedTagNames) {
			name = session.SelectedTagNames[index]
		}
		selected[selectedID] = Tag{ID: selectedID, Name: name}
	}
	if _, exists := selected[tagID]; exists {
		delete(selected, tagID)
	} else {
		tag, exists := available[tagID]
		if !exists {
			return Session{}, core_errors.ErrInvalidArgument
		}
		selected[tagID] = tag
	}
	result := make([]Tag, 0, len(selected))
	for _, tag := range selected {
		result = append(result, tag)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return service.repository.SetTags(ctx, authorTelegramID, sessionID, revision, result)
}

func (service *Service) Prepare(
	ctx context.Context,
	authorTelegramID int64,
	sessionID SessionID,
	revision int64,
) (prepared Session, err error) {
	startedAt := time.Now()
	var readDuration, persistDuration time.Duration
	itemsCount := 0
	logger := service.logger.With(zap.Int64("copy_session_id", int64(sessionID)))
	observability.LogStarted(logger, "cabinetcopy", "prepare")
	defer func() {
		observability.LogTiming(logger, "cabinetcopy", "prepare", startedAt, err,
			zap.Int("items_count", itemsCount), zap.Duration("read_cards_duration", readDuration),
			zap.Duration("persist_duration", persistDuration))
	}()
	session, err := service.repository.Get(ctx, authorTelegramID, sessionID)
	if err != nil {
		return Session{}, err
	}
	if session.Step != StepTags || session.Revision != revision || session.RequestedCount <= 0 {
		return Session{}, core_errors.ErrConflict
	}
	logger = logger.With(zap.String("source_cabinet_id", string(session.SourceCabinetID)),
		zap.String("target_cabinet_id", string(session.TargetCabinetID)), zap.Int("requested_count", session.RequestedCount))
	stepStartedAt := time.Now()
	items, err := service.reader.ReadCards(
		ctx,
		session.SourceCabinetID,
		append([]int64(nil), session.SelectedTagIDs...),
		session.RequestedCount,
	)
	readDuration = time.Since(stepStartedAt)
	itemsCount = len(items)
	if err != nil {
		return Session{}, err
	}
	if len(items) != session.RequestedCount {
		return Session{}, fmt.Errorf("found %d of %d requested cards: %w", len(items), session.RequestedCount, ErrNotEnoughCards)
	}
	stepStartedAt = time.Now()
	err = service.uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		var saveErr error
		prepared, saveErr = service.repository.SavePrepared(
			ctx, tx, authorTelegramID, sessionID, revision, items,
		)
		return saveErr
	})
	persistDuration = time.Since(stepStartedAt)
	if err != nil {
		return Session{}, fmt.Errorf("save prepared cabinet copy: %w", err)
	}
	return prepared, nil
}

func (service *Service) Submit(
	ctx context.Context,
	actor cardimport_service.TrustedActor,
	sessionID SessionID,
	expectedRevision int64,
) (submission Submission, err error) {
	startedAt := time.Now()
	var batchDuration, startTransferDuration time.Duration
	logger := service.logger.With(zap.Int64("copy_session_id", int64(sessionID)))
	observability.LogStarted(logger, "cabinetcopy", "submit")
	defer func() {
		observability.LogTiming(logger, "cabinetcopy", "submit", startedAt, err,
			zap.Duration("create_batch_duration", batchDuration),
			zap.Duration("start_transfer_duration", startTransferDuration))
	}()
	session, err := service.repository.Get(ctx, actor.TelegramUserID, sessionID)
	if err != nil {
		return Submission{}, err
	}
	if session.Step == StepSubmitted {
		logger = logger.With(zap.Int64("batch_id", int64(session.BatchID)), zap.Int64("transfer_id", int64(session.TransferID)), zap.Bool("reused", true))
		batch, batchErr := service.batches.GetBatch(ctx, session.BatchID)
		if batchErr != nil {
			return Submission{}, batchErr
		}
		return Submission{Session: session, Batch: batch, Transfer: session.TransferID}, nil
	}
	if session.Step != StepReview || session.Revision != expectedRevision {
		return Submission{}, core_errors.ErrConflict
	}
	items, err := service.repository.ListPrepared(ctx, actor.TelegramUserID, sessionID)
	if err != nil {
		return Submission{}, err
	}
	if len(items) != session.PreparedCount || len(items) == 0 {
		return Submission{}, core_errors.ErrConflict
	}
	cards := make([]cardimport_service.AggregatedCard, len(items))
	for index := range items {
		cards[index] = items[index].Card
	}
	stepStartedAt := time.Now()
	batch, err := service.batches.CreateExternalBatch(ctx, actor, cardimport_service.CreateExternalBatchCommand{
		SourceKind:        cardimport_service.BatchSourceWBCabinet,
		SourceReferenceID: int64(session.ID),
		Cards:             cards,
	})
	batchDuration = time.Since(stepStartedAt)
	if err != nil {
		return Submission{}, fmt.Errorf("create cabinet copy batch: %w", err)
	}
	logger = logger.With(zap.Int64("batch_id", int64(batch.ID)), zap.Int("items_count", len(items)))
	if session.BatchID == 0 {
		session, err = service.repository.AttachBatch(
			ctx, actor.TelegramUserID, sessionID, expectedRevision, batch.ID,
		)
		if err != nil {
			return Submission{}, err
		}
	} else if session.BatchID != batch.ID {
		return Submission{}, core_errors.ErrConflict
	}
	stepStartedAt = time.Now()
	transferID, err := service.transfers.StartToCabinet(
		ctx,
		batch.ID,
		transfer_service.CabinetID(session.TargetCabinetID),
	)
	startTransferDuration = time.Since(stepStartedAt)
	if err != nil {
		return Submission{}, fmt.Errorf("start cabinet copy transfer: %w", err)
	}
	logger = logger.With(zap.Int64("transfer_id", int64(transferID)))
	session, err = service.repository.MarkSubmitted(
		ctx, actor.TelegramUserID, sessionID, batch.ID, transferID,
	)
	if err != nil {
		return Submission{}, err
	}
	return Submission{Session: session, Batch: batch, Transfer: transferID}, nil
}

func (service *Service) Cancel(
	ctx context.Context,
	authorTelegramID int64,
	sessionID SessionID,
	expectedRevision int64,
) (Session, error) {
	return service.repository.Cancel(ctx, authorTelegramID, sessionID, expectedRevision)
}

func (service *Service) hasCabinet(
	ctx context.Context,
	ownerTelegramID int64,
	cabinetID CabinetID,
) (bool, error) {
	if strings.TrimSpace(string(cabinetID)) != string(cabinetID) || cabinetID == "" {
		return false, nil
	}
	cabinets, err := service.CabinetsForOwner(ctx, ownerTelegramID)
	if err != nil {
		return false, err
	}
	for _, cabinet := range cabinets {
		if cabinet.ID == cabinetID {
			return true, nil
		}
	}
	return false, nil
}
