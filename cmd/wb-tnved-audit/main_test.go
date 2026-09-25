package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestReadEnvValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.wb")
	if err := os.WriteFile(path, []byte("OTHER=value\nexport WB_TOKEN='secret-token'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := readEnvValue(path, "WB_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if value != "secret-token" {
		t.Fatalf("readEnvValue() = %q", value)
	}
}

func TestHasTNVED(t *testing.T) {
	tests := []struct {
		name            string
		characteristics []characteristic
		want            bool
	}{
		{name: "absent", want: false},
		{name: "empty", characteristics: []characteristic{{Name: "ТНВЭД", Value: []any{}}}, want: false},
		{name: "blank", characteristics: []characteristic{{Name: "Код ТН ВЭД", Value: []any{" "}}}, want: false},
		{name: "value", characteristics: []characteristic{{Name: "Код ТН ВЭД", Value: []any{"9019101000"}}}, want: true},
		{name: "compact name", characteristics: []characteristic{{Name: "ТН ВЭД", Value: "9019101000"}}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasTNVED(test.characteristics); got != test.want {
				t.Fatalf("hasTNVED() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestLoadMissingTNVEDStopsAtLimit(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "secret" {
			t.Fatal("authorization token is missing")
		}
		var body bytes.Buffer
		_ = json.NewEncoder(&body).Encode(cardsResponse{Cards: []card{
			{NMID: 1, VendorCode: "one"},
			{NMID: 2, VendorCode: "two", Characteristics: []characteristic{{Name: "ТНВЭД", Value: []any{"123"}}}},
			{NMID: 3, VendorCode: "three"},
		}})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(&body),
		}, nil
	})}

	result, err := loadMissingTNVED(
		context.Background(), client, "https://example.test/cards", "secret", "test-agent", 2,
		log.New(io.Discard, "", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 3 || len(result.Cards) != 2 || result.Cards[0].NMID != 1 || result.Cards[1].NMID != 3 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestLoadMissingTNVEDWithoutLimit(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body bytes.Buffer
		_ = json.NewEncoder(&body).Encode(cardsResponse{Cards: []card{
			{NMID: 1, VendorCode: "one"},
			{NMID: 2, VendorCode: "two", Characteristics: []characteristic{{Name: "ТНВЭД", Value: "123"}}},
			{NMID: 3, VendorCode: "three"},
		}})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(&body),
		}, nil
	})}

	result, err := loadMissingTNVED(
		context.Background(), client, "https://example.test/cards", "secret", "test-agent", 0,
		log.New(io.Discard, "", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 3 || len(result.Cards) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
}
