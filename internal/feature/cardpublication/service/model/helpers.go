package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

func DecodeExactJSON(payload []byte, target any) error {
	if len(payload) == 0 || target == nil {
		return errors.New("exact JSON payload is invalid")
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return err
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(encoded, payload) {
		return errors.New("exact JSON payload is not canonical")
	}
	return nil
}

func NormalizeUniqueStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func ParseWBTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
