package request_test

import (
	"context"
	"net/http"
	"testing"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	request "github.com/ERONIS/wb-service/internal/core/transport/wb/client/request"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

func TestPrepareUsesOperationHost(t *testing.T) {
	commonPing, err := policy.NewOperation(policy.OperationSpec{
		ID:               "test.common-ping",
		Method:           http.MethodGet,
		Host:             "common-api.wildberries.ru",
		Path:             "/ping",
		BucketID:         "test_common_ping",
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeNever,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		operation policy.Operation
		wantURL   string
	}{
		{name: "content", operation: contentapi.PingOperation(), wantURL: "https://content-api.wildberries.ru/ping"},
		{name: "common", operation: commonPing, wantURL: "https://common-api.wildberries.ru/ping"},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepared, err := request.Prepare(test.operation, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			httpRequest, err := prepared.NewHTTPRequest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := httpRequest.URL.String(); got != test.wantURL {
				t.Fatalf("request URL = %q, want %q", got, test.wantURL)
			}
		})
	}
}
