package response

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// ValidateTarget проверяет target до начала HTTP-отправки.
func ValidateTarget(target any) error {
	targetValue := reflect.ValueOf(target)
	if targetValue.Kind() != reflect.Pointer || targetValue.IsNil() {
		return errResultTargetRequired
	}

	return nil
}

// Decode декодирует JSON через временное значение и не изменяет target при
// ошибке.
func Decode(body []byte, target any) error {
	if err := ValidateTarget(target); err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return errResponseBodyEmpty
	}

	targetValue := reflect.ValueOf(target)
	decodedValue := reflect.New(targetValue.Elem().Type())

	if err := json.Unmarshal(body, decodedValue.Interface()); err != nil {
		return fmt.Errorf(
			"decode WB response JSON: %w",
			err,
		)
	}

	targetValue.Elem().Set(decodedValue.Elem())

	return nil
}
