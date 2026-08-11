package response

import (
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ReadAndClose считывает bounded response body и всегда пытается закрыть его.
func ReadAndClose(
	httpResponse *http.Response,
	maxResponseBytes int64,
) (body []byte, err error) {
	if httpResponse == nil {
		return nil, errHTTPResponseRequired
	}
	if httpResponse.Body == nil {
		return nil, errHTTPResponseBodyRequired
	}
	if maxResponseBytes <= 0 {
		return nil, errResponseLimitRequired
	}

	defer func() {
		closeErr := httpResponse.Body.Close()
		if closeErr == nil {
			return
		}

		err = errors.Join(
			err,
			fmt.Errorf(
				"close WB response body: %w",
				closeErr,
			),
		)
	}()

	if httpResponse.ContentLength > maxResponseBytes {
		return nil, fmt.Errorf(
			"WB response body exceeds %d bytes",
			maxResponseBytes,
		)
	}

	limitedReader := io.LimitReader(
		httpResponse.Body,
		maxResponseBytes+1,
	)

	body, err = io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf(
			"read WB response body: %w",
			err,
		)
	}

	if int64(len(body)) > maxResponseBytes {
		return nil, fmt.Errorf(
			"WB response body exceeds %d bytes",
			maxResponseBytes,
		)
	}

	return body, nil
}

// Close закрывает response body операции, у которой нет тела ответа.
func Close(httpResponse *http.Response) error {
	if httpResponse == nil {
		return errHTTPResponseRequired
	}
	if httpResponse.Body == nil {
		return errHTTPResponseBodyRequired
	}

	if err := httpResponse.Body.Close(); err != nil {
		return fmt.Errorf(
			"close WB response body: %w",
			err,
		)
	}

	return nil
}
