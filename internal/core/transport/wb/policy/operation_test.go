package policy

import (
	"net/http"
	"testing"
)

func TestNewOperationValidatesWBHost(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		wantErr bool
	}{
		{name: "content", host: "content-api.wildberries.ru"},
		{name: "common", host: "common-api.wildberries.ru"},
		{name: "sandbox", host: "content-api-sandbox.wildberries.ru"},
		{name: "empty", host: "", wantErr: true},
		{name: "external", host: "attacker.example", wantErr: true},
		{name: "lookalike", host: "content-api.wildberries.ru.attacker.example", wantErr: true},
		{name: "userinfo", host: "content-api.wildberries.ru@attacker.example", wantErr: true},
		{name: "port", host: "content-api.wildberries.ru:443", wantErr: true},
		{name: "scheme", host: "https://content-api.wildberries.ru", wantErr: true},
		{name: "empty label", host: "content-api..wildberries.ru", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operation, err := NewOperation(OperationSpec{
				ID:               "test.ping",
				Method:           http.MethodGet,
				Host:             test.host,
				Path:             "/ping",
				BucketID:         "test_ping",
				Kind:             OperationKindRead,
				RetryMode:        RetryModeNever,
				SuccessStatuses:  []int{http.StatusOK},
				RequestMode:      BodyModeNone,
				ResponseMode:     BodyModeJSON,
				MaxResponseBytes: 1024,
			})
			if (err != nil) != test.wantErr {
				t.Fatalf("NewOperation() error = %v, want error %t", err, test.wantErr)
			}
			if err == nil && operation.Host() != test.host {
				t.Fatalf("operation host = %q, want %q", operation.Host(), test.host)
			}
		})
	}
}
