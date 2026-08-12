package flowcontrol

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

func TestLimiterBoundsWaitQueueAndCleansCanceledWaiter(t *testing.T) {
	t.Parallel()

	limiter, err := NewLimiter(policy.BucketSpec{
		ID:         "queue_test",
		Interval:   time.Hour,
		Burst:      1,
		MaxWaiters: 1,
	})
	if err != nil {
		t.Fatalf("create limiter: %v", err)
	}
	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatalf("consume initial token: %v", err)
	}

	waitContext, cancelWait := context.WithCancel(context.Background())
	waitResult := make(chan error, 1)
	go func() {
		waitResult <- limiter.Wait(waitContext)
	}()

	deadline := time.Now().Add(time.Second)
	for len(limiter.waiters) != 1 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if len(limiter.waiters) != 1 {
		cancelWait()
		t.Fatal("first waiter did not enter the bounded queue")
	}

	if err := limiter.Wait(context.Background()); !errors.Is(err, ErrLimiterQueueFull) {
		cancelWait()
		t.Fatalf("second waiter error = %v, want ErrLimiterQueueFull", err)
	}

	cancelWait()
	select {
	case err := <-waitResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled waiter error = %v", err)
		}

	case <-time.After(time.Second):
		t.Fatal("canceled waiter did not exit")
	}
	if len(limiter.waiters) != 0 {
		t.Fatalf("waiter queue length = %d, want 0", len(limiter.waiters))
	}
}

func TestBackoffUsesLargerServerDelayAndHonorsCancellation(t *testing.T) {
	t.Parallel()

	backoff, err := NewBackoff(10 * time.Millisecond)
	if err != nil {
		t.Fatalf("create backoff: %v", err)
	}

	delay, err := backoff.delay(3, 25*time.Millisecond)
	if err != nil {
		t.Fatalf("calculate delay: %v", err)
	}
	if delay != 40*time.Millisecond {
		t.Fatalf("delay = %s, want 40ms", delay)
	}

	delay, err = backoff.delay(1, time.Second)
	if err != nil {
		t.Fatalf("calculate server delay: %v", err)
	}
	if delay != time.Second {
		t.Fatalf("server delay = %s, want 1s", delay)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := backoff.Wait(ctx, 1, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Wait error = %v", err)
	}
}
