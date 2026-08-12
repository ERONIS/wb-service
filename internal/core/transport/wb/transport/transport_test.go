package transport

import (
	"context"
	"net/http"
	"net/http/httptrace"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}

type recordingAttempt struct {
	result AttemptResult
	calls  int
}

func (recorder *recordingAttempt) RecordAttempt(result AttemptResult) {
	recorder.calls++
	recorder.result = result
}

type closeableRoundTripper struct {
	closed bool
}

func (*closeableRoundTripper) RoundTrip(
	*http.Request,
) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
	}, nil
}

func (transport *closeableRoundTripper) CloseIdleConnections() {
	transport.closed = true
}

func TestAuthorizationClonesRequestBeforeSettingToken(t *testing.T) {
	t.Parallel()

	original, err := http.NewRequest(
		http.MethodGet,
		"https://content-api.wildberries.ru/test",
		nil,
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	original.Header.Set("X-Test", "original")

	const token = "opaque-token"
	wrapped := Authorization(token)(roundTripperFunc(
		func(request *http.Request) (*http.Response, error) {
			if got := request.Header.Get("Authorization"); got != token {
				t.Fatalf("Authorization = %q, want %q", got, token)
			}
			request.Header.Set("X-Test", "changed")

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       http.NoBody,
			}, nil
		},
	))

	if _, err := wrapped.RoundTrip(original); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if got := original.Header.Get("Authorization"); got != "" {
		t.Fatalf("original Authorization = %q", got)
	}
	if got := original.Header.Get("X-Test"); got != "original" {
		t.Fatalf("original X-Test = %q", got)
	}
}

func TestAttemptTraceRecordsDispatchForOnePhysicalAttempt(t *testing.T) {
	t.Parallel()

	recorder := &recordingAttempt{}
	base := roundTripperFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		clientTrace := httptrace.ContextClientTrace(request.Context())
		if clientTrace == nil || clientTrace.WroteRequest == nil {
			t.Fatal("WroteRequest trace is missing")
		}
		clientTrace.WroteRequest(httptrace.WroteRequestInfo{})

		return &http.Response{
			StatusCode: http.StatusAccepted,
			Body:       http.NoBody,
		}, nil
	})
	wrapper := AttemptTrace()(base)

	request, err := http.NewRequestWithContext(
		WithAttemptRecorder(context.Background(), recorder),
		http.MethodGet,
		"https://content-api.wildberries.ru/test",
		nil,
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	if _, err := wrapper.RoundTrip(request); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if recorder.calls != 1 {
		t.Fatalf("record calls = %d, want 1", recorder.calls)
	}
	if recorder.result.DispatchCount != 1 {
		t.Fatalf("dispatch count = %d, want 1", recorder.result.DispatchCount)
	}
	if recorder.result.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", recorder.result.StatusCode, http.StatusAccepted)
	}
}

func TestHTTPClientAndSharedTransportDoNotMutateBaseClient(t *testing.T) {
	t.Parallel()

	baseTransport := &closeableRoundTripper{}
	baseClient := &http.Client{
		Transport: baseTransport,
		Timeout:   3 * time.Second,
	}

	shared, err := NewSharedTransportFrom(baseClient.Transport)
	if err != nil {
		t.Fatalf("create shared transport: %v", err)
	}
	client, err := NewHTTPClient(baseClient, shared.RoundTripper())
	if err != nil {
		t.Fatalf("create HTTP client: %v", err)
	}

	if baseClient.CheckRedirect != nil {
		t.Fatal("base client CheckRedirect was mutated")
	}
	if client.CheckRedirect == nil {
		t.Fatal("cabinet client must deny redirects")
	}
	if client.Timeout != baseClient.Timeout {
		t.Fatalf("timeout = %s, want %s", client.Timeout, baseClient.Timeout)
	}

	shared.CloseIdleConnections()
	if !baseTransport.closed {
		t.Fatal("shared transport did not close idle connections")
	}
}
