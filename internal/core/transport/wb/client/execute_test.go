package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptrace"
	"slices"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/transport/wb/client/request"
	"github.com/ERONIS/wb-service/internal/core/transport/wb/flowcontrol"
	"github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	"go.uber.org/zap"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestDoAttemptDeliveryAfterTransportError(t *testing.T) {
	operation, prepared := preparedTestRequest(t)

	for _, testCase := range []struct {
		name              string
		writeRequest      bool
		wantDelivery      DeliveryState
		wantDispatchCount int
	}{
		{name: "before dispatch", wantDelivery: NotDispatched},
		{name: "after dispatch", writeRequest: true, wantDelivery: UnknownDelivery, wantDispatchCount: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client := &APIClient{httpClient: &http.Client{
				Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					if testCase.writeRequest {
						clientTrace := httptrace.ContextClientTrace(request.Context())
						if clientTrace == nil || clientTrace.WroteRequest == nil {
							t.Fatal("request dispatch trace is missing")
						}
						clientTrace.WroteRequest(httptrace.WroteRequestInfo{})
					}
					return nil, errors.New("transport failure")
				}),
			}}
			trace := &requestTrace{maxAttempts: 1}

			result := client.doAttempt(context.Background(), prepared, operation, trace)
			if result.err == nil {
				t.Fatal("expected transport error")
			}
			if result.delivery != testCase.wantDelivery {
				t.Errorf("delivery = %v, want %v", result.delivery, testCase.wantDelivery)
			}
			if trace.attemptCount != 1 || trace.dispatchCount != testCase.wantDispatchCount {
				t.Errorf("attempts = %d, dispatches = %d; want 1, %d",
					trace.attemptCount, trace.dispatchCount, testCase.wantDispatchCount)
			}
		})
	}
}

func TestAPIClientsUseTheirOwnTokens(t *testing.T) {
	operation, prepared := preparedTestRequest(t)
	wantTokens := []string{"first-token", "second-token"}
	var gotTokens []string
	sharedHTTPClient := &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			gotTokens = append(gotTokens, request.Header.Get("Authorization"))
			return nil, errors.New("transport failure")
		}),
	}

	for _, token := range wantTokens {
		client, err := NewAPIClient(
			"00000000-0000-0000-0000-000000000001",
			"cabinet",
			token,
			sharedHTTPClient,
			&flowcontrol.Registry{},
			zap.NewNop(),
		)
		if err != nil {
			t.Fatal(err)
		}
		result := client.doAttempt(
			context.Background(), prepared, operation, &requestTrace{maxAttempts: 1},
		)
		if result.err == nil {
			t.Fatal("expected transport error")
		}
	}

	if !slices.Equal(gotTokens, wantTokens) {
		t.Errorf("authorization tokens = %v, want %v", gotTokens, wantTokens)
	}
}

func preparedTestRequest(t *testing.T) (policy.Operation, request.Prepared) {
	t.Helper()
	operation, err := policy.NewOperation(policy.OperationSpec{
		ID:              "delivery_test",
		Method:          http.MethodPost,
		Host:            "content-api.wildberries.ru",
		Path:            "/ping",
		BucketID:        "delivery_test",
		Kind:            policy.OperationKindMutation,
		RetryMode:       policy.RetryModeNever,
		SuccessStatuses: []int{http.StatusOK},
		RequestMode:     policy.BodyModeNone,
		ResponseMode:    policy.BodyModeNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := request.Prepare(operation, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return operation, prepared
}
