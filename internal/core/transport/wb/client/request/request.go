package request

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// Prepared — неизменяемое представление одного подготовленного WB-запроса.
// Каждый physical attempt создаёт из него новый *http.Request и body reader.
type Prepared struct {
	method      string
	urlString   string
	bodyBytes   []byte
	contentType string
	headers     http.Header
}

// NewHTTPRequest создаёт независимый HTTP request для одной попытки.
func (prepared Prepared) NewHTTPRequest(
	ctx context.Context,
) (*http.Request, error) {
	var body io.Reader
	if prepared.bodyBytes != nil {
		body = bytes.NewReader(prepared.bodyBytes)
	}

	httpRequest, err := http.NewRequestWithContext(
		ctx,
		prepared.method,
		prepared.urlString,
		body,
	)
	if err != nil {
		return nil, errHTTPRequestBuildFailed
	}

	for name, values := range prepared.headers {
		for _, value := range values {
			httpRequest.Header.Add(name, value)
		}
	}
	if prepared.contentType != "" {
		httpRequest.Header.Set("Content-Type", prepared.contentType)
	}

	return httpRequest, nil
}

// BodyBytes возвращает размер сериализованного request body.
func (prepared Prepared) BodyBytes() int {
	return len(prepared.bodyBytes)
}
