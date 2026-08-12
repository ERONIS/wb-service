package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
)

var errAttemptRecorderMissing = errors.New(
	"WB attempt recorder is missing",
)

type AttemptResult struct {
	StatusCode    int
	DispatchCount int
}

type AttemptRecorder interface {
	RecordAttempt(result AttemptResult)
}

type attemptRecorderContextKey struct{}

type attemptTraceRoundTripper struct {
	next http.RoundTripper
}

// WithAttemptRecorder добавляет recorder логического запроса в context.
func WithAttemptRecorder(
	ctx context.Context,
	recorder AttemptRecorder,
) context.Context {
	return context.WithValue(
		ctx,
		attemptRecorderContextKey{},
		recorder,
	)
}

// AttemptTrace собирает результат одного вызова transport pipeline.
func AttemptTrace() WrapperFunc {
	return func(next http.RoundTripper) http.RoundTripper {
		return &attemptTraceRoundTripper{
			next: next,
		}
	}
}

func (transport *attemptTraceRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	recorder, ok := request.Context().
		Value(attemptRecorderContextKey{}).(AttemptRecorder)

	if !ok || recorder == nil {
		return nil, errAttemptRecorderMissing
	}

	var dispatchCount atomic.Int64

	clientTrace := &httptrace.ClientTrace{
		WroteRequest: func(
			_ httptrace.WroteRequestInfo,
		) {
			dispatchCount.Add(1)
		},
	}

	requestContext := httptrace.WithClientTrace(
		request.Context(),
		clientTrace,
	)
	requestCopy := request.Clone(requestContext)

	response, err := transport.next.RoundTrip(requestCopy)

	result := AttemptResult{
		DispatchCount: int(dispatchCount.Load()),
	}

	if response != nil {
		result.StatusCode = response.StatusCode
	}

	recorder.RecordAttempt(result)

	return response, err
}
