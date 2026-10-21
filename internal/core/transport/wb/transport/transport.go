package transport

import (
	"errors"
	"net/http"
)

var errUnsupportedDefaultTransport = errors.New(
	"unsupported default HTTP transport",
)

// SharedTransport владеет общим пулом соединений всех клиентов WB.
type SharedTransport struct {
	base *http.Transport
}

// NewSharedTransport создаёт копию стандартного HTTP-транспорта для WB.
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

// CloseIdleConnections закрывает простаивающие соединения общего пула.
func (transport *SharedTransport) CloseIdleConnections() {
	if transport == nil || transport.base == nil {
		return
	}

	transport.base.CloseIdleConnections()
}

// configureWBTransport включает согласование HTTP/2 и настраивает пул соединений.
func configureWBTransport(transport *http.Transport) {
	if transport == nil {
		return
	}
	transport.ForceAttemptHTTP2 = true
	transport.TLSNextProto = nil
	if transport.MaxIdleConns < 200 {
		transport.MaxIdleConns = 200
	}
	if transport.MaxIdleConnsPerHost < 50 {
		transport.MaxIdleConnsPerHost = 50
	}
}
