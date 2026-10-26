package request

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/url"
	"reflect"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

type MultipartFile struct {
	FieldName string
	FileName  string
	Data      []byte
}

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

	case operation.RequestMode().IsMultipart():
		return prepareMultipartBody(body)

	default:
		return nil, "", errRequestBodyModeUnsupported
	}
}

func prepareMultipartBody(body any) ([]byte, string, error) {
	form, ok := body.(MultipartFile)
	if !ok || form.FieldName == "" || form.FileName == "" || form.Data == nil {
		return nil, "", errRequestBodyRequired
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile(form.FieldName, form.FileName)
	if err != nil {
		return nil, "", fmt.Errorf("create WB multipart body: %w", err)
	}
	if _, err := part.Write(form.Data); err != nil {
		return nil, "", fmt.Errorf("write WB multipart body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close WB multipart body: %w", err)
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
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
