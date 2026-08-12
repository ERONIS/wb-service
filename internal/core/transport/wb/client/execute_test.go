package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	flowcontrol "github.com/ERONIS/wb-service/internal/core/transport/wb/flowcontrol"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	transport "github.com/ERONIS/wb-service/internal/core/transport/wb/transport"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const executorTestBucket policy.BucketID = "executor_test_bucket"

type executorResponse struct {
	Value string `json:"value"`
}

type scriptedStep struct {
	statusCode int
	body       string
	header     http.Header
	err        error
	dispatched bool
}

type scriptedRoundTripper struct {
	mutex sync.Mutex
	steps []scriptedStep
	calls int
}

func (transport *scriptedRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	transport.mutex.Lock()
	defer transport.mutex.Unlock()

	if transport.calls >= len(transport.steps) {
		return nil, errors.New("unexpected HTTP attempt")
	}

	step := transport.steps[transport.calls]
	transport.calls++

	if step.dispatched {
		clientTrace := httptrace.ContextClientTrace(request.Context())
		if clientTrace != nil && clientTrace.WroteRequest != nil {
			clientTrace.WroteRequest(httptrace.WroteRequestInfo{})
		}
	}

	if step.statusCode == 0 {
		return nil, step.err
	}

	return &http.Response{
		StatusCode:    step.statusCode,
		Header:        step.header.Clone(),
		Body:          io.NopCloser(strings.NewReader(step.body)),
		ContentLength: int64(len(step.body)),
		Request:       request,
	}, step.err
}

func (transport *scriptedRoundTripper) callCount() int {
	transport.mutex.Lock()
	defer transport.mutex.Unlock()

	return transport.calls
}

func TestAPIClientExecuteSuccessAndDecodeFailureLogging(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		body          string
		wantCode      string
		wantValue     string
		wantResultLog string
	}{
		{
			name:          "success",
			body:          `{"value":"decoded"}`,
			wantValue:     "decoded",
			wantResultLog: "success",
		},
		{
			name:          "decode failure",
			body:          `{"value":`,
			wantCode:      ErrorCodeInvalidResponse,
			wantResultLog: "error",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			script := &scriptedRoundTripper{steps: []scriptedStep{{
				statusCode: http.StatusOK,
				body:       test.body,
				dispatched: true,
			}}}
			apiClient, operation, observed := newExecutorTestClient(
				t,
				script,
				policy.OperationKindRead,
				policy.RetryModeReadSafe,
				3,
				1024,
			)

			target := executorResponse{Value: "original"}
			err := apiClient.Execute(
				context.Background(),
				operation,
				nil,
				nil,
				&target,
			)

			if test.wantCode == "" {
				if err != nil {
					t.Fatalf("Execute error: %v", err)
				}
				if target.Value != test.wantValue {
					t.Fatalf("decoded value = %q, want %q", target.Value, test.wantValue)
				}
			} else {
				assertClassifiedError(
					t,
					err,
					test.wantCode,
					ResponseReceived,
				)
				if target.Value != "original" {
					t.Fatalf("target mutated after decode error: %+v", target)
				}
			}

			fields := singleCompletionLog(t, observed)
			if got := fields["result"]; got != test.wantResultLog {
				t.Fatalf("result log = %#v, want %q", got, test.wantResultLog)
			}
			if got := fields["error_code"]; got != test.wantCode {
				t.Fatalf("error code log = %#v, want %q", got, test.wantCode)
			}
			if got := fields["attempt_count"]; got != int64(1) {
				t.Fatalf("attempt count log = %#v, want 1", got)
			}
		})
	}
}

func TestAPIClientExecuteRetriesSafeRead(t *testing.T) {
	t.Parallel()

	script := &scriptedRoundTripper{steps: []scriptedStep{
		{
			statusCode: http.StatusTooManyRequests,
			body:       `{}`,
			header: http.Header{
				"Retry-After": []string{"0"},
			},
			dispatched: true,
		},
		{
			statusCode: http.StatusOK,
			body:       `{"value":"after-retry"}`,
			dispatched: true,
		},
	}}
	apiClient, operation, observed := newExecutorTestClient(
		t,
		script,
		policy.OperationKindRead,
		policy.RetryModeReadSafe,
		3,
		1024,
	)

	target := executorResponse{}
	if err := apiClient.Execute(
		context.Background(),
		operation,
		nil,
		nil,
		&target,
	); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if target.Value != "after-retry" {
		t.Fatalf("decoded value = %q", target.Value)
	}
	if script.callCount() != 2 {
		t.Fatalf("HTTP calls = %d, want 2", script.callCount())
	}

	fields := singleCompletionLog(t, observed)
	if got := fields["attempt_count"]; got != int64(2) {
		t.Fatalf("attempt count log = %#v, want 2", got)
	}
}

func TestAPIClientExecuteNeverRetriesMutation(t *testing.T) {
	t.Parallel()

	script := &scriptedRoundTripper{steps: []scriptedStep{{
		statusCode: http.StatusTooManyRequests,
		body:       `{}`,
		dispatched: true,
	}}}
	apiClient, operation, observed := newExecutorTestClient(
		t,
		script,
		policy.OperationKindMutation,
		policy.RetryModeNever,
		5,
		1024,
	)

	err := apiClient.Execute(
		context.Background(),
		operation,
		nil,
		nil,
		&executorResponse{},
	)
	assertClassifiedError(
		t,
		err,
		ErrorCodeRateLimited,
		ResponseReceived,
	)
	if script.callCount() != 1 {
		t.Fatalf("mutation HTTP calls = %d, want 1", script.callCount())
	}
	singleCompletionLog(t, observed)
}

func TestAPIClientExecutePreservesPreviousDeliveryOnPreDispatchRetryFailure(
	t *testing.T,
) {
	t.Parallel()

	script := &scriptedRoundTripper{steps: []scriptedStep{
		{
			statusCode: http.StatusServiceUnavailable,
			body:       `{}`,
			dispatched: true,
		},
		{
			err:        errors.New("failed before dispatch"),
			dispatched: false,
		},
	}}
	apiClient, operation, observed := newExecutorTestClient(
		t,
		script,
		policy.OperationKindRead,
		policy.RetryModeReadSafe,
		3,
		1024,
	)

	err := apiClient.Execute(
		context.Background(),
		operation,
		nil,
		nil,
		&executorResponse{},
	)
	assertClassifiedError(
		t,
		err,
		ErrorCodeRetryInterrupted,
		ResponseReceived,
	)
	if script.callCount() != 2 {
		t.Fatalf("HTTP calls = %d, want 2", script.callCount())
	}

	fields := singleCompletionLog(t, observed)
	if got := fields["status_code"]; got != int64(http.StatusServiceUnavailable) {
		t.Fatalf("final status log = %#v, want 503", got)
	}
	if got := fields["delivery_state"]; got != "response_received" {
		t.Fatalf("delivery log = %#v", got)
	}
}

func TestAPIClientExecuteClassifiesDispatchedTransportFailureAsUnknown(
	t *testing.T,
) {
	t.Parallel()

	script := &scriptedRoundTripper{steps: []scriptedStep{{
		err:        errors.New("connection lost"),
		dispatched: true,
	}}}
	apiClient, operation, observed := newExecutorTestClient(
		t,
		script,
		policy.OperationKindMutation,
		policy.RetryModeNever,
		5,
		1024,
	)

	err := apiClient.Execute(
		context.Background(),
		operation,
		nil,
		nil,
		&executorResponse{},
	)
	assertClassifiedError(
		t,
		err,
		ErrorCodeTransport,
		UnknownDelivery,
	)
	if script.callCount() != 1 {
		t.Fatalf("HTTP calls = %d, want 1", script.callCount())
	}
	singleCompletionLog(t, observed)
}

func TestAPIClientExecuteKeepsSuccessfulResultOnObservationError(t *testing.T) {
	t.Parallel()

	script := &scriptedRoundTripper{steps: []scriptedStep{{
		statusCode: http.StatusOK,
		body:       `{"value":"ok"}`,
		header: http.Header{
			"X-Ratelimit-Remaining": []string{"invalid"},
		},
		dispatched: true,
	}}}
	apiClient, operation, observed := newExecutorTestClient(
		t,
		script,
		policy.OperationKindRead,
		policy.RetryModeReadSafe,
		3,
		1024,
	)

	target := executorResponse{}
	if err := apiClient.Execute(
		context.Background(),
		operation,
		nil,
		nil,
		&target,
	); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if target.Value != "ok" {
		t.Fatalf("decoded value = %q", target.Value)
	}

	fields := singleCompletionLog(t, observed)
	if got := fields["rate_limit_observation_errors"]; got != int64(1) {
		t.Fatalf("observation error count = %#v, want 1", got)
	}
	if got := fields["result"]; got != "success" {
		t.Fatalf("result log = %#v, want success", got)
	}
}

func TestAPIClientExecuteEnforcesResponseBound(t *testing.T) {
	t.Parallel()

	script := &scriptedRoundTripper{steps: []scriptedStep{{
		statusCode: http.StatusOK,
		body:       `12345`,
		dispatched: true,
	}}}
	apiClient, operation, observed := newExecutorTestClient(
		t,
		script,
		policy.OperationKindRead,
		policy.RetryModeNever,
		1,
		4,
	)

	err := apiClient.Execute(
		context.Background(),
		operation,
		nil,
		nil,
		&executorResponse{},
	)
	assertClassifiedError(
		t,
		err,
		ErrorCodeInvalidResponse,
		ResponseReceived,
	)
	singleCompletionLog(t, observed)
}

func TestAPIClientExecuteValidatesTargetBeforeDispatch(t *testing.T) {
	t.Parallel()

	script := &scriptedRoundTripper{}
	apiClient, operation, observed := newExecutorTestClient(
		t,
		script,
		policy.OperationKindRead,
		policy.RetryModeReadSafe,
		3,
		1024,
	)

	err := apiClient.Execute(
		context.Background(),
		operation,
		nil,
		nil,
		nil,
	)
	assertClassifiedError(
		t,
		err,
		ErrorCodeInvalidResultTarget,
		NotDispatched,
	)
	if script.callCount() != 0 {
		t.Fatalf("HTTP calls = %d, want 0", script.callCount())
	}

	fields := singleCompletionLog(t, observed)
	if got := fields["attempt_count"]; got != int64(0) {
		t.Fatalf("attempt count log = %#v, want 0", got)
	}
}

func TestAPIClientExecuteContextInterruptsServerBackoff(t *testing.T) {
	t.Parallel()

	script := &scriptedRoundTripper{steps: []scriptedStep{{
		statusCode: http.StatusTooManyRequests,
		body:       `{}`,
		header: http.Header{
			"Retry-After": []string{"60"},
		},
		dispatched: true,
	}}}
	apiClient, operation, observed := newExecutorTestClient(
		t,
		script,
		policy.OperationKindRead,
		policy.RetryModeReadSafe,
		3,
		1024,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := apiClient.Execute(ctx, operation, nil, nil, &executorResponse{})
	assertClassifiedError(
		t,
		err,
		ErrorCodeRetryInterrupted,
		ResponseReceived,
	)
	if script.callCount() != 1 {
		t.Fatalf("HTTP calls = %d, want 1", script.callCount())
	}

	fields := singleCompletionLog(t, observed)
	if got := fields["status_code"]; got != int64(http.StatusTooManyRequests) {
		t.Fatalf("status log = %#v, want 429", got)
	}
}

func newExecutorTestClient(
	t *testing.T,
	roundTripper http.RoundTripper,
	kind policy.OperationKind,
	retryMode policy.RetryMode,
	maxAttempts int,
	maxResponseBytes int64,
) (*APIClient, policy.Operation, *observer.ObservedLogs) {
	t.Helper()

	operation, err := policy.NewOperation(policy.OperationSpec{
		ID:               "executor.test",
		Method:           http.MethodPost,
		Path:             "/test",
		BucketID:         executorTestBucket,
		Kind:             kind,
		RetryMode:        retryMode,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: maxResponseBytes,
	})
	if err != nil {
		t.Fatalf("create operation: %v", err)
	}

	registry, err := flowcontrol.NewRegistry([]policy.BucketSpec{{
		ID:         executorTestBucket,
		Interval:   time.Nanosecond,
		Burst:      100,
		MaxWaiters: 10,
	}})
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	baseURL, err := url.Parse("https://content-api.wildberries.ru")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	logCore, observed := observer.New(zapcore.InfoLevel)
	logger := zap.New(logCore)

	apiClient, err := NewAPIClient(
		config.CabinetID("test"),
		"Test Cabinet",
		baseURL,
		&http.Client{
			Transport: transport.Chain(
				roundTripper,
				transport.AttemptTrace(),
			),
			Timeout: time.Second,
		},
		registry,
		time.Nanosecond,
		maxAttempts,
		logger,
	)
	if err != nil {
		t.Fatalf("create API client: %v", err)
	}

	return apiClient, operation, observed
}

func assertClassifiedError(
	t *testing.T,
	err error,
	wantCode string,
	wantDelivery DeliveryState,
) {
	t.Helper()

	if err == nil {
		t.Fatal("expected classified error, got nil")
	}

	var classifiedErr ClassifiedError
	if !errors.As(err, &classifiedErr) {
		t.Fatalf("error %T is not ClassifiedError: %v", err, err)
	}
	if classifiedErr.Code() != wantCode {
		t.Fatalf("error code = %q, want %q", classifiedErr.Code(), wantCode)
	}
	if classifiedErr.Delivery() != wantDelivery {
		t.Fatalf(
			"delivery = %s, want %s",
			classifiedErr.Delivery(),
			wantDelivery,
		)
	}
}

func singleCompletionLog(
	t *testing.T,
	observed *observer.ObservedLogs,
) map[string]any {
	t.Helper()

	entries := observed.FilterMessage("WB request completed").All()
	if len(entries) != 1 {
		t.Fatalf("completion logs = %d, want 1", len(entries))
	}

	return entries[0].ContextMap()
}
