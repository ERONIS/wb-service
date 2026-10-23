package flowcontrol

import (
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

var (
	errRegistryRequired         = errors.New("rate limiter registry is required")
	errRegistryContextRequired  = errors.New("rate limiter registry context is required")
	errRegistrySellerIDRequired = errors.New("rate limiter registry seller ID is required")
	errRegistryBucketIDRequired = errors.New("rate limiter registry bucket ID is required")
	ErrBucketNotRegistered      = errors.New("rate limiter bucket is not registered")
	ErrLimiterQueueFull         = fmt.Errorf(
		"rate limiter wait queue is full: %w",
		core_errors.ErrQueueFull,
	)
	errRateLimitResponseRequired = errors.New("rate-limit HTTP response is required")
)
