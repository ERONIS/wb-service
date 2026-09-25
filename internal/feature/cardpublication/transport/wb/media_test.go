package cardpublication_wb_transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestDownloadMediaFilePreservesRetryAfterAndStatus(t *testing.T) {
	transport := &CatalogTransport{downloader: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{"45"}},
			Body:       io.NopCloser(strings.NewReader("busy")),
		}, nil
	})}}
	_, err := transport.DownloadMediaFile(context.Background(), "https://example.test/photo")
	var classified core_wb.ClassifiedError
	if !errors.As(err, &classified) || classified.HTTPStatus() != 429 || classified.RetryAfter() != 45*time.Second || classified.Delivery() != core_wb.NotDispatched {
		t.Fatalf("download response lost retry information: %v", err)
	}
}

func TestDownloadMediaFileDetectsTypeAndAddsExtension(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"image/png"}},
			Body:       io.NopCloser(strings.NewReader("png-data")),
		}, nil
	})}
	transport := &CatalogTransport{downloader: client}
	file, err := transport.DownloadMediaFile(context.Background(), "https://example.test/photo")
	if err != nil {
		t.Fatal(err)
	}
	if file.MediaType != "image/png" || file.FileName != "photo.png" || string(file.Data) != "png-data" {
		t.Fatalf("downloaded file = %+v", file)
	}
}
