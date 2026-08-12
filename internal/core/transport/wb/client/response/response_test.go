package response

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

type trackingBody struct {
	io.Reader
	closed   bool
	closeErr error
}

func (body *trackingBody) Close() error {
	body.closed = true

	return body.closeErr
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestReadAndCloseEnforcesBoundAndClosesBody(t *testing.T) {
	t.Parallel()

	body := &trackingBody{Reader: strings.NewReader("12345")}
	httpResponse := &http.Response{
		Body:          body,
		ContentLength: -1,
	}

	_, err := ReadAndClose(httpResponse, 4)
	if err == nil || !strings.Contains(err.Error(), "exceeds 4 bytes") {
		t.Fatalf("ReadAndClose error = %v", err)
	}
	if !body.closed {
		t.Fatal("response body was not closed")
	}
}

func TestReadAndCloseJoinsCloseError(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("close failed")
	body := &trackingBody{
		Reader:   strings.NewReader(`{"ok":true}`),
		closeErr: closeErr,
	}

	result, err := ReadAndClose(&http.Response{Body: body}, 1024)
	if string(result) != `{"ok":true}` {
		t.Fatalf("body = %q", result)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("ReadAndClose error = %v, want close error", err)
	}
}

func TestDecodeDoesNotMutateTargetOnFailure(t *testing.T) {
	t.Parallel()

	target := struct {
		Value string `json:"value"`
	}{Value: "original"}

	err := Decode([]byte(`{"value":`), &target)
	if err == nil {
		t.Fatal("Decode unexpectedly succeeded")
	}
	if target.Value != "original" {
		t.Fatalf("target was mutated: %+v", target)
	}
}

func TestRetryClassifiers(t *testing.T) {
	t.Parallel()

	statusTests := map[int]bool{
		http.StatusTooManyRequests:     true,
		http.StatusInternalServerError: true,
		http.StatusBadGateway:          true,
		http.StatusServiceUnavailable:  true,
		http.StatusGatewayTimeout:      true,
		http.StatusBadRequest:          false,
		http.StatusOK:                  false,
	}
	for status, want := range statusTests {
		if got := IsRetryableStatus(status); got != want {
			t.Errorf("IsRetryableStatus(%d) = %v, want %v", status, got, want)
		}
	}

	errorTests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "timeout", err: timeoutError{}, want: true},
		{name: "EOF", err: io.EOF, want: true},
		{name: "closed", err: net.ErrClosed, want: true},
		{name: "canceled", err: context.Canceled, want: false},
		{name: "deadline", err: context.DeadlineExceeded, want: false},
		{name: "ordinary", err: errors.New("ordinary"), want: false},
	}
	for _, test := range errorTests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := IsRetryableTransportError(test.err); got != test.want {
				t.Fatalf("IsRetryableTransportError() = %v, want %v", got, test.want)
			}
		})
	}
}
