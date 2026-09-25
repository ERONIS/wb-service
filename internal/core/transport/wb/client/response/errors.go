package response

import "errors"

var (
	errResultTargetRequired = errors.New(
		"result target must be a non-nil pointer",
	)
	errResponseBodyEmpty = errors.New(
		"WB response body is empty",
	)
	errHTTPResponseRequired = errors.New(
		"WB HTTP response is required",
	)
	errHTTPResponseBodyRequired = errors.New(
		"WB HTTP response body is required",
	)
	errResponseLimitRequired = errors.New(
		"WB response body limit must be positive",
	)
)

// IsEmptyBody reports whether decoding failed because WB returned no JSON body.
func IsEmptyBody(err error) bool {
	return errors.Is(err, errResponseBodyEmpty)
}
