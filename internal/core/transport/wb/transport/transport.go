package transport

import (
	"errors"
	"net/http"
)

var errUnsupportedDefaultTransport = errors.New(
	"unsupported default HTTP transport",
)

// SharedTransport владеет общим connection pool всех WB-клиентов.
type SharedTransport struct {
	base http.RoundTripper
}

// NewSharedTransport создаёт принадлежащую WB Core копию стандартного transport.
func NewSharedTransport() (*SharedTransport, error) {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errUnsupportedDefaultTransport
	}

	return &SharedTransport{
		base: defaultTransport.Clone(),
	}, nil
}

// NewSharedTransportFrom создаёт общий transport из явно внедрённой базы.
// *http.Transport клонируется; произвольный RoundTripper остаётся test seam.
func NewSharedTransportFrom(
	base http.RoundTripper,
) (*SharedTransport, error) {
	if base == nil {
		return nil, errRoundTripperRequired
	}

	if httpTransport, ok := base.(*http.Transport); ok {
		base = httpTransport.Clone()
	}

	return &SharedTransport{base: base}, nil
}

// RoundTripper строит отдельную wrapper chain поверх общего transport.
func (transport *SharedTransport) RoundTripper(
	wrappers ...WrapperFunc,
) http.RoundTripper {
	return Chain(transport.base, wrappers...)
}

// CloseIdleConnections закрывает простаивающие соединения общего pool.
func (transport *SharedTransport) CloseIdleConnections() {
	if transport == nil || transport.base == nil {
		return
	}

	if closer, ok := transport.base.(interface {
		CloseIdleConnections()
	}); ok {
		closer.CloseIdleConnections()
	}
}
