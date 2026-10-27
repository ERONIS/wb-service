package transport

import (
	"errors"
	"net/http"
	"time"
)

var errRoundTripperRequired = errors.New("HTTP round tripper is required")

// NewHTTPClient создаёт HTTP-клиент с общим транспортом WB.
func (transport *SharedTransport) NewHTTPClient(
	timeout time.Duration,
) (*http.Client, error) {
	if transport == nil || transport.base == nil {
		return nil, errRoundTripperRequired
	}

	return &http.Client{
		Timeout:       timeout,
		Transport:     transport.base,
		CheckRedirect: denyRedirects,
	}, nil
}

func denyRedirects(
	_ *http.Request,
	_ []*http.Request,
) error {
	return http.ErrUseLastResponse
}
