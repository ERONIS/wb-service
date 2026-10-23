package request

import "errors"

var (
	errRequestBodyRequired = errors.New(
		"WB request body is required",
	)
	errRequestBodyNotAllowed = errors.New(
		"WB request body is not allowed for this operation",
	)
	errRequestBodyModeUnsupported = errors.New(
		"WB request has unsupported body mode",
	)
	errHTTPRequestBuildFailed = errors.New(
		"build WB HTTP request",
	)
)
