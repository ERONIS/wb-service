package core_errors_test

import (
	"fmt"
	"testing"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cabinetcopy_service "github.com/ERONIS/wb-service/internal/feature/cabinetcopy/service"
	cardedit_service "github.com/ERONIS/wb-service/internal/feature/cardedit/service"
	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"
)

func TestSentinelErrorsDistinct(t *testing.T) {
	sentinels := []error{
		core_errors.ErrNotFound,
		core_errors.ErrInvalidArgument,
		core_errors.ErrConflict,
		core_errors.ErrForbidden,
		core_errors.ErrUnauthorized,
		core_errors.ErrAlreadyExists,
		core_errors.ErrRateLimited,
		core_errors.ErrQueueFull,
		core_errors.ErrCapacityExceeded,
		core_errors.ErrPayloadTooLarge,
		core_errors.ErrPreconditionFailed,
		core_errors.ErrInternal,
		core_errors.ErrUnavailable,
		core_errors.ErrTimeout,
		core_errors.ErrCanceled,
		core_errors.ErrNotImplemented,
	}

	seen := make(map[string]bool)
	for _, err := range sentinels {
		if err == nil {
			t.Fatal("expected non-nil sentinel error")
		}
		msg := err.Error()
		if seen[msg] {
			t.Errorf("duplicate sentinel error message: %q", msg)
		}
		seen[msg] = true
	}
}

func TestReExportedFunctions(t *testing.T) {
	t.Run("Is and New", func(t *testing.T) {
		customErr := core_errors.New("custom error")
		wrapped := fmt.Errorf("wrapped: %w", customErr)

		if !core_errors.Is(wrapped, customErr) {
			t.Errorf("expected core_errors.Is to return true for wrapped error")
		}
		if core_errors.Is(wrapped, core_errors.ErrNotFound) {
			t.Errorf("expected core_errors.Is to return false for unrelated error")
		}
	})

	t.Run("Join and Unwrap", func(t *testing.T) {
		err1 := core_errors.ErrNotFound
		err2 := core_errors.ErrInvalidArgument
		joined := core_errors.Join(err1, err2)

		if !core_errors.Is(joined, core_errors.ErrNotFound) {
			t.Errorf("expected joined error to match ErrNotFound")
		}
		if !core_errors.Is(joined, core_errors.ErrInvalidArgument) {
			t.Errorf("expected joined error to match ErrInvalidArgument")
		}

		wrappedSingle := fmt.Errorf("wrap: %w", err1)
		if core_errors.Unwrap(wrappedSingle) != err1 {
			t.Errorf("expected Unwrap to return err1")
		}
	})

	t.Run("As", func(t *testing.T) {
		var target *testCustomError
		wrapped := fmt.Errorf("wrapped: %w", &testCustomError{msg: "hello"})
		if !core_errors.As(wrapped, &target) || target.msg != "hello" {
			t.Errorf("expected As to extract customTestError")
		}
	})
}

type testCustomError struct {
	msg string
}

func (e *testCustomError) Error() string {
	return e.msg
}

func TestFeatureErrorsWrapCore(t *testing.T) {
	tests := []struct {
		name       string
		featureErr error
		coreErr    error
	}{
		{"cardedit.ErrCardNotFound", cardedit_service.ErrCardNotFound, core_errors.ErrNotFound},
		{"cardedit.ErrAlreadyActive", cardedit_service.ErrAlreadyActive, core_errors.ErrConflict},
		{"cardedit.ErrQueueFull", cardedit_service.ErrQueueFull, core_errors.ErrQueueFull},
		{"statistics.ErrOperationNotFound", statistics_service.ErrOperationNotFound, core_errors.ErrNotFound},
		{"statistics.ErrActionNotFound", statistics_service.ErrActionNotFound, core_errors.ErrNotFound},
		{"statistics.ErrInvalidFilter", statistics_service.ErrInvalidFilter, core_errors.ErrInvalidArgument},
		{"transfer.ErrCapacityExceeded", transfer_service.ErrCapacityExceeded, core_errors.ErrCapacityExceeded},
		{"cabinetcopy.ErrNotEnoughCards", cabinetcopy_service.ErrNotEnoughCards, core_errors.ErrInvalidArgument},
		{"wbcabinet.ErrVerificationRateLimited", wbcabinet_service.ErrVerificationRateLimited, core_errors.ErrRateLimited},
		{"wbcabinet.ErrVerificationTransient", wbcabinet_service.ErrVerificationTransient, core_errors.ErrUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !core_errors.Is(tc.featureErr, tc.coreErr) {
				t.Errorf("expected %v to wrap %v", tc.featureErr, tc.coreErr)
			}
		})
	}
}

func TestWrap(t *testing.T) {
	if core_errors.Wrap(nil, "x") != nil || core_errors.Wrapf(nil, "x %d", 1) != nil {
		t.Fatal("expected nil for nil err")
	}
	base := core_errors.New("base")
	if !core_errors.Is(core_errors.Wrap(base, "ctx"), base) || !core_errors.Is(core_errors.Wrapf(base, "code %d", 1), base) {
		t.Fatal("expected wrapped error to match base")
	}
}

func TestConstructorsAndPredicates(t *testing.T) {
	cases := []struct {
		err  error
		pred func(error) bool
		base error
	}{
		{core_errors.NotFound("msg"), core_errors.IsNotFound, core_errors.ErrNotFound},
		{core_errors.InvalidArgument("msg"), core_errors.IsInvalidArgument, core_errors.ErrInvalidArgument},
		{core_errors.Conflict("msg"), core_errors.IsConflict, core_errors.ErrConflict},
		{core_errors.AlreadyExists("msg"), core_errors.IsConflict, core_errors.ErrAlreadyExists},
		{core_errors.Forbidden("msg"), core_errors.IsForbidden, core_errors.ErrForbidden},
		{core_errors.Unauthorized("msg"), core_errors.IsUnauthorized, core_errors.ErrUnauthorized},
		{core_errors.RateLimited("msg"), core_errors.IsRateLimited, core_errors.ErrRateLimited},
		{core_errors.Unavailable("msg"), core_errors.IsUnavailable, core_errors.ErrUnavailable},
		{core_errors.QueueFull("msg"), func(e error) bool { return core_errors.Is(e, core_errors.ErrQueueFull) }, core_errors.ErrQueueFull},
		{core_errors.CapacityExceeded("msg"), func(e error) bool { return core_errors.Is(e, core_errors.ErrCapacityExceeded) }, core_errors.ErrCapacityExceeded},
		{core_errors.Internal("msg"), func(e error) bool { return core_errors.Is(e, core_errors.ErrInternal) }, core_errors.ErrInternal},
		{core_errors.PreconditionFailed("msg"), func(e error) bool { return core_errors.Is(e, core_errors.ErrPreconditionFailed) }, core_errors.ErrPreconditionFailed},
	}
	for i, c := range cases {
		if !core_errors.Is(c.err, c.base) || !c.pred(c.err) {
			t.Errorf("case %d failed: err=%v, base=%v", i, c.err, c.base)
		}
	}
	if !core_errors.IsTimeout(core_errors.ErrTimeout) || !core_errors.IsCanceled(core_errors.ErrCanceled) {
		t.Fatal("context predicates failed")
	}
}

func TestRequire(t *testing.T) {
	err := core_errors.New("err")
	if core_errors.Require(true, err) != nil || !core_errors.Is(core_errors.Require(false, err), err) {
		t.Fatal("Require failed")
	}
	if !core_errors.IsInvalidArgument(core_errors.RequireContext(nil)) {
		t.Fatal("RequireContext(nil) failed")
	}
}
