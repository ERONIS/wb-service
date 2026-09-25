package transport

import (
	"crypto/tls"
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

	cloned := defaultTransport.Clone()
	configureWBTransport(cloned)

	return &SharedTransport{
		base: cloned,
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
		cloned := httpTransport.Clone()
		configureWBTransport(cloned)
		base = cloned
	}

	return &SharedTransport{base: base}, nil
}

func configureWBTransport(transport *http.Transport) {
	if transport == nil {
		return
	}
	// Wildberries API gateways often fail or reset multiplexed HTTP/2 streams
	// with PROTOCOL_ERROR. Disabling HTTP/2 enforces reliable HTTP/1.1 keep-alive.
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	if transport.MaxIdleConns < 200 {
		transport.MaxIdleConns = 200
	}
	if transport.MaxIdleConnsPerHost < 50 {
		transport.MaxIdleConnsPerHost = 50
	}
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
