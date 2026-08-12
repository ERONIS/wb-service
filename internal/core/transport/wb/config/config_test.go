package config

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestConfigFormattingAndJSONNeverExposeToken(t *testing.T) {
	t.Parallel()

	const token = "super-secret-token"
	cabinet := CabinetConfig{
		ID:    "main",
		Name:  "Main",
		Token: token,
	}
	configuration := Config{
		BaseURL:  "https://content-api.wildberries.ru",
		Timeout:  time.Second,
		Cabinets: []CabinetConfig{cabinet},
	}

	formatted := []string{
		cabinet.String(),
		cabinet.GoString(),
		configuration.String(),
		configuration.GoString(),
	}
	for _, value := range formatted {
		if strings.Contains(value, token) {
			t.Fatalf("token leaked into formatting: %s", value)
		}
		if !strings.Contains(value, redactedToken) {
			t.Fatalf("redaction marker missing from %s", value)
		}
	}

	encoded, err := json.Marshal(cabinet)
	if err != nil {
		t.Fatalf("marshal cabinet: %v", err)
	}
	if strings.Contains(string(encoded), token) ||
		strings.Contains(string(encoded), "Token") {
		t.Fatalf("token field leaked into JSON: %s", encoded)
	}
}

func TestConfigValidateRejectsAmbiguousCabinetsAndUnsafeToken(t *testing.T) {
	t.Parallel()

	valid := Config{
		BaseURL: "https://content-api.wildberries.ru",
		Timeout: time.Second,
		Cabinets: []CabinetConfig{{
			ID:    "main",
			Name:  "Main",
			Token: "token",
		}},
	}

	tests := []struct {
		name     string
		mutate   func(*Config)
		contains string
	}{
		{
			name: "duplicate ID",
			mutate: func(configuration *Config) {
				configuration.Cabinets = append(
					configuration.Cabinets,
					CabinetConfig{ID: "main", Name: "Other", Token: "other"},
				)
			},
			contains: "duplicated",
		},
		{
			name: "normalized duplicate name",
			mutate: func(configuration *Config) {
				configuration.Cabinets = append(
					configuration.Cabinets,
					CabinetConfig{ID: "other", Name: " main ", Token: "other"},
				)
			},
			contains: "duplicate names",
		},
		{
			name: "token whitespace",
			mutate: func(configuration *Config) {
				configuration.Cabinets[0].Token = " token "
			},
			contains: "surrounding whitespace",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			configuration := valid
			configuration.Cabinets = append(
				[]CabinetConfig(nil),
				valid.Cabinets...,
			)
			test.mutate(&configuration)

			err := configuration.Validate()
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Validate error = %v, want substring %q", err, test.contains)
			}
		})
	}
}
