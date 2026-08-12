package request

import (
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

func encodeQueryStruct(query any) (url.Values, error) {
	queryValue := reflect.ValueOf(query)

	if queryValue.Kind() == reflect.Pointer {
		queryValue = queryValue.Elem()
	}

	if queryValue.Kind() != reflect.Struct {
		return nil, fmt.Errorf(
			"query must be a struct, got %s",
			queryValue.Kind(),
		)
	}

	queryType := queryValue.Type()
	values := make(url.Values)
	seenNames := make(map[string]struct{})

	for index := 0; index < queryType.NumField(); index++ {
		fieldType := queryType.Field(index)
		tag, exists := fieldType.Tag.Lookup("url")
		if !exists {
			continue
		}

		name, omitEmpty, err := parseQueryTag(tag)
		if err != nil {
			return nil, fmt.Errorf(
				"query field %q: %w",
				fieldType.Name,
				err,
			)
		}
		if name == "-" {
			continue
		}

		if _, exists := seenNames[name]; exists {
			return nil, fmt.Errorf(
				"query parameter %q is duplicated",
				name,
			)
		}
		seenNames[name] = struct{}{}

		fieldValue := queryValue.Field(index)
		if omitEmpty && fieldValue.IsZero() {
			continue
		}

		encodedValue, err := encodeQueryField(fieldValue)
		if err != nil {
			return nil, fmt.Errorf(
				"query field %q: %w",
				fieldType.Name,
				err,
			)
		}

		values.Set(name, encodedValue)
	}

	return values, nil
}

func parseQueryTag(
	tag string,
) (string, bool, error) {
	parts := strings.Split(tag, ",")
	name := parts[0]

	if name == "" {
		return "", false, fmt.Errorf(
			"URL parameter name is empty",
		)
	}
	if name == "-" {
		return name, false, nil
	}

	omitEmpty := false

	for _, option := range parts[1:] {
		switch option {
		case "omitempty":
			if omitEmpty {
				return "", false, fmt.Errorf(
					"omitempty option is duplicated",
				)
			}

			omitEmpty = true

		default:
			return "", false, fmt.Errorf(
				"unsupported URL tag option %q",
				option,
			)
		}
	}

	return name, omitEmpty, nil
}

func encodeQueryField(
	fieldValue reflect.Value,
) (string, error) {
	switch fieldValue.Kind() {
	case reflect.String:
		return fieldValue.String(), nil

	case reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64:
		return strconv.FormatInt(
			fieldValue.Int(),
			10,
		), nil

	default:
		return "", fmt.Errorf(
			"unsupported query field kind %s",
			fieldValue.Kind(),
		)
	}
}
