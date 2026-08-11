package request

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

// Prepare проверяет и сериализует входные данные логического WB-запроса.
func Prepare(
	baseURL *url.URL,
	operation policy.Operation,
	query any,
	body any,
) (Prepared, error) {
	if baseURL == nil {
		return Prepared{}, errRequestBaseURLRequired
	}
	if operation.ID() == "" {
		return Prepared{}, errRequestOperationRequired
	}

	queryString, err := prepareQuery(query)
	if err != nil {
		return Prepared{}, err
	}

	bodyBytes, err := prepareBody(operation, body)
	if err != nil {
		return Prepared{}, err
	}

	requestURL := *baseURL
	requestURL.Path = operation.Path()
	requestURL.RawPath = ""
	requestURL.RawQuery = queryString
	requestURL.ForceQuery = false
	requestURL.Fragment = ""
	requestURL.RawFragment = ""

	return Prepared{
		method:      operation.Method(),
		urlString:   requestURL.String(),
		requestMode: operation.RequestMode(),
		bodyBytes:   bodyBytes,
	}, nil
}

func prepareQuery(query any) (string, error) {
	if isNilValue(query) {
		return "", nil
	}

	values, err := encodeQueryStruct(query)
	if err != nil {
		return "", fmt.Errorf(
			"encode WB request query: %w",
			err,
		)
	}

	return values.Encode(), nil
}

func prepareBody(
	operation policy.Operation,
	body any,
) ([]byte, error) {
	switch {
	case operation.RequestMode().IsNone():
		if !isNilValue(body) {
			return nil, errRequestBodyNotAllowed
		}

		return nil, nil

	case operation.RequestMode().IsJSON():
		if isNilValue(body) {
			return nil, errRequestBodyRequired
		}

	default:
		return nil, errRequestBodyModeUnsupported
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf(
			"encode WB request body: %w",
			err,
		)
	}

	if int64(len(bodyBytes)) > operation.MaxRequestBytes() {
		return nil, fmt.Errorf(
			"WB request body exceeds %d bytes",
			operation.MaxRequestBytes(),
		)
	}

	return bodyBytes, nil
}

func isNilValue(value any) bool {
	if value == nil {
		return true
	}

	reflectedValue := reflect.ValueOf(value)

	switch reflectedValue.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return reflectedValue.IsNil()

	default:
		return false
	}
}
