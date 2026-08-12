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
	errRequestBaseURLRequired = errors.New(
		"WB API base URL is required",
	)
	errRequestOperationRequired = errors.New(
		"WB request operation is required",
	)
	errRequestContextRequired = errors.New(
		"WB request context is required",
	)
	errRequestNotPrepared = errors.New(
		"WB request is not prepared",
	)
	errHTTPRequestBuildFailed = errors.New(
		"build WB HTTP request",
	)
)
