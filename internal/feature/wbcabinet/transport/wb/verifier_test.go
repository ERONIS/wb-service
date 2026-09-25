package wbcabinet_wb_transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	core_wb_config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"
	"go.uber.org/zap"
)

func TestPrepareValidatesContentTokenBeforePublishingCandidate(t *testing.T) {
	configuration := core_wb_config.Config{
		BaseURL: "https://content-api.wildberries.ru",
		Timeout: time.Second,
	}
	roundTripper := &verificationRoundTripper{token: "credential-token"}
	clientset, err := core_wb.NewForConfigAndHTTPClient(
		context.Background(),
		&configuration,
		&http.Client{Transport: roundTripper},
		zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("NewForConfigAndHTTPClient() error = %v", err)
	}
	defer clientset.CloseIdleConnections()

	prepared, err := NewVerifier(clientset).Prepare(
		context.Background(),
		wbcabinet_service.CabinetID("cabinet-1"),
		"Основной",
		"credential-token",
	)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if len(clientset.Cabinets()) != 0 {
		t.Fatal("verified candidate was published before durable activation")
	}
	if err := prepared.Activate(); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if got := clientset.Cabinets(); len(got) != 1 || got[0].ID != "cabinet-1" {
		t.Fatalf("Cabinets() = %+v", got)
	}
	if roundTripper.calls != 1 {
		t.Fatalf("WB verification calls = %d, want 1", roundTripper.calls)
	}
}

func TestPrepareClassifiesContentPingRateLimit(t *testing.T) {
	t.Parallel()

	configuration := core_wb_config.Config{
		BaseURL: "https://content-api.wildberries.ru",
		Timeout: time.Second,
	}
	clientset, err := core_wb.NewForConfigAndHTTPClient(
		context.Background(),
		&configuration,
		&http.Client{Transport: rateLimitedRoundTripper{}},
		zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("NewForConfigAndHTTPClient() error = %v", err)
	}
	defer clientset.CloseIdleConnections()

	_, err = NewVerifier(clientset).Prepare(
		context.Background(),
		wbcabinet_service.CabinetID("cabinet-1"),
		"Основной",
		"credential-token",
	)
	if !errors.Is(err, wbcabinet_service.ErrVerificationRateLimited) {
		t.Fatalf("Prepare() error = %v, want ErrVerificationRateLimited", err)
	}
}

type rateLimitedRoundTripper struct{}

func (rateLimitedRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Status:     "429 Too Many Requests",
		Header:     http.Header{"Retry-After": []string{"30"}},
		Body:       io.NopCloser(strings.NewReader(`{"title":"too many requests"}`)),
		Request:    request,
	}, nil
}

type verificationRoundTripper struct {
	token string
	calls int
}

func (roundTripper *verificationRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Header.Get("Authorization") != roundTripper.token {
		return nil, fmt.Errorf("unexpected authorization header")
	}
	roundTripper.calls++
	var body string
	switch request.URL.Host + request.URL.Path {
	case "content-api.wildberries.ru/ping":
		body = `{"TS":"2026-08-27T12:00:00Z","Status":"OK"}`
	default:
		return nil, fmt.Errorf("unexpected WB request: %s", request.URL)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}, nil
}
