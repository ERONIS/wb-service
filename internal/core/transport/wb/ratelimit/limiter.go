package core_transport_wb_ratelimit

import (
	"math"
	"sync"
	"time"

	core_transport_wb_policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

type bucketLimiter struct {
	mutex        sync.Mutex
	policy       core_transport_wb_policy.BucketPolicy
	tokens       float64
	lastRefill   time.Time
	blockedUntil time.Time
}

func newBucketLimiter(
	bucketPolicy core_transport_wb_policy.BucketPolicy,
) *bucketLimiter {
	now := time.Now()

	return &bucketLimiter{
		policy:     bucketPolicy,
		tokens:     float64(bucketPolicy.Burst),
		lastRefill: now,
	}
}

// Отвечает за пересчёт восстановившихся токенов
func (limiter *bucketLimiter) refillLocked(
	now time.Time,
) {
	if !now.After(limiter.lastRefill) {
		return
	}

	elapsed := now.Sub(limiter.lastRefill)

	refilledTokens :=
		float64(elapsed) /
			float64(limiter.policy.Interval)

	limiter.tokens = min(
		float64(limiter.policy.Burst),
		limiter.tokens+refilledTokens,
	)

	limiter.lastRefill = now
}

func (limiter *bucketLimiter) waitDurationLocked(
	now time.Time,
) time.Duration {
	if now.Before(limiter.blockedUntil) {
		return limiter.blockedUntil.Sub(now)
	}

	limiter.refillLocked(now)

	if limiter.tokens >= 1 {
		return 0
	}

	missingTokens := 1 - limiter.tokens

	return time.Duration(
		math.Ceil(
			missingTokens *
				float64(limiter.policy.Interval),
		),
	)
}

func (limiter *bucketLimiter) takeLocked() {
	limiter.tokens--
}

func (limiter *bucketLimiter) clampRemainingLocked(
	now time.Time,
	remaining int,
) {
	limiter.refillLocked(now)

	limiter.tokens = min(
		limiter.tokens,
		float64(remaining),
	)
}
func (limiter *bucketLimiter) applyRetryLocked(
	now time.Time,
	retryDuration time.Duration,
) {
	blockedUntil := now.Add(retryDuration)

	if !blockedUntil.After(limiter.blockedUntil) {
		return
	}

	limiter.blockedUntil = blockedUntil
	limiter.tokens = 0

	limiter.lastRefill =
		blockedUntil.Add(
			-limiter.policy.Interval,
		)
}
