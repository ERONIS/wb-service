package transport

import (
	"errors"
	"net/http"
)

var (
	errHTTPClientRequired = errors.New(
		"base HTTP client is required",
	)
	errRoundTripperRequired = errors.New(
		"HTTP round tripper is required",
	)
)

// NewHTTPClient создаёт копию базового клиента с общим WB transport.
func (transport *SharedTransport) NewHTTPClient(
	baseClient *http.Client,
) (*http.Client, error) {
	if baseClient == nil {
		return nil, errHTTPClientRequired
	}
	if transport == nil || transport.base == nil {
		return nil, errRoundTripperRequired
	}

	clientCopy := *baseClient
	clientCopy.Transport = transport.base
	clientCopy.CheckRedirect = denyRedirects

	return &clientCopy, nil
}

func denyRedirects(
	_ *http.Request,
	_ []*http.Request,
) error {
	return http.ErrUseLastResponse
}
