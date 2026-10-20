package flowcontrol

import (
	"context"
	"math"
	"sync"
	"time"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	"golang.org/x/time/rate"
)

// bucketLimiter ограничивает скорость запросов одного rate-limit bucket.
type bucketLimiter struct {
	mu           sync.Mutex
	tokenBucket  *rate.Limiter
	waiters      chan struct{}
	admission    chan struct{}
	interval     time.Duration
	burst        int
	blockedUntil time.Time
}

// newBucketLimiter создаёт limiter из проверенной политики bucket.
func newBucketLimiter(spec policy.BucketSpec) *bucketLimiter {
	limiter := &bucketLimiter{
		tokenBucket: rate.NewLimiter(
			rate.Every(spec.Interval),
			spec.Burst,
		),
		waiters: make(
			chan struct{},
			spec.MaxWaiters,
		),
		interval:  spec.Interval,
		burst:     spec.Burst,
		admission: make(chan struct{}, 1),
	}
	limiter.admission <- struct{}{}

	return limiter
}

// wait ожидает разрешение на отправку одного HTTP-запроса.
func (limiter *bucketLimiter) wait(
	ctx context.Context,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	allowed, err := limiter.tryAllowNow()
	if err != nil {
		return err
	}
	if allowed {
		return nil
	}

	if err := limiter.acquireWaiter(ctx); err != nil {
		return err
	}
	defer limiter.releaseWaiter()

	if err := limiter.acquireAdmission(ctx); err != nil {
		return err
	}
	defer limiter.releaseAdmission()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		reservation, tokenDelay, blockDelay, err :=
			limiter.reserveToken()
		if err != nil {
			return err
		}

		if blockDelay > 0 {
			if err := waitDuration(
				ctx,
				blockDelay,
			); err != nil {
				return err
			}

			continue
		}

		if err := limiter.waitReservation(
			ctx,
			reservation,
			tokenDelay,
		); err != nil {
			return err
		}

		return limiter.waitForServerBlock(ctx)
	}
}

func (limiter *bucketLimiter) tryAllowNow() (bool, error) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := time.Now()
	blockDelay, err := limiter.blockDelayLocked(
		now,
		1,
	)
	if err != nil {
		return false, err
	}
	if blockDelay > 0 {
		return false, nil
	}

	return limiter.tokenBucket.AllowN(now, 1), nil
}

func (limiter *bucketLimiter) acquireWaiter(
	ctx context.Context,
) error {
	select {
	case limiter.waiters <- struct{}{}:
		return nil

	case <-ctx.Done():
		return ctx.Err()

	default:
		return ErrLimiterQueueFull
	}
}

func (limiter *bucketLimiter) releaseWaiter() {
	<-limiter.waiters
}

func (limiter *bucketLimiter) acquireAdmission(
	ctx context.Context,
) error {
	select {
	case <-limiter.admission:
		return nil

	case <-ctx.Done():
		return ctx.Err()
	}
}

func (limiter *bucketLimiter) releaseAdmission() {
	limiter.admission <- struct{}{}
}

func (limiter *bucketLimiter) reserveToken() (
	*rate.Reservation,
	time.Duration,
	time.Duration,
	error,
) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := time.Now()
	blockDelay, err := limiter.blockDelayLocked(
		now,
		1,
	)
	if err != nil {
		return nil, 0, 0, err
	}
	if blockDelay > 0 {
		return nil, 0, blockDelay, nil
	}

	reservation := limiter.tokenBucket.ReserveN(
		now,
		1,
	)
	if !reservation.OK() {
		return nil, 0, 0,
			errLimiterReservationFailed
	}

	return reservation,
		reservation.DelayFrom(now),
		0,
		nil
}

func (limiter *bucketLimiter) waitReservation(
	ctx context.Context,
	reservation *rate.Reservation,
	delay time.Duration,
) error {
	if reservation == nil {
		return errLimiterReservationFailed
	}
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		limiter.mu.Lock()
		reservation.CancelAt(time.Now())
		limiter.mu.Unlock()

		return ctx.Err()

	case <-timer.C:
		return nil
	}
}

func (limiter *bucketLimiter) waitForServerBlock(
	ctx context.Context,
) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		limiter.mu.Lock()
		blockDelay, err := limiter.blockDelayLocked(
			time.Now(),
			0,
		)
		limiter.mu.Unlock()
		if err != nil {
			return err
		}
		if blockDelay <= 0 {
			return nil
		}

		if err := waitDuration(
			ctx,
			blockDelay,
		); err != nil {
			return err
		}
	}
}

func (limiter *bucketLimiter) blockDelayLocked(
	now time.Time,
	remainingAfterBlock int,
) (time.Duration, error) {
	if limiter.blockedUntil.IsZero() {
		return 0, nil
	}
	if now.Before(limiter.blockedUntil) {
		return limiter.blockedUntil.Sub(now), nil
	}

	limiter.blockedUntil = time.Time{}

	return 0, limiter.clampRemainingLocked(
		now,
		remainingAfterBlock,
	)
}

func (limiter *bucketLimiter) observe(
	limits responseRateLimits,
) error {
	now := time.Now()

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	if limits.statusCode == 429 {
		blockDuration := limiter.interval
		if limits.retryAfter != nil &&
			*limits.retryAfter > 0 {
			blockDuration = *limits.retryAfter
		}

		blockedUntil := now.Add(blockDuration)
		if blockedUntil.After(limiter.blockedUntil) {
			limiter.blockedUntil = blockedUntil
		}

		return limiter.clampRemainingLocked(now, 0)
	}

	if limits.remaining == nil {
		return nil
	}

	remaining := *limits.remaining
	if limits.limit != nil &&
		*limits.limit < remaining {
		remaining = *limits.limit
	}

	return limiter.clampRemainingLocked(
		now,
		remaining,
	)
}

func (limiter *bucketLimiter) clampRemainingLocked(
	now time.Time,
	remaining int,
) error {
	if remaining < 0 {
		remaining = 0
	}
	if remaining > limiter.burst {
		remaining = limiter.burst
	}

	tokens := limiter.tokenBucket.TokensAt(now)
	if tokens <= float64(remaining) {
		return nil
	}

	consume := int(math.Ceil(
		tokens - float64(remaining),
	))
	if consume <= 0 {
		return nil
	}
	if consume > limiter.burst {
		consume = limiter.burst
	}

	reservation := limiter.tokenBucket.ReserveN(
		now,
		consume,
	)
	if !reservation.OK() {
		return errLimiterReservationFailed
	}

	return nil
}

func waitDuration(
	ctx context.Context,
	delay time.Duration,
) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()

	case <-timer.C:
		return nil
	}
}
