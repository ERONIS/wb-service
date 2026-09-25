package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	flowcontrol "github.com/ERONIS/wb-service/internal/core/transport/wb/flowcontrol"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	"go.uber.org/zap"
)

type deadlineBody struct {
	ctx context.Context
}

func (body *deadlineBody) Read([]byte) (int, error) {
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (*deadlineBody) Close() error { return nil }

func TestExecuteRetriesInterruptedReadResponse(t *testing.T) {
	var calls atomic.Int64
	apiClient, operation := newResponseRetryTestClient(
		t,
		policy.OperationKindRead,
		policy.RetryModeReadSafe,
		3,
		func(request *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       &deadlineBody{ctx: request.Context()},
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}, nil
		},
	)

	var result struct {
		OK bool `json:"ok"`
	}
	if err := apiClient.Execute(context.Background(), operation, nil, nil, &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatal("successful retry response was not decoded")
	}
	if calls.Load() != 2 {
		t.Fatalf("HTTP transport called %d times, want 2", calls.Load())
	}
}

func TestExecuteDoesNotRetryMutationResponseTimeout(t *testing.T) {
	var calls atomic.Int64
	apiClient, operation := newResponseRetryTestClient(
		t,
		policy.OperationKindMutation,
		policy.RetryModeNever,
		3,
		func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       &deadlineBody{ctx: request.Context()},
			}, nil
		},
	)

	var result struct {
		OK bool `json:"ok"`
	}
	err := apiClient.Execute(context.Background(), operation, nil, nil, &result)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("execute error = %v, want deadline exceeded", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("HTTP transport called %d times, want 1", calls.Load())
	}
}

func TestExecuteClassifiesEmptyMutationResponse(t *testing.T) {
	var calls atomic.Int64
	apiClient, operation := newResponseRetryTestClient(
		t,
		policy.OperationKindMutation,
		policy.RetryModeNever,
		3,
		func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		},
	)

	var result struct {
		OK bool `json:"ok"`
	}
	err := apiClient.Execute(context.Background(), operation, nil, nil, &result)
	var classified ClassifiedError
	if !errors.As(err, &classified) {
		t.Fatalf("execute error = %v, want classified error", err)
	}
	if classified.Code() != ErrorCodeEmptyResponse {
		t.Fatalf("error code = %q, want %q", classified.Code(), ErrorCodeEmptyResponse)
	}
	if classified.Delivery() != ResponseReceived || classified.HTTPStatus() != http.StatusOK {
		t.Fatalf("unexpected empty response evidence: delivery=%v status=%d", classified.Delivery(), classified.HTTPStatus())
	}
	if calls.Load() != 1 {
		t.Fatalf("HTTP transport called %d times, want 1", calls.Load())
	}
}

func newResponseRetryTestClient(
	t *testing.T,
	kind policy.OperationKind,
	retryMode policy.RetryMode,
	maxReadAttempts int,
	roundTrip roundTripperFunc,
) (*APIClient, policy.Operation) {
	t.Helper()

	const bucketID policy.BucketID = "test.response-retry"
	registry, err := flowcontrol.NewRegistry([]policy.BucketSpec{{
		ID:         bucketID,
		Interval:   time.Millisecond,
		Burst:      1,
		MaxWaiters: 4,
	}})
	if err != nil {
		t.Fatal(err)
	}
	baseURL, err := url.Parse("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	apiClient, err := NewAPIClient(
		config.CabinetID("cabinet"),
		"test",
		baseURL,
		&http.Client{Transport: roundTrip, Timeout: 90 * time.Millisecond},
		registry,
		time.Millisecond,
		maxReadAttempts,
		zap.NewNop(),
	)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := policy.NewOperation(policy.OperationSpec{
		ID:               "test.response-retry",
		Method:           http.MethodGet,
		Path:             "/test",
		BucketID:         bucketID,
		Kind:             kind,
		RetryMode:        retryMode,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}

	return apiClient, operation
}
