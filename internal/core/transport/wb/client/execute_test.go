package client

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	flowcontrol "github.com/ERONIS/wb-service/internal/core/transport/wb/flowcontrol"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	"go.uber.org/zap"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestExecuteLimiterWaitIsOutsideHTTPTimeout(t *testing.T) {
	t.Parallel()

	const bucketID policy.BucketID = "test_upload"
	registry, err := flowcontrol.NewRegistry([]policy.BucketSpec{{
		ID:         bucketID,
		Interval:   60 * time.Millisecond,
		Burst:      1,
		MaxWaiters: 4,
	}})
	if err != nil {
		t.Fatalf("create rate limiter registry: %v", err)
	}
	operation, err := policy.NewOperation(policy.OperationSpec{
		ID:               "test.upload",
		Method:           http.MethodPost,
		Path:             "/upload",
		BucketID:         bucketID,
		Kind:             policy.OperationKindMutation,
		RetryMode:        policy.RetryModeNever,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeNone,
		MaxRequestBytes:  0,
		MaxResponseBytes: 0,
	})
	if err != nil {
		t.Fatalf("create operation: %v", err)
	}
	baseURL, err := url.Parse("https://example.test")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	httpClient := &http.Client{
		Timeout: 10 * time.Millisecond,
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    request,
			}, nil
		}),
	}
	apiClient, err := NewAPIClient(
		config.CabinetID("test"),
		"Test",
		baseURL,
		httpClient,
		registry,
		time.Millisecond,
		1,
		zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("create API client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := apiClient.Execute(ctx, operation, nil, nil, nil); err != nil {
		t.Fatalf("consume burst token: %v", err)
	}

	startedAt := time.Now()
	if err := apiClient.Execute(ctx, operation, nil, nil, nil); err != nil {
		t.Fatalf("execute after limiter wait: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed < 40*time.Millisecond {
		t.Fatalf("limiter wait was unexpectedly short: %v", elapsed)
	}
}
