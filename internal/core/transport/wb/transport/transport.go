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
	base *http.Transport
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

func configureWBTransport(transport *http.Transport) {
	if transport == nil {
		return
	}
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

// CloseIdleConnections закрывает простаивающие соединения общего pool.
func (transport *SharedTransport) CloseIdleConnections() {
	if transport == nil || transport.base == nil {
		return
	}

	transport.base.CloseIdleConnections()
}
