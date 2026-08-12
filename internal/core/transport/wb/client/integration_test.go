package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
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

func TestAPIClientIntegrationRetryThenSuccess(t *testing.T) {
	t.Parallel()

	const token = "integration-token"
	var calls atomic.Int32

	handler := http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if got := request.Header.Get("Authorization"); got != token {
			t.Errorf("Authorization = %q, want %q", got, token)
		}

		attempt := calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		if attempt == 1 {
			writer.Header().Set("Retry-After", "0")
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = writer.Write([]byte(`{}`))

			return
		}

		_, _ = writer.Write([]byte(`{"value":"from-server"}`))
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local TCP listener is unavailable: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	operation, err := policy.NewOperation(policy.OperationSpec{
		ID:               "integration.retry",
		Method:           http.MethodGet,
		Path:             "/retry",
		BucketID:         executorTestBucket,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatalf("create operation: %v", err)
	}
	registry, err := flowcontrol.NewRegistry([]policy.BucketSpec{{
		ID:         executorTestBucket,
		Interval:   time.Nanosecond,
		Burst:      5,
		MaxWaiters: 5,
	}})
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	baseHTTPClient := server.Client()
	baseHTTPClient.Transport = transport.Chain(
		baseHTTPClient.Transport,
		transport.Authorization(token),
		transport.AttemptTrace(),
	)
	baseHTTPClient.Timeout = time.Second

	logCore, observed := observer.New(zapcore.InfoLevel)
	apiClient, err := NewAPIClient(
		config.CabinetID("integration"),
		"Integration",
		baseURL,
		baseHTTPClient,
		registry,
		time.Nanosecond,
		3,
		zap.New(logCore),
	)
	if err != nil {
		t.Fatalf("create API client: %v", err)
	}

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
	if target.Value != "from-server" {
		t.Fatalf("decoded value = %q", target.Value)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("server calls = %d, want 2", got)
	}

	fields := singleCompletionLog(t, observed)
	if got := fields["attempt_count"]; got != int64(2) {
		t.Fatalf("attempt count log = %v", got)
	}
	if got := fields["dispatch_count"]; got != int64(2) {
		t.Fatalf("dispatch count log = %v", got)
	}
	if got := fields["result"]; got != "success" {
		t.Fatalf("result log = %v", got)
	}

	for _, entry := range observed.All() {
		if strings.Contains(fmt.Sprint(entry.ContextMap()), token) ||
			strings.Contains(entry.Message, token) {
			t.Fatal("token leaked into logs")
		}
	}
}
