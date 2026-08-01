package core_transport_wb

import (
	"fmt"
	"net/http"
	"strings"
)

type APIError struct {
	StatusCode int
	Body       string
}

func (apiErr *APIError) Error() string {
	statusText := http.StatusText(
		apiErr.StatusCode,
	)

	message := fmt.Sprintf(
		"WB API returned status %d %s",
		apiErr.StatusCode,
		statusText,
	)

	body := strings.TrimSpace(apiErr.Body)
	if body != "" {
		message += ": " + body
	}

	return message
}
func checkResponseStatus(
	response *http.Response,
	body []byte,
) error {
	if response.StatusCode >= 200 &&
		response.StatusCode < 300 {
		return nil
	}

	return &APIError{
		StatusCode: response.StatusCode,
		Body:       string(body),
	}
}
