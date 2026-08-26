package cardpublication_service

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestProcessConcurrentlyHonorsWorkerLimit(t *testing.T) {
	t.Parallel()

	const limit = 3
	started := make(chan struct{}, 10)
	release := make(chan struct{})
	done := make(chan error, 1)
	var active atomic.Int64
	var maximum atomic.Int64

	go func() {
		done <- processConcurrently(
			context.Background(),
			[]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			limit,
			func(int) error {
				current := active.Add(1)
				for {
					previous := maximum.Load()
					if current <= previous || maximum.CompareAndSwap(previous, current) {
						break
					}
				}
				started <- struct{}{}
				<-release
				active.Add(-1)
				return nil
			},
		)
	}()

	for range limit {
		<-started
	}
	select {
	case <-started:
		t.Fatal("worker limit was exceeded before a slot was released")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("process concurrently: %v", err)
	}
	if got := maximum.Load(); got != limit {
		t.Fatalf("maximum concurrency = %d, want %d", got, limit)
	}
}
