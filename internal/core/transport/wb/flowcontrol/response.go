package flowcontrol

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	headerRetryAfter         = "Retry-After"
	headerRateLimitRemaining = "X-Ratelimit-Remaining"
	headerRateLimitRetry     = "X-Ratelimit-Retry"
	headerRateLimitLimit     = "X-Ratelimit-Limit"
)

var errRateLimitResponseRequired = errors.New(
	"rate-limit HTTP response is required",
)

type responseRateLimits struct {
	statusCode int
	remaining  *int
	retryAfter *time.Duration
	limit      *int
}

func parseResponseRateLimits(
	response *http.Response,
) (responseRateLimits, error) {
	if response == nil {
		return responseRateLimits{},
			errRateLimitResponseRequired
	}

	remaining, err := parseOptionalIntHeader(
		response.Header,
		headerRateLimitRemaining,
	)
	if err != nil {
		return responseRateLimits{}, err
	}

	retryAfter, err := parseRetryAfterHeaders(
		response.Header,
		time.Now(),
	)
	if err != nil {
		return responseRateLimits{}, err
	}

	limit, err := parseOptionalIntHeader(
		response.Header,
		headerRateLimitLimit,
	)
	if err != nil {
		return responseRateLimits{}, err
	}

	return responseRateLimits{
		statusCode: response.StatusCode,
		remaining:  remaining,
		retryAfter: retryAfter,
		limit:      limit,
	}, nil
}

func parseRetryAfterHeaders(
	header http.Header,
	now time.Time,
) (*time.Duration, error) {
	wbRetryAfter, err := parseOptionalDurationHeader(
		header,
		headerRateLimitRetry,
	)
	if err != nil {
		return nil, err
	}

	retryAfter, err := parseOptionalRetryAfterHeader(
		header,
		now,
	)
	if err != nil {
		return nil, err
	}

	return longerDuration(
		wbRetryAfter,
		retryAfter,
	), nil
}

func parseOptionalIntHeader(
	header http.Header,
	name string,
) (*int, error) {
	value, exists, err := singleHeaderValue(
		header,
		name,
	)
	if err != nil || !exists {
		return nil, err
	}

	parsed, err := parseHeaderUint(value, name)
	if err != nil {
		return nil, err
	}
	if parsed > uint64(maxInt()) {
		return nil, fmt.Errorf(
			"WB response header %s exceeds int range",
			name,
		)
	}

	result := int(parsed)

	return &result, nil
}

func parseOptionalDurationHeader(
	header http.Header,
	name string,
) (*time.Duration, error) {
	value, exists, err := singleHeaderValue(
		header,
		name,
	)
	if err != nil || !exists {
		return nil, err
	}

	result, err := parseDurationSeconds(value, name)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func parseOptionalRetryAfterHeader(
	header http.Header,
	now time.Time,
) (*time.Duration, error) {
	value, exists, err := singleHeaderValue(
		header,
		headerRetryAfter,
	)
	if err != nil || !exists {
		return nil, err
	}

	if containsOnlyDecimalDigits(value) {
		result, err := parseDurationSeconds(
			value,
			headerRetryAfter,
		)
		if err != nil {
			return nil, err
		}

		return &result, nil
	}

	retryAt, err := http.ParseTime(value)
	if err != nil {
		return nil, fmt.Errorf(
			"WB response header %s must contain decimal seconds or an HTTP date",
			headerRetryAfter,
		)
	}

	result := retryAt.Sub(now)
	if result < 0 {
		result = 0
	}

	return &result, nil
}

func parseDurationSeconds(
	value string,
	name string,
) (time.Duration, error) {
	seconds, err := parseHeaderUint(value, name)
	if err != nil {
		return 0, err
	}

	const maxDuration = time.Duration(1<<63 - 1)
	maxSeconds := uint64(maxDuration / time.Second)
	if seconds > maxSeconds {
		return 0, fmt.Errorf(
			"WB response header %s exceeds duration range",
			name,
		)
	}

	return time.Duration(seconds) * time.Second, nil
}

func singleHeaderValue(
	header http.Header,
	name string,
) (string, bool, error) {
	values := header.Values(name)
	if len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", false, fmt.Errorf(
			"WB response header %s must have one value",
			name,
		)
	}

	value := strings.Trim(values[0], " \t")
	if value == "" {
		return "", false, fmt.Errorf(
			"WB response header %s is empty",
			name,
		)
	}

	return value, true, nil
}

func parseHeaderUint(
	value string,
	name string,
) (uint64, error) {
	if !containsOnlyDecimalDigits(value) {
		return 0, fmt.Errorf(
			"WB response header %s must contain decimal digits",
			name,
		)
	}

	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf(
			"WB response header %s exceeds uint64 range",
			name,
		)
	}

	return parsed, nil
}

func containsOnlyDecimalDigits(value string) bool {
	if value == "" {
		return false
	}

	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}

	return true
}

func longerDuration(
	first *time.Duration,
	second *time.Duration,
) *time.Duration {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}

	result := *first
	if *second > result {
		result = *second
	}

	return &result
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
