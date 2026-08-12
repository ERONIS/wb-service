package flowcontrol

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	"golang.org/x/time/rate"
)

var (
	errLimiterRequired = errors.New(
		"rate limiter is required",
	)
	errLimiterContextRequired = errors.New(
		"rate limiter context is required",
	)
	errLimiterReservationFailed = errors.New(
		"rate limiter could not reserve a request token",
	)
)

// ErrLimiterQueueFull означает, что очередь ожидания заполнена.
var ErrLimiterQueueFull = errors.New(
	"rate limiter wait queue is full",
)

// Limiter ограничивает скорость запросов одного rate-limit bucket.
type Limiter struct {
	mutex        sync.Mutex
	tokenBucket  *rate.Limiter
	waiters      chan struct{}
	admission    chan struct{}
	interval     time.Duration
	burst        int
	blockedUntil time.Time
}

// NewLimiter создаёт limiter из политики bucket.
func NewLimiter(
	spec policy.BucketSpec,
) (*Limiter, error) {
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf(
			"invalid rate limiter bucket spec: %w",
			err,
		)
	}

	limiter := &Limiter{
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

	return limiter, nil
}

// Wait ожидает разрешение на отправку одного HTTP-запроса.
func (limiter *Limiter) Wait(
	ctx context.Context,
) error {
	if limiter == nil ||
		limiter.tokenBucket == nil {
		return errLimiterRequired
	}
	if ctx == nil {
		return errLimiterContextRequired
	}
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

func (limiter *Limiter) tryAllowNow() (bool, error) {
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()

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

func (limiter *Limiter) acquireWaiter(
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

func (limiter *Limiter) releaseWaiter() {
	<-limiter.waiters
}

func (limiter *Limiter) acquireAdmission(
	ctx context.Context,
) error {
	select {
	case <-limiter.admission:
		return nil

	case <-ctx.Done():
		return ctx.Err()
	}
}

func (limiter *Limiter) releaseAdmission() {
	limiter.admission <- struct{}{}
}

func (limiter *Limiter) reserveToken() (
	*rate.Reservation,
	time.Duration,
	time.Duration,
	error,
) {
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()

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

func (limiter *Limiter) waitReservation(
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
		limiter.mutex.Lock()
		reservation.CancelAt(time.Now())
		limiter.mutex.Unlock()

		return ctx.Err()

	case <-timer.C:
		return nil
	}
}

func (limiter *Limiter) waitForServerBlock(
	ctx context.Context,
) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		limiter.mutex.Lock()
		blockDelay, err := limiter.blockDelayLocked(
			time.Now(),
			0,
		)
		limiter.mutex.Unlock()
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

func (limiter *Limiter) blockDelayLocked(
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

func (limiter *Limiter) observe(
	limits responseRateLimits,
) error {
	if limiter == nil ||
		limiter.tokenBucket == nil {
		return errLimiterRequired
	}

	now := time.Now()

	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()

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

func (limiter *Limiter) clampRemainingLocked(
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
