package request

import (
	"bytes"
	"context"
	"io"
	"net/http"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

// Prepared — неизменяемое представление одного подготовленного WB-запроса.
// Каждый physical attempt создаёт из него новый *http.Request и body reader.
type Prepared struct {
	method      string
	urlString   string
	requestMode policy.BodyMode
	bodyBytes   []byte
	contentType string
	headers     http.Header
}

// NewHTTPRequest создаёт независимый HTTP request для одной попытки.
func (prepared Prepared) NewHTTPRequest(
	ctx context.Context,
) (*http.Request, error) {
	if ctx == nil {
		return nil, errRequestContextRequired
	}
	if prepared.method == "" || prepared.urlString == "" {
		return nil, errRequestNotPrepared
	}

	var body io.Reader
	if prepared.requestMode.IsJSON() || prepared.requestMode.IsMultipart() {
		if prepared.bodyBytes == nil {
			return nil, errRequestNotPrepared
		}

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
