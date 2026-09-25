package preparation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

type referenceTransport struct {
	CatalogTransport
	calls, limits  int
	fail, envelope bool
}

func (s *referenceTransport) Subjects(context.Context, CabinetID, contentapi.SubjectsQuery) (contentapi.SubjectsResponse, error) {
	s.calls++
	if s.fail {
		return contentapi.SubjectsResponse{}, errors.New("temporary")
	}
	return contentapi.SubjectsResponse{Data: []contentapi.Subject{{SubjectID: 1, SubjectName: "original"}}, ResponseMeta: contentapi.ResponseMeta{Error: s.envelope}}, nil
}
func (s *referenceTransport) CardsLimits(context.Context, CabinetID) (contentapi.CardsLimitsResponse, error) {
	s.limits++
	return contentapi.CardsLimitsResponse{}, nil
}

func TestBatchCatalogIsolationAndLiveLimits(t *testing.T) {
	source := &referenceTransport{}
	preparer := NewPreparer(source)
	batch := preparer.ForBatch()
	ctx := context.Background()
	query := contentapi.SubjectsQuery{Name: "category"}
	first, err := batch.transport.Subjects(ctx, "one", query)
	if err != nil {
		t.Fatal(err)
	}
	first.Data[0].SubjectName = "changed"
	second, err := batch.transport.Subjects(ctx, "one", query)
	if err != nil || second.Data[0].SubjectName != "original" || source.calls != 1 {
		t.Fatalf("cache did not isolate response: %+v %v calls=%d", second, err, source.calls)
	}
	for range 2 {
		if _, err := batch.transport.CardsLimits(ctx, "one"); err != nil {
			t.Fatal(err)
		}
	}
	if source.limits != 1 {
		t.Fatalf("identical limits were not coalesced: %d", source.limits)
	}
	if _, err := batch.transport.Subjects(ctx, "two", query); err != nil {
		t.Fatal(err)
	}
	if _, err := batch.transport.Subjects(ctx, "one", contentapi.SubjectsQuery{Name: "other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := preparer.ForBatch().transport.Subjects(ctx, "one", query); err != nil {
		t.Fatal(err)
	}
	if source.calls != 4 {
		t.Fatalf("cabinet/query/batch isolation failed: %d", source.calls)
	}
	cache := batch.transport.(*batchCatalog)
	stats := cache.stats()
	if stats.Hits == 0 || stats.Misses == 0 || stats.Stores == 0 || stats.Bytes == 0 {
		t.Fatalf("cache stats were not recorded: %+v", stats)
	}
}

type concurrentCatalogTransport struct {
	CatalogTransport
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func (transport *concurrentCatalogTransport) Subjects(context.Context, CabinetID, contentapi.SubjectsQuery) (contentapi.SubjectsResponse, error) {
	transport.mu.Lock()
	transport.calls++
	transport.mu.Unlock()
	select {
	case transport.started <- struct{}{}:
	default:
	}
	<-transport.release
	return contentapi.SubjectsResponse{
		Data: []contentapi.Subject{{SubjectID: 1, SubjectName: "subject"}},
	}, nil
}

func TestBatchCatalogCoalescesConcurrentIdenticalReads(t *testing.T) {
	transport := &concurrentCatalogTransport{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	batch := NewPreparer(transport).ForBatch()
	query := contentapi.SubjectsQuery{Name: "subject"}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := batch.transport.Subjects(context.Background(), "one", query)
			results <- err
		}()
	}
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("catalog read did not start")
	}
	time.Sleep(10 * time.Millisecond)
	transport.mu.Lock()
	calls := transport.calls
	transport.mu.Unlock()
	if calls != 1 {
		t.Fatalf("identical reads were not coalesced: %d", calls)
	}
	close(transport.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	stats := batch.transport.(*batchCatalog).stats()
	if stats.CoalescedWaits != 1 || stats.Hits != 1 || stats.Misses != 2 {
		t.Fatalf("unexpected coalescing stats: %+v", stats)
	}
}
func TestBatchCatalogDoesNotCacheFailures(t *testing.T) {
	for _, envelope := range []bool{false, true} {
		source := &referenceTransport{fail: !envelope, envelope: envelope}
		batch := NewPreparer(source).ForBatch()
		_, _ = batch.transport.Subjects(context.Background(), "one", contentapi.SubjectsQuery{})
		source.fail, source.envelope = false, false
		result, err := batch.transport.Subjects(context.Background(), "one", contentapi.SubjectsQuery{})
		if err != nil || result.Error || source.calls != 2 {
			t.Fatalf("failure cached: %+v %v", result, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := batch.transport.Subjects(ctx, "one", contentapi.SubjectsQuery{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled cache access: %v", err)
		}
	}
}
