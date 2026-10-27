package monitoring

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardpublication_catalog "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
)

type independentErrorFeedTargets struct {
	targets []core_domain.CabinetAccess
	calls   int
}

type independentErrorFeedTransport struct {
	cardpublication_catalog.CatalogTransport
	original    context.Context
	failingID   core_domain.SellerID
	failure     error
	failed      chan struct{}
	failedCalls atomic.Int32
	goodCalls   atomic.Int32
}

type independentErrorFeedRepository struct {
	ErrorFeedRepository
	saved []core_domain.SellerID
}

type independentErrorFeedUOW struct{}

func (source *independentErrorFeedTargets) AllTargets(context.Context) ([]core_domain.CabinetAccess, error) {
	source.calls++
	return source.targets, nil
}

func (transport *independentErrorFeedTransport) CardsErrorList(
	ctx context.Context,
	sellerID core_domain.SellerID,
	_ contentapi.CardsErrorListQuery,
	_ contentapi.CardsErrorListRequest,
) (contentapi.CardsErrorListResponse, error) {
	if sellerID == transport.failingID {
		transport.failedCalls.Add(1)
		close(transport.failed)
		return contentapi.CardsErrorListResponse{}, transport.failure
	}
	transport.goodCalls.Add(1)
	<-transport.failed
	if ctx != transport.original || ctx.Err() != nil {
		return contentapi.CardsErrorListResponse{}, errors.New("контекст успешного кабинета отменён")
	}
	return contentapi.CardsErrorListResponse{}, nil
}

func (*independentErrorFeedRepository) LoadErrorCursor(
	_ context.Context,
	sellerID core_domain.SellerID,
) (ErrorCursor, error) {
	return ErrorCursor{SellerID: sellerID}, nil
}

func (repository *independentErrorFeedRepository) SaveErrorFeed(
	_ context.Context,
	_ core_postgres_transaction.DBTX,
	command SaveErrorFeedCommand,
) (ErrorCursor, error) {
	repository.saved = append(repository.saved, command.SellerID)
	return command.Next, nil
}

func (independentErrorFeedUOW) WithinTransaction(
	ctx context.Context,
	run func(context.Context, core_postgres_transaction.DBTX) error,
) error {
	return run(ctx, nil)
}

func TestErrorFeedPollsOtherCabinetsAfterOneFails(t *testing.T) {
	ctx := context.Background()
	failingID := core_domain.SellerID("00000000-0000-0000-0000-000000000001")
	goodID := core_domain.SellerID("00000000-0000-0000-0000-000000000002")
	failure := errors.New("временная ошибка WB")
	targets := &independentErrorFeedTargets{targets: []core_domain.CabinetAccess{
		{SellerID: failingID},
		{SellerID: goodID},
	}}
	transport := &independentErrorFeedTransport{
		original: ctx, failingID: failingID, failure: failure, failed: make(chan struct{}),
	}
	repository := &independentErrorFeedRepository{}
	feed := NewErrorFeed(transport, repository, targets, independentErrorFeedUOW{})
	feed.now = func() time.Time { return time.Date(2026, time.October, 26, 12, 0, 0, 0, time.UTC) }

	if err := feed.ProcessPending(ctx); !errors.Is(err, failure) {
		t.Fatalf("ошибка кабинета не возвращена: %v", err)
	}
	if transport.failedCalls.Load() != 1 || transport.goodCalls.Load() != 1 ||
		len(repository.saved) != 1 || repository.saved[0] != goodID {
		t.Fatalf("успешный кабинет не сохранён: bad=%d good=%d saved=%v",
			transport.failedCalls.Load(), transport.goodCalls.Load(), repository.saved)
	}
	if err := feed.ProcessPending(ctx); err != nil || targets.calls != 1 {
		t.Fatalf("повторный опрос выполнен до интервала: calls=%d err=%v", targets.calls, err)
	}
}
