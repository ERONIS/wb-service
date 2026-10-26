package flowcontrol

import (
	"context"
	"math"
	"sync"
	"time"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	"golang.org/x/time/rate"
)

// bucketLimiter ограничивает скорость запросов в одной корзине лимита.
type bucketLimiter struct {
	mu           sync.Mutex
	tokenBucket  *rate.Limiter
	waiters      chan struct{}
	admission    chan struct{}
	interval     time.Duration
	burst        int
	blockedUntil time.Time
}

// newBucketLimiter создаёт лимитер из проверенных параметров корзины.
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

// wait ожидает разрешение на отправку одного запроса.
func (limiter *bucketLimiter) wait(
	ctx context.Context,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if limiter.tryAllowNow() {
		return nil
	}

	if err := limiter.acquireWaiter(ctx); err != nil {
		return err
	}
	defer func() {
		<-limiter.waiters
	}()

	if err := limiter.acquireAdmission(ctx); err != nil {
		return err
	}
	defer func() {
		limiter.admission <- struct{}{}
	}()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		reservation, tokenDelay, blockDelay := limiter.reserveToken()

		if blockDelay > 0 {
			if err := waitDuration(ctx, blockDelay); err != nil {
				return err
			}
			continue
		}

		if err := limiter.waitReservation(
			ctx, reservation, tokenDelay,
		); err != nil {
			return err
		}

		return limiter.waitForServerBlock(ctx)
	}
}

// observe учитывает ограничения из ответа WB и паузу после 429.
func (limiter *bucketLimiter) observe(
	limits responseRateLimits,
) {
	now := time.Now()

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	if limits.statusCode == 429 {
		blockDuration := limiter.interval
		if limits.retryAfter != nil && *limits.retryAfter > 0 {
			blockDuration = *limits.retryAfter
		}

		blockedUntil := now.Add(blockDuration)
		if blockedUntil.After(limiter.blockedUntil) {
			limiter.blockedUntil = blockedUntil
		}

		limiter.clampRemainingLocked(now, 0)
		return
	}

	if limits.remaining == nil {
		return
	}

	remaining := *limits.remaining
	if limits.limit != nil && *limits.limit < remaining {
		remaining = *limits.limit
	}

	limiter.clampRemainingLocked(now, remaining)
}

// tryAllowNow пробует сразу взять токен, если нет блокировки.
func (limiter *bucketLimiter) tryAllowNow() bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := time.Now()
	if limiter.blockDelayLocked(now, 1) > 0 {
		return false
	}

	return limiter.tokenBucket.AllowN(now, 1)
}

// acquireWaiter занимает место среди ожидающих запросов.
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

// acquireAdmission даёт одному ожидающему право бронировать токен.
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

// reserveToken бронирует токен и возвращает время ожидания.
func (limiter *bucketLimiter) reserveToken() (
	*rate.Reservation,
	time.Duration,
	time.Duration,
) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := time.Now()
	blockDelay := limiter.blockDelayLocked(now, 1)
	if blockDelay > 0 {
		return nil, 0, blockDelay
	}

	reservation := limiter.tokenBucket.ReserveN(
		now,
		1,
	)
	return reservation,
		reservation.DelayFrom(now),
		0
}

// waitReservation ждёт токен и отменяет бронь при отмене контекста.
func (limiter *bucketLimiter) waitReservation(
	ctx context.Context,
	reservation *rate.Reservation,
	delay time.Duration,
) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		limiter.mu.Lock()
		reservation.CancelAt(time.Now())
		limiter.mu.Unlock()

		return ctx.Err()
	}
}

// waitForServerBlock ждёт окончания паузы, возникшей во время ожидания токена.
func (limiter *bucketLimiter) waitForServerBlock(
	ctx context.Context,
) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		limiter.mu.Lock()
		blockDelay := limiter.blockDelayLocked(time.Now(), 0)
		limiter.mu.Unlock()

		if blockDelay <= 0 {
			return nil
		}

		if err := waitDuration(ctx, blockDelay); err != nil {
			return err
		}
	}
}

// blockDelayLocked возвращает остаток паузы и ограничивает токены после неё.
func (limiter *bucketLimiter) blockDelayLocked(
	now time.Time,
	remainingAfterBlock int,
) time.Duration {
	if limiter.blockedUntil.IsZero() {
		return 0
	}
	if now.Before(limiter.blockedUntil) {
		return limiter.blockedUntil.Sub(now)
	}

	limiter.blockedUntil = time.Time{}
	limiter.clampRemainingLocked(now, remainingAfterBlock)

	return 0
}

// clampRemainingLocked уменьшает число доступных токенов до указанного предела.
func (limiter *bucketLimiter) clampRemainingLocked(
	now time.Time,
	remaining int,
) {
	if remaining > limiter.burst {
		remaining = limiter.burst
	}

	tokens := limiter.tokenBucket.TokensAt(now)
	if tokens <= float64(remaining) {
		return
	}

	consume := int(math.Ceil(tokens - float64(remaining)))
	limiter.tokenBucket.ReserveN(now, consume)
}

// waitDuration ждёт заданное время или отмену контекста.
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
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
