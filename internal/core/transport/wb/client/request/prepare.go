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
	operation policy.Operation,
	query any,
	body any,
) (Prepared, error) {
	queryString, err := prepareQuery(query)
	if err != nil {
		return Prepared{}, err
	}

	bodyBytes, contentType, err := prepareBody(operation, body)
	if err != nil {
		return Prepared{}, err
	}
	if int64(len(bodyBytes)) > operation.MaxRequestBytes() {
		return Prepared{}, fmt.Errorf(
			"WB request body exceeds %d bytes",
			operation.MaxRequestBytes(),
		)
	}

	requestURL := url.URL{
		Scheme:   "https",
		Host:     operation.Host(),
		Path:     operation.Path(),
		RawQuery: queryString,
	}

	return Prepared{
		method:      operation.Method(),
		urlString:   requestURL.String(),
		bodyBytes:   bodyBytes,
		contentType: contentType,
		headers:     operation.Headers(),
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
) ([]byte, string, error) {
	switch {
	case operation.RequestMode().IsNone():
		if !isNilValue(body) {
			return nil, "", errRequestBodyNotAllowed
		}

		return nil, "", nil

	case operation.RequestMode().IsJSON():
		if isNilValue(body) {
			return nil, "", errRequestBodyRequired
		}
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, "", fmt.Errorf("encode WB request body: %w", err)
		}
		return bodyBytes, "application/json", nil

	default:
		return nil, "", errRequestBodyModeUnsupported
	}
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
