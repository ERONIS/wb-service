package policy

import (
	"fmt"
	"time"
)

type RetryMode string

const (
	RetryModeNever    RetryMode = "never"
	RetryModeReadSafe RetryMode = "read_safe"
	DefaultMaxReadAttempts = 3
	DefaultRetryBaseDelay = 500 * time.Millisecond
)

func (mode RetryMode) AllowsRetry() bool {
	return mode == RetryModeReadSafe
}

func validateSuccessStatuses(statuses []int) error {
	if len(statuses) == 0 {
		return fmt.Errorf("success status list is empty")
	}

	seen := make(map[int]struct{}, len(statuses))

	for _, status := range statuses {
		if status < 200 || status > 299 {
			return fmt.Errorf(
				"success status %d is outside 2xx range",
				status,
			)
		}

		if _, exists := seen[status]; exists {
			return fmt.Errorf(
				"success status %d is duplicated",
				status,
			)
		}

		seen[status] = struct{}{}
	}

	return nil
}

func containsSuccessStatus(
	statuses []int,
	status int,
) bool {
	for _, allowedStatus := range statuses {
		if status == allowedStatus {
			return true
		}
	}

	return false
}
