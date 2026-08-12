package wb_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	"go.uber.org/zap"
)

type clientsetRoundTripper struct {
	mutex         sync.Mutex
	authorization []string
	paths         []string
	closed        bool
	body          string
}

func (transport *clientsetRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	transport.mutex.Lock()
	transport.authorization = append(
		transport.authorization,
		request.Header.Get("Authorization"),
	)
	transport.paths = append(transport.paths, request.URL.Path)
	transport.mutex.Unlock()

	body := transport.body
	if body == "" {
		body = `{
			"data":{"freeLimits":4,"paidLimits":5},
			"error":false,
			"errorText":"",
			"additionalErrors":null
		}`
	}

	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       request,
	}, nil
}

func TestPublicClassifiedErrorContract(t *testing.T) {
	t.Parallel()

	clientset, err := wb.NewForConfigAndHTTPClient(
		&config.Config{
			BaseURL: "https://content-api.wildberries.ru",
			Timeout: time.Second,
			Cabinets: []config.CabinetConfig{{
				ID:    "test",
				Name:  "Test",
				Token: "token",
			}},
		},
		&http.Client{Transport: &clientsetRoundTripper{body: `{"data":`}},
		zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("create Clientset: %v", err)
	}
	t.Cleanup(clientset.CloseIdleConnections)

	cabinet, err := clientset.ForCabinet("test")
	if err != nil {
		t.Fatalf("get cabinet: %v", err)
	}
	_, err = cabinet.ContentV1().Cards().Limits(context.Background())
	if err == nil {
		t.Fatal("invalid JSON unexpectedly succeeded")
	}

	var classifiedErr wb.ClassifiedError
	if !errors.As(err, &classifiedErr) {
		t.Fatalf("error does not implement wb.ClassifiedError: %v", err)
	}
	if classifiedErr.Code() != wb.ErrorCodeInvalidResponse {
		t.Fatalf("error code = %q", classifiedErr.Code())
	}
	if classifiedErr.Delivery() != wb.ResponseReceived {
		t.Fatalf("delivery = %s", classifiedErr.Delivery())
	}
}

func (transport *clientsetRoundTripper) CloseIdleConnections() {
	transport.mutex.Lock()
	transport.closed = true
	transport.mutex.Unlock()
}

func (transport *clientsetRoundTripper) snapshot() (
	[]string,
	[]string,
	bool,
) {
	transport.mutex.Lock()
	defer transport.mutex.Unlock()

	return append([]string(nil), transport.authorization...),
		append([]string(nil), transport.paths...),
		transport.closed
}

func TestClientsetPublicAPIAndCabinetIsolation(t *testing.T) {
	t.Parallel()

	baseTransport := &clientsetRoundTripper{}
	baseHTTPClient := &http.Client{
		Transport: baseTransport,
		Timeout:   9 * time.Second,
	}
	configuration := &config.Config{
		BaseURL: "https://content-api.wildberries.ru",
		Timeout: time.Second,
		Cabinets: []config.CabinetConfig{
			{ID: "beta", Name: " Beta ", Token: "token-beta"},
			{ID: "alpha", Name: "Alpha", Token: "token-alpha"},
		},
	}

	clientset, err := wb.NewForConfigAndHTTPClient(
		configuration,
		baseHTTPClient,
		zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("create Clientset: %v", err)
	}
	t.Cleanup(clientset.CloseIdleConnections)

	if baseHTTPClient.Transport != baseTransport {
		t.Fatal("base HTTP client transport was mutated")
	}
	if baseHTTPClient.Timeout != 9*time.Second {
		t.Fatalf("base timeout = %s, want 9s", baseHTTPClient.Timeout)
	}
	if baseHTTPClient.CheckRedirect != nil {
		t.Fatal("base CheckRedirect was mutated")
	}

	cabinets := clientset.Cabinets()
	if len(cabinets) != 2 {
		t.Fatalf("cabinet count = %d, want 2", len(cabinets))
	}
	if cabinets[0].ID != "alpha" || cabinets[1].ID != "beta" {
		t.Fatalf("cabinet order = %+v", cabinets)
	}
	if cabinets[1].Name != "Beta" {
		t.Fatalf("normalized cabinet name = %q, want Beta", cabinets[1].Name)
	}

	cabinets[0].Name = "mutated snapshot"
	if got := clientset.Cabinets()[0].Name; got != "Alpha" {
		t.Fatalf("Clientset snapshot was mutated: %q", got)
	}

	beta, err := clientset.ForCabinet("beta")
	if err != nil {
		t.Fatalf("get beta cabinet: %v", err)
	}
	if beta.ID() != "beta" || beta.Name() != "Beta" {
		t.Fatalf("beta cabinet = id %q, name %q", beta.ID(), beta.Name())
	}

	limits, err := beta.ContentV1().Cards().Limits(context.Background())
	if err != nil {
		t.Fatalf("get card limits: %v", err)
	}
	if limits.Data.FreeLimits != 4 || limits.Data.PaidLimits != 5 {
		t.Fatalf("limits = %+v", limits.Data)
	}

	authorization, paths, _ := baseTransport.snapshot()
	if len(authorization) != 1 || authorization[0] != "token-beta" {
		t.Fatalf("Authorization calls = %#v", authorization)
	}
	if len(paths) != 1 || paths[0] != "/content/v2/cards/limits" {
		t.Fatalf("request paths = %#v", paths)
	}

	_, err = clientset.ForCabinet("missing")
	if !errors.Is(err, wb.ErrCabinetNotFound) {
		t.Fatalf("missing cabinet error = %v", err)
	}

	clientset.CloseIdleConnections()
	_, _, closed := baseTransport.snapshot()
	if !closed {
		t.Fatal("idle connections were not closed")
	}
}

func TestClientsetRejectsUnsafeBaseURL(t *testing.T) {
	t.Parallel()

	tests := []string{
		"http://content-api.wildberries.ru",
		"https://example.com",
		"https://content-api.wildberries.ru:443",
		"https://content-api.wildberries.ru/prefix",
	}

	for _, baseURL := range tests {
		baseURL := baseURL
		t.Run(baseURL, func(t *testing.T) {
			t.Parallel()

			_, err := wb.NewForConfigAndHTTPClient(
				&config.Config{
					BaseURL: baseURL,
					Timeout: time.Second,
					Cabinets: []config.CabinetConfig{{
						ID:    "test",
						Name:  "Test",
						Token: "token",
					}},
				},
				&http.Client{Transport: &clientsetRoundTripper{}},
				zap.NewNop(),
			)
			if err == nil {
				t.Fatal("unsafe base URL was accepted")
			}
		})
	}
}
