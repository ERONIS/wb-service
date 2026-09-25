package core_errors

import (
	"context"
	"errors"
	"fmt"
)

// Standard library errors functions re-exported for convenience.
var (
	Is     = errors.Is
	As     = errors.As
	New    = errors.New
	Join   = errors.Join
	Unwrap = errors.Unwrap
)

// Common domain, client, and business errors (4xx equivalents).
var (
	ErrNotFound           = errors.New("not found")
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrConflict           = errors.New("conflict")
	ErrForbidden          = errors.New("forbidden")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrAlreadyExists      = errors.New("already exists")
	ErrRateLimited        = errors.New("rate limited")
	ErrQueueFull          = errors.New("queue full")
	ErrCapacityExceeded   = errors.New("capacity exceeded")
	ErrPayloadTooLarge    = errors.New("payload too large")
	ErrPreconditionFailed = errors.New("precondition failed")
)

// Common system, infrastructure, and server errors (5xx equivalents).
var (
	ErrInternal       = errors.New("internal error")
	ErrUnavailable    = errors.New("service unavailable")
	ErrTimeout        = errors.New("timeout")
	ErrCanceled       = errors.New("operation canceled")
	ErrNotImplemented = errors.New("not implemented")
)

// Wrap returns an error annotating err with message.
// If err is nil, Wrap returns nil.
func Wrap(err error, message string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

// Wrapf returns an error annotating err with formatted message.
// If err is nil, Wrapf returns nil.
func Wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), err)
}

func formatError(sentinel error, format string, args ...any) error {
	if len(args) == 0 {
		return fmt.Errorf("%s: %w", format, sentinel)
	}
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), sentinel)
}

func NotFound(format string, args ...any) error            { return formatError(ErrNotFound, format, args...) }
func InvalidArgument(format string, args ...any) error    { return formatError(ErrInvalidArgument, format, args...) }
func Conflict(format string, args ...any) error           { return formatError(ErrConflict, format, args...) }
func Forbidden(format string, args ...any) error          { return formatError(ErrForbidden, format, args...) }
func Unauthorized(format string, args ...any) error       { return formatError(ErrUnauthorized, format, args...) }
func AlreadyExists(format string, args ...any) error      { return formatError(ErrAlreadyExists, format, args...) }
func RateLimited(format string, args ...any) error        { return formatError(ErrRateLimited, format, args...) }
func QueueFull(format string, args ...any) error          { return formatError(ErrQueueFull, format, args...) }
func CapacityExceeded(format string, args ...any) error   { return formatError(ErrCapacityExceeded, format, args...) }
func Unavailable(format string, args ...any) error        { return formatError(ErrUnavailable, format, args...) }
func Internal(format string, args ...any) error           { return formatError(ErrInternal, format, args...) }
func PreconditionFailed(format string, args ...any) error { return formatError(ErrPreconditionFailed, format, args...) }

func IsNotFound(err error) bool        { return errors.Is(err, ErrNotFound) }
func IsInvalidArgument(err error) bool { return errors.Is(err, ErrInvalidArgument) }
func IsConflict(err error) bool        { return errors.Is(err, ErrConflict) || errors.Is(err, ErrAlreadyExists) }
func IsForbidden(err error) bool       { return errors.Is(err, ErrForbidden) }
func IsUnauthorized(err error) bool    { return errors.Is(err, ErrUnauthorized) }
func IsRateLimited(err error) bool     { return errors.Is(err, ErrRateLimited) }
func IsUnavailable(err error) bool     { return errors.Is(err, ErrUnavailable) }
func IsTimeout(err error) bool         { return errors.Is(err, ErrTimeout) || errors.Is(err, context.DeadlineExceeded) }
func IsCanceled(err error) bool        { return errors.Is(err, ErrCanceled) || errors.Is(err, context.Canceled) }

func Require(condition bool, err error) error {
	if !condition {
		return err
	}
	return nil
}

func RequireContext(ctx context.Context) error {
	if ctx == nil {
		return InvalidArgument("context is nil")
	}
	return ctx.Err()
}


