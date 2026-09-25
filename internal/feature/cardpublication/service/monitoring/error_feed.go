package monitoring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	core_observability "github.com/ERONIS/wb-service/internal/core/observability"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	errorFeedOverlap  = 5 * time.Minute
	errorFeedInterval = 30 * time.Second
	maxErrorFeedPages = 1000
)

var ErrErrorFeedEnvelope = errors.New("WB Cards Error List returned an error envelope")

type ErrorCursor struct {
	CabinetID CabinetID
	UpdatedAt time.Time
	BatchUUID string
	Revision  int64
	PolledAt  time.Time
}

type ErrorBatchEvidence struct {
	CabinetID           CabinetID
	BatchUUID           string
	UpdatedAt           time.Time
	SourceDigest        Digest
	VendorCodes         []string
	RejectedVendorCodes []string
	ErrorCodes          []string
}

type SaveErrorFeedCommand struct {
	CabinetID CabinetID
	Previous  ErrorCursor
	Next      ErrorCursor
	Batches   []ErrorBatchEvidence
	PolledAt  time.Time
}

type CaptureErrorBaselineCommand struct {
	TransferID transfer_service.TransferID
	ActionID   int64
	CabinetID  CabinetID
}

type ErrorBaseline struct {
	TransferID      transfer_service.TransferID
	ActionID        int64
	CabinetID       CabinetID
	CursorRevision  int64
	CursorUpdatedAt time.Time
	CursorBatchUUID string
	CapturedAt      time.Time
}

func (baseline ErrorBaseline) Validate() error {
	if baseline.TransferID <= 0 || baseline.ActionID <= 0 || baseline.CabinetID == "" ||
		baseline.CursorRevision < 0 || baseline.CapturedAt.IsZero() ||
		(baseline.CursorUpdatedAt.IsZero() && baseline.CursorBatchUUID != "") {
		return errors.New("Cards Error List baseline is invalid")
	}
	return nil
}

type ErrorFeed struct {
	transport  CatalogTransport
	repository ErrorFeedRepository
	targets    ErrorTargetSource
	uow        core_postgres_transaction.UnitOfWork
	now        func() time.Time
	logger     *zap.Logger
	mu         sync.Mutex
	nextPoll   time.Time
}

func NewErrorFeed(
	transport CatalogTransport,
	repository ErrorFeedRepository,
	targets ErrorTargetSource,
	uow core_postgres_transaction.UnitOfWork,
	loggers ...*zap.Logger,
) *ErrorFeed {
	if transport == nil || repository == nil || targets == nil || uow == nil {
		panic("cardpublication error feed dependency is nil")
	}
	return &ErrorFeed{
		transport:  transport,
		repository: repository,
		targets:    targets,
		uow:        uow,
		now:        time.Now,
		logger:     core_observability.Logger(loggers...),
	}
}

func (feed *ErrorFeed) ProcessPending(ctx context.Context) (err error) {
	startedAt := time.Now()
	targetsCount := 0
	skipped := false
	var lockWait time.Duration
	defer func() {
		logTiming := core_observability.LogTiming
		if skipped {
			logTiming = core_observability.LogTimingDebug
		}
		logTiming(
			feed.logger,
			"cardpublication",
			"error_feed_poll",
			startedAt,
			err,
			zap.Duration("lock_wait_duration", lockWait),
			zap.Int("targets_count", targetsCount),
			zap.Bool("skipped", skipped),
		)
	}()
	if ctx == nil {
		return errors.New("process Cards Error List: context is nil")
	}
	lockStartedAt := time.Now()
	feed.mu.Lock()
	lockWait = time.Since(lockStartedAt)
	defer feed.mu.Unlock()
	now := feed.now().UTC()
	if !feed.nextPoll.IsZero() && now.Before(feed.nextPoll) {
		skipped = true
		return nil
	}
	feed.nextPoll = now.Add(errorFeedInterval)
	targets, err := feed.targets.PublicationTargets(ctx)
	if err != nil {
		return err
	}
	targetsCount = len(targets)
	group, refreshCtx := errgroup.WithContext(ctx)
	for _, target := range targets {
		target := target
		group.Go(func() error {
			_, err := feed.Refresh(refreshCtx, CabinetID(target.CabinetID))
			return err
		})
	}
	return group.Wait()
}

func (feed *ErrorFeed) Refresh(
	ctx context.Context,
	cabinetID CabinetID,
) (savedCursor ErrorCursor, err error) {
	startedAt := time.Now()
	var loadCursorDuration, requestsDuration, saveDuration time.Duration
	pagesCount, batchesCount := 0, 0
	defer func() {
		core_observability.LogTiming(
			feed.logger,
			"cardpublication",
			"refresh_error_feed",
			startedAt,
			err,
			zap.String("cabinet_id", string(cabinetID)),
			zap.Int("pages_count", pagesCount),
			zap.Int("batches_count", batchesCount),
			zap.Duration("load_cursor_duration", loadCursorDuration),
			zap.Duration("requests_duration", requestsDuration),
			zap.Duration("save_duration", saveDuration),
		)
	}()
	if ctx == nil || strings.TrimSpace(string(cabinetID)) != string(cabinetID) ||
		cabinetID == "" {
		return ErrorCursor{}, errors.New("Cards Error List cabinet is invalid")
	}
	stepStartedAt := time.Now()
	previous, err := feed.repository.LoadErrorCursor(ctx, cabinetID)
	loadCursorDuration = time.Since(stepStartedAt)
	if err != nil {
		return ErrorCursor{}, err
	}
	pollStartedAt := feed.now().UTC()
	requestCursor := contentapi.CardsErrorListCursor{
		Limit: contentapi.MaxCardsErrorListPageSize,
	}
	if !previous.UpdatedAt.IsZero() {
		requestCursor.UpdatedAt = previous.UpdatedAt.Add(-errorFeedOverlap).Format(time.RFC3339Nano)
	} else {
		requestCursor.UpdatedAt = pollStartedAt.Add(-errorFeedOverlap).Format(time.RFC3339Nano)
	}
	batches := make([]ErrorBatchEvidence, 0)
	next := previous
	for page := 0; page < maxErrorFeedPages; page++ {
		requestStartedAt := time.Now()
		response, err := feed.transport.CardsErrorList(
			ctx,
			cabinetID,
			contentapi.CardsErrorListQuery{},
			contentapi.CardsErrorListRequest{
				Cursor: requestCursor,
				Order:  contentapi.CardsErrorListOrder{Ascending: true},
			},
		)
		requestsDuration += time.Since(requestStartedAt)
		pagesCount++
		if err != nil {
			return ErrorCursor{}, fmt.Errorf("read WB Cards Error List: %w", err)
		}
		if response.Error {
			return ErrorCursor{}, ErrErrorFeedEnvelope
		}
		for _, raw := range response.Data.Items {
			batch, err := normalizeErrorBatch(cabinetID, raw)
			if err != nil {
				return ErrorCursor{}, err
			}
			batches = append(batches, batch)
			batchesCount++
			if cursorAfter(batch.UpdatedAt, batch.BatchUUID, next.UpdatedAt, next.BatchUUID) {
				next.UpdatedAt = batch.UpdatedAt
				next.BatchUUID = batch.BatchUUID
			}
		}
		cursorTime, err := parseWBTime(response.Data.Cursor.UpdatedAt)
		if response.Data.Cursor.UpdatedAt != "" && err != nil {
			return ErrorCursor{}, fmt.Errorf("parse Cards Error List cursor: %w", err)
		}
		if cursorAfter(
			cursorTime,
			response.Data.Cursor.BatchUUID,
			next.UpdatedAt,
			next.BatchUUID,
		) {
			next.UpdatedAt = cursorTime
			next.BatchUUID = response.Data.Cursor.BatchUUID
		}
		if !response.Data.Cursor.Next {
			polledAt := feed.now().UTC()
			if cursorAfter(polledAt, "", next.UpdatedAt, next.BatchUUID) {
				next.UpdatedAt = polledAt
				next.BatchUUID = ""
			}
			next.CabinetID = cabinetID
			next.PolledAt = polledAt
			command := SaveErrorFeedCommand{
				CabinetID: cabinetID,
				Previous:  previous,
				Next:      next,
				Batches:   batches,
				PolledAt:  polledAt,
			}
			stepStartedAt = time.Now()
			err = feed.uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
				var err error
				savedCursor, err = feed.repository.SaveErrorFeed(ctx, tx, command)
				return err
			})
			saveDuration = time.Since(stepStartedAt)
			if err != nil {
				return ErrorCursor{}, fmt.Errorf("save Cards Error List feed: %w", err)
			}
			return savedCursor, nil
		}
		if response.Data.Cursor.UpdatedAt == requestCursor.UpdatedAt &&
			response.Data.Cursor.BatchUUID == requestCursor.BatchUUID {
			return ErrorCursor{}, errors.New("Cards Error List cursor did not advance")
		}
		requestCursor.UpdatedAt = response.Data.Cursor.UpdatedAt
		requestCursor.BatchUUID = response.Data.Cursor.BatchUUID
	}
	return ErrorCursor{}, errors.New("Cards Error List pagination limit exceeded")
}

func (feed *ErrorFeed) CaptureBaseline(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command CaptureErrorBaselineCommand,
) (ErrorBaseline, error) {
	if ctx == nil || tx == nil || command.TransferID <= 0 || command.ActionID <= 0 ||
		command.CabinetID == "" {
		return ErrorBaseline{}, errors.New("capture Cards Error List baseline command is invalid")
	}
	return feed.repository.CaptureErrorBaseline(ctx, tx, command)
}

func normalizeErrorBatch(
	cabinetID CabinetID,
	raw contentapi.CardsErrorBatch,
) (ErrorBatchEvidence, error) {
	if strings.TrimSpace(raw.BatchUUID) == "" {
		return ErrorBatchEvidence{}, errors.New("Cards Error List batch UUID is empty")
	}
	updatedAt, err := parseWBTime(raw.UpdatedAt)
	if err != nil || updatedAt.IsZero() {
		return ErrorBatchEvidence{}, errors.New("Cards Error List batch time is invalid")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return ErrorBatchEvidence{}, fmt.Errorf("encode Cards Error List batch: %w", err)
	}
	vendorCodes := normalizedUniqueStrings(raw.VendorCodes)
	rejectedVendorCodes := make([]string, 0, len(raw.Errors))
	errorCodeSet := make(map[string]struct{})
	for vendorCode, messages := range raw.Errors {
		rejectedVendorCodes = append(rejectedVendorCodes, vendorCode)
		for _, message := range messages {
			message = strings.TrimSpace(message)
			if message == "" {
				continue
			}
			digest := sha256.Sum256([]byte(message))
			errorCodeSet["wb_error_"+hex.EncodeToString(digest[:])] = struct{}{}
		}
	}
	rejectedVendorCodes = normalizedUniqueStrings(rejectedVendorCodes)
	errorCodes := make([]string, 0, len(errorCodeSet))
	for code := range errorCodeSet {
		errorCodes = append(errorCodes, code)
	}
	sort.Strings(errorCodes)
	if len(errorCodes) == 0 {
		errorCodes = []string{"wb_error_unspecified"}
	}
	return ErrorBatchEvidence{
		CabinetID:           cabinetID,
		BatchUUID:           strings.TrimSpace(raw.BatchUUID),
		UpdatedAt:           updatedAt,
		SourceDigest:        digestParts("cardpublication-error-batch:v1", encoded),
		VendorCodes:         vendorCodes,
		RejectedVendorCodes: rejectedVendorCodes,
		ErrorCodes:          errorCodes,
	}, nil
}

func cursorAfter(leftTime time.Time, leftUUID string, rightTime time.Time, rightUUID string) bool {
	if leftTime.After(rightTime) {
		return true
	}
	return leftTime.Equal(rightTime) && leftUUID > rightUUID
}
