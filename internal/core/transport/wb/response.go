package core_transport_wb

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const maxResponseBodySize int64 = 32 * 1024 * 1024

func readResponseBody(
	response *http.Response,
) ([]byte, error) {
	defer response.Body.Close()

	limitedBody := io.LimitReader(
		response.Body,
		maxResponseBodySize+1,
	)

	body, err := io.ReadAll(limitedBody)
	if err != nil {
		return nil, fmt.Errorf(
			"read WB response body: %w",
			err,
		)
	}
	if int64(len(body)) > maxResponseBodySize {
		return nil, fmt.Errorf(
			"WB response body exceeds %d bytes",
			maxResponseBodySize)
	}

	return body, nil
}
func decodeResponseBody(
	body []byte,
	result any,
) error {

	if len(body) == 0 || result == nil {
		return nil
	}
	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("decode WB response body %w", err)
	}

	return nil
}
