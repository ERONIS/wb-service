package core_transport_wb_ratelimit

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const (
	headerRateLimitRemaining = "X-Ratelimit-Remaining"
	headerRateLimitRetry     = "X-Ratelimit-Retry"
	headerRateLimitLimit     = "X-Ratelimit-Limit"
	headerRateLimitReset     = "X-Ratelimit-Reset"
)

type rateLimitHeaderValues struct {
	remaining    *int
	retrySeconds *int
	burstLimit   *int
	resetSeconds *int
}

func parseRateLimitHeaders(
	headers http.Header,
) (rateLimitHeaderValues, error) {
	remaining, err := parseRateLimitHeader(
		headers,
		headerRateLimitRemaining,
	)
	if err != nil {
		return rateLimitHeaderValues{}, err
	}

	retrySeconds, err := parseRateLimitHeader(
		headers,
		headerRateLimitRetry,
	)
	if err != nil {
		return rateLimitHeaderValues{}, err
	}

	burstLimit, err := parseRateLimitHeader(
		headers,
		headerRateLimitLimit,
	)
	if err != nil {
		return rateLimitHeaderValues{}, err
	}

	resetSeconds, err := parseRateLimitHeader(
		headers,
		headerRateLimitReset,
	)
	if err != nil {
		return rateLimitHeaderValues{}, err
	}

	return rateLimitHeaderValues{
		remaining:    remaining,
		retrySeconds: retrySeconds,
		burstLimit:   burstLimit,
		resetSeconds: resetSeconds,
	}, nil
}
func parseRateLimitHeader(
	headers http.Header,
	name string,
) (*int, error) {
	rawValue := readRateLimitHeader(headers, name)

	if rawValue == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(rawValue)
	if err != nil {
		return nil, fmt.Errorf(
			"parse WB rate limit header %q value %q: %w",
			name,
			rawValue,
			err,
		)
	}

	return &value, nil

}

func readRateLimitHeader(
	headers http.Header,
	name string,
) string {
	return strings.TrimSpace(headers.Get(name))
}
