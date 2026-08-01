package core_transport_wb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (client *ScopedClient) newRequest(
	ctx context.Context,
	method string,
	path string,
	query url.Values,
	body any,
) (*http.Request, error) {
	endpointURL := client.client.baseURL +
		"/" +
		strings.TrimLeft(path, "/")

	if len(query) > 0 {
		endpointURL += "?" + query.Encode()
	}

	var bodyReader io.Reader

	if body != nil {
		bodyJSON, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal WB request %w", err)
		}
		bodyReader = bytes.NewReader(bodyJSON)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		endpointURL,
		bodyReader,
	)
	if err != nil {
		return nil, fmt.Errorf("create WB request %w", err)
	}
	request.Header.Set(
		"Accept",
		"application/json",
	)
	request.Header.Set(
		"Authorization",
		client.credentials.Token,
	)
	if body != nil {
		request.Header.Set(
			"Content-Type",
			"application/json",
		)
	}

	return request, nil

}
