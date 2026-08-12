package flowcontrol

import (
	"net/http"
	"strings"
	"testing"
	"time"

	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

const (
	testCabinetID config.CabinetID = "test"
	testBucketID  policy.BucketID  = "test_bucket"
)

func TestRegistryObserveReturnsLongestRetryDelay(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t)
	httpResponse := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"X-Ratelimit-Retry": []string{"2"},
			"Retry-After":       []string{"5"},
		},
	}

	retryAfter, err := registry.Observe(
		testCabinetID,
		testBucketID,
		httpResponse,
	)
	if err != nil {
		t.Fatalf("observe response: %v", err)
	}
	if retryAfter != 5*time.Second {
		t.Fatalf("retry delay = %s, want 5s", retryAfter)
	}
}

func TestRegistryObserveRejectsMalformedHeader(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t)
	httpResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"X-Ratelimit-Remaining": []string{"not-a-number"},
		},
	}

	_, err := registry.Observe(
		testCabinetID,
		testBucketID,
		httpResponse,
	)
	if err == nil || !strings.Contains(
		err.Error(),
		"X-Ratelimit-Remaining",
	) {
		t.Fatalf("Observe error = %v", err)
	}
}

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()

	registry, err := NewRegistry([]policy.BucketSpec{{
		ID:         testBucketID,
		Interval:   time.Nanosecond,
		Burst:      5,
		MaxWaiters: 5,
	}})
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	return registry
}
