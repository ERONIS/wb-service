package request

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

type testQuery struct {
	Locale string `url:"locale,omitempty"`
	Limit  int    `url:"limit,omitempty"`
}

type testBody struct {
	Values []string `json:"values"`
}

func TestPrepareBuildsStableRequestForEveryAttempt(t *testing.T) {
	t.Parallel()

	baseURL, err := url.Parse("https://content-api.wildberries.ru")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}

	operation := mustTestOperation(
		t,
		http.MethodPost,
		policy.BodyModeJSON,
		1024,
	)
	body := testBody{Values: []string{"first"}}

	prepared, err := Prepare(
		baseURL,
		operation,
		testQuery{Locale: "ru", Limit: 10},
		body,
	)
	if err != nil {
		t.Fatalf("prepare request: %v", err)
	}

	body.Values[0] = "changed-after-prepare"

	for attempt := 1; attempt <= 2; attempt++ {
		httpRequest, err := prepared.NewHTTPRequest(context.Background())
		if err != nil {
			t.Fatalf("build attempt %d: %v", attempt, err)
		}

		if got, want := httpRequest.URL.String(),
			"https://content-api.wildberries.ru/test?limit=10&locale=ru"; got != want {
			t.Fatalf("attempt %d URL = %q, want %q", attempt, got, want)
		}
		if got := httpRequest.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("attempt %d content type = %q", attempt, got)
		}

		bodyBytes, err := io.ReadAll(httpRequest.Body)
		if err != nil {
			t.Fatalf("read attempt %d body: %v", attempt, err)
		}
		if got, want := string(bodyBytes), `{"values":["first"]}`; got != want {
			t.Fatalf("attempt %d body = %s, want %s", attempt, got, want)
		}
	}
}

func TestPrepareRejectsInvalidBodyUsage(t *testing.T) {
	t.Parallel()

	baseURL, err := url.Parse("https://content-api.wildberries.ru")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}

	tests := []struct {
		name      string
		operation policy.Operation
		body      any
		contains  string
	}{
		{
			name: "body forbidden",
			operation: mustTestOperation(
				t,
				http.MethodGet,
				policy.BodyModeNone,
				0,
			),
			body:     testBody{},
			contains: "not allowed",
		},
		{
			name: "body required",
			operation: mustTestOperation(
				t,
				http.MethodPost,
				policy.BodyModeJSON,
				1024,
			),
			body:     nil,
			contains: "required",
		},
		{
			name: "body too large",
			operation: mustTestOperation(
				t,
				http.MethodPost,
				policy.BodyModeJSON,
				4,
			),
			body:     testBody{Values: []string{"too large"}},
			contains: "exceeds 4 bytes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := Prepare(baseURL, test.operation, nil, test.body)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Prepare error = %v, want substring %q", err, test.contains)
			}
		})
	}
}

func mustTestOperation(
	t *testing.T,
	method string,
	requestMode policy.BodyMode,
	maxRequestBytes int64,
) policy.Operation {
	t.Helper()

	operation, err := policy.NewOperation(policy.OperationSpec{
		ID:               "test.operation",
		Method:           method,
		Path:             "/test",
		BucketID:         "test_bucket",
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeNever,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      requestMode,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  maxRequestBytes,
		MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatalf("create test operation: %v", err)
	}

	return operation
}
