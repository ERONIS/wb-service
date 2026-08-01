package core_transport_wb_middleware

import "net/http"

type Middleware func(next http.RoundTripper) http.RoundTripper

type RoundTripperFunc func(request *http.Request) (*http.Response, error)

func (function RoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func Chain(
	transport http.RoundTripper,
	middleware ...Middleware,
) http.RoundTripper {
	if transport == nil {
		transport = http.DefaultTransport
	}

	for index := len(middleware) - 1; index >= 0; index-- {
		transport = middleware[index](transport)
	}

	return transport
}
