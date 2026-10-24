package monitoring

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardpublication_catalog "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"

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
	SellerID  core_domain.SellerID
	UpdatedAt time.Time
	BatchUUID string
	Revision  int64
	PolledAt  time.Time
}

type ErrorBatchEvidence struct {
	SellerID            core_domain.SellerID
	BatchUUID           string
	UpdatedAt           time.Time
	VendorCodes         []string
	RejectedVendorCodes []string
	ErrorCodes          []string
}

type SaveErrorFeedCommand struct {
	SellerID core_domain.SellerID
	Previous ErrorCursor
	Next     ErrorCursor
	Batches  []ErrorBatchEvidence
	PolledAt time.Time
}

type CaptureErrorBaselineCommand struct {
	TransferID transfer_model.TransferID
	ActionID   int64
	SellerID   core_domain.SellerID
}

type ErrorBaseline struct {
	TransferID      transfer_model.TransferID
	ActionID        int64
	SellerID        core_domain.SellerID
	CursorRevision  int64
	CursorUpdatedAt time.Time
	CursorBatchUUID string
	CapturedAt      time.Time
}

func (baseline ErrorBaseline) Validate() error {
	if baseline.TransferID <= 0 || baseline.ActionID <= 0 || baseline.SellerID == "" ||
		baseline.CursorRevision < 0 || baseline.CapturedAt.IsZero() ||
		(baseline.CursorUpdatedAt.IsZero() && baseline.CursorBatchUUID != "") {
		return errors.New("Cards Error List baseline is invalid")
	}
	return nil
}

type ErrorFeed struct {
	transport  cardpublication_catalog.CatalogTransport
	repository ErrorFeedRepository
	targets    ErrorTargetSource
	uow        core_postgres_transaction.UnitOfWork
	now        func() time.Time
	logger     *zap.Logger
	mu         sync.Mutex
	nextPoll   time.Time
}

func NewErrorFeed(
	transport cardpublication_catalog.CatalogTransport,
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
		logger:     core_logger.Fallback(loggers...),
	}
}

func (feed *ErrorFeed) ProcessPending(ctx context.Context) error {
	if ctx == nil {
		return errors.New("process Cards Error List: context is nil")
	}
	feed.mu.Lock()
	defer feed.mu.Unlock()
	now := feed.now().UTC()
	if !feed.nextPoll.IsZero() && now.Before(feed.nextPoll) {
		return nil
	}
	feed.nextPoll = now.Add(errorFeedInterval)
	targets, err := feed.targets.PublicationTargets(ctx)
	if err != nil {
		return err
	}
	group, refreshCtx := errgroup.WithContext(ctx)
	for _, target := range targets {
		target := target
		group.Go(func() error {
			_, err := feed.Refresh(refreshCtx, core_domain.SellerID(target.SellerID))
			return err
		})
	}
	return group.Wait()
}

func (feed *ErrorFeed) Refresh(
	ctx context.Context,
	sellerID core_domain.SellerID,
) (ErrorCursor, error) {
	if ctx == nil || strings.TrimSpace(string(sellerID)) != string(sellerID) ||
		sellerID == "" {
		return ErrorCursor{}, errors.New("Cards Error List cabinet is invalid")
	}
	previous, err := feed.repository.LoadErrorCursor(ctx, sellerID)
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
		response, err := feed.transport.CardsErrorList(
			ctx,
			sellerID,
			contentapi.CardsErrorListQuery{},
			contentapi.CardsErrorListRequest{
				Cursor: requestCursor,
				Order:  contentapi.CardsErrorListOrder{Ascending: true},
			},
		)
		if err != nil {
			return ErrorCursor{}, fmt.Errorf("read WB Cards Error List: %w", err)
		}
		if response.Error {
			return ErrorCursor{}, ErrErrorFeedEnvelope
		}
		for _, raw := range response.Data.Items {
			batch, err := normalizeErrorBatch(sellerID, raw)
			if err != nil {
				return ErrorCursor{}, err
			}
			batches = append(batches, batch)
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
			next.SellerID = sellerID
			next.PolledAt = polledAt
			command := SaveErrorFeedCommand{
				SellerID: sellerID,
				Previous: previous,
				Next:     next,
				Batches:  batches,
				PolledAt: polledAt,
			}
			var savedCursor ErrorCursor
			err = feed.uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
				var err error
				savedCursor, err = feed.repository.SaveErrorFeed(ctx, tx, command)
				return err
			})
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
		command.SellerID == "" {
		return ErrorBaseline{}, errors.New("capture Cards Error List baseline command is invalid")
	}
	return feed.repository.CaptureErrorBaseline(ctx, tx, command)
}

func normalizeErrorBatch(
	sellerID core_domain.SellerID,
	raw contentapi.CardsErrorBatch,
) (ErrorBatchEvidence, error) {
	if strings.TrimSpace(raw.BatchUUID) == "" {
		return ErrorBatchEvidence{}, errors.New("Cards Error List batch UUID is empty")
	}
	updatedAt, err := parseWBTime(raw.UpdatedAt)
	if err != nil || updatedAt.IsZero() {
		return ErrorBatchEvidence{}, errors.New("Cards Error List batch time is invalid")
	}
	vendorCodes := normalizedUniqueStrings(raw.VendorCodes)
	rejectedVendorCodes := make([]string, 0, len(raw.Errors))
	messagesSeen := make(map[string]struct{})
	for vendorCode, messages := range raw.Errors {
		rejectedVendorCodes = append(rejectedVendorCodes, vendorCode)
		for _, message := range messages {
			message = strings.TrimSpace(message)
			if message == "" {
				continue
			}
			messagesSeen[message] = struct{}{}
		}
	}
	rejectedVendorCodes = normalizedUniqueStrings(rejectedVendorCodes)
	messages := make([]string, 0, len(messagesSeen))
	for message := range messagesSeen {
		messages = append(messages, message)
	}
	sort.Strings(messages)
	errorCodes := make([]string, len(messages))
	for index := range messages {
		errorCodes[index] = fmt.Sprintf("wb_error_%d", index+1)
	}
	if len(errorCodes) == 0 {
		errorCodes = []string{"wb_error_unspecified"}
	}
	return ErrorBatchEvidence{
		SellerID:            sellerID,
		BatchUUID:           strings.TrimSpace(raw.BatchUUID),
		UpdatedAt:           updatedAt,
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
