package policy

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	BodyModeNone          BodyMode      = "none"
	BodyModeJSON          BodyMode      = "json"
	BodyModeMultipart     BodyMode      = "multipart"
	OperationKindRead     OperationKind = "read"
	OperationKindMutation OperationKind = "mutation"
)

// OperationID — стабильный идентификатор операции WB Core.
type OperationID string

// OperationKind определяет, читает операция данные или изменяет их.
type OperationKind string

// BodyMode определяет, ожидается ли JSON body.
type BodyMode string

// Operation описывает одну разрешённую операцию WB API.
type Operation struct {
	id               OperationID
	method           string
	path             string
	bucketID         BucketID
	kind             OperationKind
	retryMode        RetryMode
	successStatuses  []int
	requestMode      BodyMode
	responseMode     BodyMode
	maxRequestBytes  int64
	maxResponseBytes int64
	headers          http.Header
}

// OperationSpec описывает операцию для закрытого API-каталога.
type OperationSpec struct {
	ID               OperationID
	Method           string
	Path             string
	BucketID         BucketID
	Kind             OperationKind
	RetryMode        RetryMode
	SuccessStatuses  []int
	RequestMode      BodyMode
	ResponseMode     BodyMode
	MaxRequestBytes  int64
	MaxResponseBytes int64
	Headers          http.Header
}

// NewOperation создаёт проверенную операцию для закрытых catalog-пакетов WB Core.
// Feature-код не должен вызывать этот конструктор.
func NewOperation(spec OperationSpec) (Operation, error) {
	if err := validateOperationSpec(spec); err != nil {
		return Operation{}, err
	}

	return buildOperation(spec), nil
}

func (id OperationID) String() string {
	return string(id)
}

func (mode BodyMode) String() string {
	return string(mode)
}

func (mode BodyMode) IsNone() bool {
	return mode == BodyModeNone
}

func (mode BodyMode) IsJSON() bool {
	return mode == BodyModeJSON
}

func (mode BodyMode) IsMultipart() bool {
	return mode == BodyModeMultipart
}

func (kind OperationKind) IsRead() bool {
	return kind == OperationKindRead
}

func (kind OperationKind) IsMutation() bool {
	return kind == OperationKindMutation
}

func (operation Operation) ID() OperationID {
	return operation.id
}

func (operation Operation) Method() string {
	return operation.method
}

func (operation Operation) Path() string {
	return operation.path
}

func (operation Operation) BucketID() BucketID {
	return operation.bucketID
}

func (operation Operation) Kind() OperationKind {
	return operation.kind
}

func (operation Operation) RetryMode() RetryMode {
	return operation.retryMode
}

func (operation Operation) SuccessStatuses() []int {
	return cloneSuccessStatuses(
		operation.successStatuses,
	)
}

func (operation Operation) IsSuccessStatus(
	status int,
) bool {
	return containsSuccessStatus(
		operation.successStatuses,
		status,
	)
}

func (operation Operation) RequestMode() BodyMode {
	return operation.requestMode
}

func (operation Operation) ResponseMode() BodyMode {
	return operation.responseMode
}

func (operation Operation) MaxRequestBytes() int64 {
	return operation.maxRequestBytes
}

func (operation Operation) MaxResponseBytes() int64 {
	return operation.maxResponseBytes
}

func (operation Operation) Headers() http.Header {
	return operation.headers.Clone()
}

func buildOperation(spec OperationSpec) Operation {
	return Operation{
		id:        spec.ID,
		method:    spec.Method,
		path:      spec.Path,
		bucketID:  spec.BucketID,
		kind:      spec.Kind,
		retryMode: spec.RetryMode,
		successStatuses: cloneSuccessStatuses(
			spec.SuccessStatuses,
		),
		requestMode:      spec.RequestMode,
		responseMode:     spec.ResponseMode,
		maxRequestBytes:  spec.MaxRequestBytes,
		maxResponseBytes: spec.MaxResponseBytes,
		headers:          spec.Headers.Clone(),
	}
}

func validateOperationIdentityAndModes(spec OperationSpec) error {
	if spec.ID == "" {
		return fmt.Errorf("operation ID is empty")
	}

	if spec.BucketID == "" {
		return fmt.Errorf(
			"operation %q bucket ID is empty",
			spec.ID,
		)
	}

	if !spec.Kind.IsRead() && !spec.Kind.IsMutation() {
		return fmt.Errorf(
			"operation %q has unsupported kind %q",
			spec.ID,
			spec.Kind,
		)
	}

	if spec.RetryMode != RetryModeNever &&
		spec.RetryMode != RetryModeReadSafe {
		return fmt.Errorf(
			"operation %q has unsupported retry mode %q",
			spec.ID,
			spec.RetryMode,
		)
	}

	if spec.RetryMode.AllowsRetry() && !spec.Kind.IsRead() {
		return fmt.Errorf(
			"operation %q allows retry for non-read kind %q",
			spec.ID,
			spec.Kind,
		)
	}

	if !spec.RequestMode.IsNone() && !spec.RequestMode.IsJSON() &&
		!spec.RequestMode.IsMultipart() {
		return fmt.Errorf(
			"operation %q has unsupported request body mode %q",
			spec.ID,
			spec.RequestMode,
		)
	}
	for name, values := range spec.Headers {
		if strings.EqualFold(name, "Authorization") ||
			strings.EqualFold(name, "Content-Type") || len(values) != 1 ||
			strings.TrimSpace(name) == "" || strings.TrimSpace(values[0]) == "" {
			return fmt.Errorf("operation %q has invalid fixed header %q", spec.ID, name)
		}
	}

	if !spec.ResponseMode.IsNone() && !spec.ResponseMode.IsJSON() {
		return fmt.Errorf(
			"operation %q has unsupported response body mode %q",
			spec.ID,
			spec.ResponseMode,
		)
	}

	return nil
}

func validateOperationHTTP(spec OperationSpec) error {
	switch spec.Method {
	case http.MethodGet, http.MethodPost:
	default:
		return fmt.Errorf(
			"operation %q has unsupported HTTP method %q",
			spec.ID,
			spec.Method,
		)
	}

	if spec.Path == "" {
		return fmt.Errorf(
			"operation %q path is empty",
			spec.ID,
		)
	}

	if !strings.HasPrefix(spec.Path, "/") ||
		strings.HasPrefix(spec.Path, "//") {
		return fmt.Errorf(
			"operation %q path %q must start with exactly one slash",
			spec.ID,
			spec.Path,
		)
	}

	parsedPath, err := url.ParseRequestURI(spec.Path)
	if err != nil {
		return fmt.Errorf(
			"operation %q has invalid path %q: %w",
			spec.ID,
			spec.Path,
			err,
		)
	}

	if parsedPath.IsAbs() ||
		parsedPath.Host != "" ||
		parsedPath.RawQuery != "" ||
		parsedPath.ForceQuery ||
		parsedPath.Fragment != "" {
		return fmt.Errorf(
			"operation %q path %q must not contain host, query or fragment",
			spec.ID,
			spec.Path,
		)
	}

	return nil
}

func validateOperationSizeBounds(spec OperationSpec) error {
	if spec.RequestMode.IsNone() && spec.MaxRequestBytes != 0 {
		return fmt.Errorf(
			"operation %q has no request body but max request bytes is %d",
			spec.ID,
			spec.MaxRequestBytes,
		)
	}

	if spec.RequestMode.IsJSON() && spec.MaxRequestBytes <= 0 {
		return fmt.Errorf(
			"operation %q has JSON request body but max request bytes is %d",
			spec.ID,
			spec.MaxRequestBytes,
		)
	}

	if spec.ResponseMode.IsNone() && spec.MaxResponseBytes != 0 {
		return fmt.Errorf(
			"operation %q has no response body but max response bytes is %d",
			spec.ID,
			spec.MaxResponseBytes,
		)
	}

	if spec.ResponseMode.IsJSON() && spec.MaxResponseBytes <= 0 {
		return fmt.Errorf(
			"operation %q has JSON response body but max response bytes is %d",
			spec.ID,
			spec.MaxResponseBytes,
		)
	}

	return nil
}

func validateOperationSpec(spec OperationSpec) error {
	if err := validateOperationIdentityAndModes(spec); err != nil {
		return err
	}

	if err := validateOperationHTTP(spec); err != nil {
		return err
	}

	if err := validateSuccessStatuses(spec.SuccessStatuses); err != nil {
		return fmt.Errorf(
			"operation %q: %w",
			spec.ID,
			err,
		)
	}

	if err := validateOperationSizeBounds(spec); err != nil {
		return err
	}

	return nil
}
