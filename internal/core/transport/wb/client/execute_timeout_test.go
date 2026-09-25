package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	flowcontrol "github.com/ERONIS/wb-service/internal/core/transport/wb/flowcontrol"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	"go.uber.org/zap"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestExecuteTimeoutIncludesRateLimiterWait(t *testing.T) {
	const (
		cabinetID config.CabinetID = "cabinet"
		bucketID  policy.BucketID  = "test.bucket"
	)
	registry, err := flowcontrol.NewRegistry([]policy.BucketSpec{{
		ID:         bucketID,
		Interval:   time.Second,
		Burst:      1,
		MaxWaiters: 4,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Wait(context.Background(), cabinetID, bucketID); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int64
	baseURL, err := url.Parse("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	apiClient, err := NewAPIClient(
		cabinetID,
		"test",
		baseURL,
		&http.Client{
			Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Body:       http.NoBody,
				}, nil
			}),
			Timeout: 40 * time.Millisecond,
		},
		registry,
		time.Millisecond,
		1,
		zap.NewNop(),
	)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := policy.NewOperation(policy.OperationSpec{
		ID:              "test.timeout",
		Method:          http.MethodGet,
		Path:            "/test",
		BucketID:        bucketID,
		Kind:            policy.OperationKindRead,
		RetryMode:       policy.RetryModeNever,
		SuccessStatuses: []int{http.StatusNoContent},
		RequestMode:     policy.BodyModeNone,
		ResponseMode:    policy.BodyModeNone,
	})
	if err != nil {
		t.Fatal(err)
	}

	startedAt := time.Now()
	err = apiClient.Execute(context.Background(), operation, nil, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("execute error = %v, want deadline exceeded", err)
	}
	var classified ClassifiedError
	if !errors.As(err, &classified) {
		t.Fatalf("execute error %T is not classified", err)
	}
	if classified.Code() != ErrorCodeDeadlineExceeded || classified.Delivery() != NotDispatched {
		t.Fatalf("classification = (%s, %s)", classified.Code(), classified.Delivery())
	}
	if calls.Load() != 0 {
		t.Fatalf("HTTP transport called %d times while waiting for limiter", calls.Load())
	}
	if elapsed := time.Since(startedAt); elapsed > 300*time.Millisecond {
		t.Fatalf("execute exceeded total timeout: %v", elapsed)
	}
}
