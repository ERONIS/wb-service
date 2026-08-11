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

// NewHTTPClient создаёт копию базового клиента для одного кабинета.
func NewHTTPClient(
	baseClient *http.Client,
	roundTripper http.RoundTripper,
) (*http.Client, error) {
	if baseClient == nil {
		return nil, errHTTPClientRequired
	}
	if roundTripper == nil {
		return nil, errRoundTripperRequired
	}

	clientCopy := *baseClient
	clientCopy.Transport = roundTripper
	clientCopy.CheckRedirect = denyRedirects

	return &clientCopy, nil
}

func denyRedirects(
	_ *http.Request,
	_ []*http.Request,
) error {
	return http.ErrUseLastResponse
}
